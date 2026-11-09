#Requires -Version 5.1
<#
从固定 GitHub Release 安装 Windows amd64 程序。
校验后以不覆盖方式提交到用户目录，不要求管理员权限，也不改变执行策略。
#>
[CmdletBinding()]
param(
    [string]$Version = '',
    [string]$InstallDirectory = (Join-Path $env:LOCALAPPDATA 'Programs\sotea'),
    [switch]$NoModifyPath,
    [ValidateSet("auto", "anonymous")]
    [string]$GitHubAuth = $(if ($env:SOTEA_GITHUB_AUTH) { $env:SOTEA_GITHUB_AUTH } else { "auto" })
)

# Invoke-CredentialQuery 在隔离目录查询只读凭证，禁止提示且限制为五秒。
# 子进程输出仅留内存，不转发stderr，超时立即终止并释放进程资源。
function Invoke-CredentialQuery([string]$Name, [string]$Arguments, [string]$InputText, [string]$Directory) {
    $application = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $application) { return '' }
    $process = New-Object Diagnostics.Process
    try {
        $info = New-Object Diagnostics.ProcessStartInfo
        $info.FileName = $application.Source
        $info.Arguments = $Arguments
        $info.WorkingDirectory = $Directory
        $info.UseShellExecute = $false
        $info.CreateNoWindow = $true
        $info.RedirectStandardInput = $true
        $info.RedirectStandardOutput = $true
        $info.RedirectStandardError = $true
        $info.StandardOutputEncoding = [Text.UTF8Encoding]::new($false)
        foreach ($key in @($info.EnvironmentVariables.Keys)) {
            if ($key -like 'GIT_*') { $info.EnvironmentVariables.Remove($key) }
        }
        $info.EnvironmentVariables['GIT_TERMINAL_PROMPT'] = '0'
        $info.EnvironmentVariables['GIT_ASKPASS'] = ''
        $info.EnvironmentVariables['GCM_INTERACTIVE'] = 'Never'
        $info.EnvironmentVariables['GH_PROMPT_DISABLED'] = '1'
        $process.StartInfo = $info
        # .NET Framework初始化stdin的AutoFlush会写入Console.InputEncoding的BOM。
        # 新版指定单个进程编码，旧版仅在Start期间切换并立即恢复控制台编码。
        $utf8 = [Text.UTF8Encoding]::new($false)
        if ($info.PSObject.Properties['StandardInputEncoding']) {
            $info.StandardInputEncoding = $utf8
            $started = $process.Start()
        }
        else {
            $previousInputEncoding = [Console]::InputEncoding
            try {
                [Console]::InputEncoding = $utf8
                $started = $process.Start()
            }
            finally { [Console]::InputEncoding = $previousInputEncoding }
        }
        if (-not $started) { return '' }
        $credentialChars = New-Object char[] 16385
        $readOutput = $process.StandardOutput.ReadBlockAsync($credentialChars, 0, $credentialChars.Length)
        $process.StandardInput.Write($InputText)
        $process.StandardInput.Close()
        if (-not $process.WaitForExit(5000)) {
            $process.Kill()
            $process.WaitForExit()
            return ''
        }
        if ($process.ExitCode -ne 0) { return '' }
        if (-not $readOutput.Wait(500) -or $readOutput.Result -gt 16384) { return '' }
        $value = [string]::new($credentialChars, 0, $readOutput.Result)
        return $value.TrimEnd([char[]]"`r`n")
    }
    catch { return '' }
    finally { $process.Dispose() }
}

# Get-GitHubCredential 按环境、gh、Git helper顺序查询固定github.com的token。
# 不修改凭证存储，SSH及浏览器会话不参与；无可用凭证时返回空值。
function Get-GitHubCredential {
    foreach ($value in @($env:SOTEA_GITHUB_TOKEN, $env:GH_TOKEN, $env:GITHUB_TOKEN)) {
        if ($value -cmatch '^[A-Za-z0-9_.-]+\z') { return $value }
    }
    $directory = Join-Path ([IO.Path]::GetTempPath()) ('sotea-github-auth-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $directory | Out-Null
    try {
        $value = Invoke-CredentialQuery 'gh' 'auth token --hostname github.com' '' $directory
        if ($value -cmatch '^[A-Za-z0-9_.-]+\z') { return $value }
        $reply = Invoke-CredentialQuery 'git' '-c core.askPass= -c credential.interactive=false credential fill' "protocol=https`nhost=github.com`npath=Spring-Ocean/gitea-cli-so`n`n" $directory
        $fields = @{}
        foreach ($line in ($reply -split "`n")) {
            $pair = $line.TrimEnd([char]13) -split '=', 2
            if ($pair.Count -eq 2) { $fields[$pair[0]] = $pair[1] }
        }
        if ($fields['protocol'] -ceq 'https' -and $fields['host'] -ceq 'github.com' -and (-not $fields['path'] -or $fields['path'] -ceq 'Spring-Ocean/gitea-cli-so') -and $fields['password'] -cmatch '^[A-Za-z0-9_.-]+\z') {
            return $fields['password']
        }
        return ''
    }
    finally { Remove-Item -LiteralPath $directory -Recurse -Force }
}

$ErrorActionPreference = 'Stop'
$downloadDirectory = $null
$stagedBinary = $null
$originalProtocol = [Net.ServicePointManager]::SecurityProtocol

try {
    if ($env:OS -ne 'Windows_NT' -or -not [Environment]::Is64BitProcess) {
        throw 'Run this installer in 64-bit Windows PowerShell or PowerShell.'
    }
    if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64') {
        throw 'Only Windows amd64 is supported. Windows ARM64 is not a supported installation target.'
    }
    if (-not [IO.Path]::IsPathRooted($InstallDirectory) -or $InstallDirectory -match '[\r\n]') {
        throw 'The installation directory must be an absolute path without newlines.'
    }
    $InstallDirectory = [IO.Path]::GetFullPath($InstallDirectory)
    $target = Join-Path $InstallDirectory 'sotea.exe'
    if (Test-Path -LiteralPath $target) {
        throw "An installation already exists at $target. Use sotea update install."
    }
    [Net.ServicePointManager]::SecurityProtocol = $originalProtocol -bor [Net.SecurityProtocolType]::Tls12
    $repository = 'https://github.com/Spring-Ocean/gitea-cli-so'
    if (-not $Version) {
        $headers = @{ Accept = 'application/vnd.github+json' }
        if ($GitHubAuth -eq 'auto') {
            $token = Get-GitHubCredential
            if ($token) { $headers['Authorization'] = "Bearer $token" }
            $token = $null
        }
        try {
            try {
                $release = Invoke-RestMethod -Uri 'https://api.github.com/repos/Spring-Ocean/gitea-cli-so/releases/latest' -Headers $headers -MaximumRedirection 0 -TimeoutSec 60
            }
            catch {
                if ($_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 401 -and $headers.ContainsKey('Authorization')) {
                    Write-Warning 'GitHub credential was rejected; retrying anonymously once.'
                    $headers.Remove('Authorization')
                    $release = Invoke-RestMethod -Uri 'https://api.github.com/repos/Spring-Ocean/gitea-cli-so/releases/latest' -Headers $headers -MaximumRedirection 0 -TimeoutSec 60
                }
                else { throw }
            }
        }
        catch {
            throw 'Unable to resolve a stable release. GitHub may be unavailable or rate limited; retry later or pass an exact -Version to avoid the API query.'
        }
        finally { $headers.Remove('Authorization') }
        if ($release.draft -or $release.prerelease) {
            throw 'GitHub did not return a stable public release.'
        }
        $Version = [string]$release.tag_name
    }
    if ($Version.Length -gt 128 -or $Version -cnotmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$') {
        throw 'Use an exact release tag such as v0.1.0 or v0.1.0-rc.1.'
    }
    $downloadDirectory = Join-Path ([IO.Path]::GetTempPath()) ('sotea-download-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $downloadDirectory | Out-Null
    $folder = "sotea-$Version-windows-amd64"
    $archiveName = "$folder.zip"
    $archivePath = Join-Path $downloadDirectory $archiveName
    $checksumPath = Join-Path $downloadDirectory 'SHA256SUMS'
    $base = "$repository/releases/download/$Version"
    Write-Host "Downloading $Version for Windows amd64..."
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$archiveName" -OutFile $archivePath -TimeoutSec 300
    Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $checksumPath -TimeoutSec 60
    if ((Get-Item -LiteralPath $archivePath).Length -gt 134217728 -or (Get-Item -LiteralPath $checksumPath).Length -gt 1048576) {
        throw 'The release download exceeds its size limit.'
    }
    $matchesFound = @(
        foreach ($line in [IO.File]::ReadAllLines($checksumPath)) {
            if ($line -cmatch ('^([0-9a-fA-F]{64})  ' + [regex]::Escape($archiveName) + '$')) {
                $Matches[1]
            }
        }
    )
    if ($matchesFound.Count -ne 1) {
        throw 'SHA256SUMS must contain exactly one matching archive.'
    }
    $actual = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash
    if ($actual -ine $matchesFound[0]) {
        throw 'Checksum verification failed. Nothing was installed.'
    }

    # 只解压精确二进制条目，不让 ZIP 内的路径控制文件系统写入。
    # 展开上限与普通文件检查在运行候选前执行，任何失败都保留原有目标。
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [IO.Compression.ZipFile]::OpenRead($archivePath)
    try {
        $entries = @($archive.Entries | Where-Object { $_.FullName -ceq "$folder/sotea.exe" })
        if ($entries.Count -ne 1 -or $entries[0].Length -le 0 -or $entries[0].Length -gt 201326592) {
            throw 'The archive must contain exactly one bounded executable.'
        }
        $kind = ($entries[0].ExternalAttributes -shr 16) -band 61440
        if ($kind -ne 0 -and $kind -ne 32768) {
            throw 'The executable must be a regular file.'
        }
        New-Item -ItemType Directory -Path $InstallDirectory -Force | Out-Null
        $stagedBinary = Join-Path $InstallDirectory ('.sotea-install-' + [Guid]::NewGuid().ToString('N') + '.exe')
        [IO.Compression.ZipFileExtensions]::ExtractToFile($entries[0], $stagedBinary, $false)
    }
    finally {
        $archive.Dispose()
    }
    $reported = & $stagedBinary version --no-interactive
    if ($LASTEXITCODE -ne 0 -or $reported -cne "sotea $Version") {
        throw 'The executable could not run or its version does not match the requested release.'
    }
    [IO.File]::Move($stagedBinary, $target)
    $stagedBinary = $null
    Write-Host "Installed $Version at $target"

    if (-not $NoModifyPath) {
        try {
            # 保留 PATH 的原始变量引用和注册表类型，不展开或重写既有条目。
            # 仅追加本次安装目录；随后通知桌面环境，使新启动的终端读取新值。
            $environmentKey = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
            try {
                $userPath = [string]$environmentKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
                $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
                if ($environmentKey.GetValueNames() -icontains 'Path') {
                    $kind = $environmentKey.GetValueKind('Path')
                }
                $pathEntries = @($userPath -split ';' | Where-Object { $_ })
                if ($pathEntries -inotcontains $InstallDirectory) {
                    $newPath = $InstallDirectory
                    if ($userPath) {
                        $newPath += ';' + $userPath
                    }
                    $environmentKey.SetValue('Path', $newPath, $kind)
                }
            }
            finally {
                $environmentKey.Dispose()
            }
            if (@($env:Path -split ';') -inotcontains $InstallDirectory) {
                $env:Path = $InstallDirectory + ';' + $env:Path
            }
            if (-not ('Sotea.InstallEnvironment' -as [type])) {
                Add-Type -Namespace Sotea -Name InstallEnvironment -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("user32.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode)]
public static extern System.IntPtr SendMessageTimeout(System.IntPtr window, uint message, System.UIntPtr wparam, string lparam, uint flags, uint timeout, out System.UIntPtr result);
'@
            }
            $broadcastResult = [UIntPtr]::Zero
            $notified = [Sotea.InstallEnvironment]::SendMessageTimeout([IntPtr]65535, 26, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$broadcastResult)
            if ($notified -eq [IntPtr]::Zero) {
                Write-Warning 'PATH was saved, but desktop applications could not be notified. Run sotea by its full path until the environment is refreshed.'
            }
        }
        catch {
            Write-Warning "Installed successfully, but could not update user PATH. Add $InstallDirectory manually."
        }
    }
    Write-Host "Open a new terminal, or run $target directly."
}
finally {
    [Net.ServicePointManager]::SecurityProtocol = $originalProtocol
    if ($stagedBinary -and (Test-Path -LiteralPath $stagedBinary)) {
        Remove-Item -LiteralPath $stagedBinary -Force
    }
    if ($downloadDirectory -and (Test-Path -LiteralPath $downloadDirectory)) {
        Remove-Item -LiteralPath $downloadDirectory -Recurse -Force
    }
}
