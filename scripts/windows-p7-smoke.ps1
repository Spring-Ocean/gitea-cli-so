# 在真实 Windows 主机核验候选文件、配置、Git、MCP 与原生替换失败保护。
# 只使用临时目录和回环地址，不调用远端业务、不访问开发者账号或凭证。
param(
    [string]$Binary = (Join-Path $PSScriptRoot 'sotea.exe'),
    [string]$Manifest = (Join-Path $PSScriptRoot 'BUILD-INFO.json'),
    [string]$Result = (Join-Path (Get-Location) 'windows-p7-result.json')
)

$ErrorActionPreference = 'Stop'
$candidatePath = (Resolve-Path -LiteralPath $Binary).Path
$manifestRecord = Get-Content -LiteralPath $Manifest -Raw | ConvertFrom-Json
$resultPath = [IO.Path]::GetFullPath($Result)
if (Test-Path -LiteralPath $resultPath) {
    throw 'The result file already exists. Choose another -Result path to preserve earlier evidence.'
}
$temporaryPath = Join-Path ([IO.Path]::GetTempPath()) ('sotea-p7-' + [Guid]::NewGuid().ToString('N'))
$record = [ordered]@{
    schema_version = 1
    time_utc = [DateTime]::UtcNow.ToString('o')
    status = 'failed'
    operating_system = $null
    os_build = $null
    architecture = $env:PROCESSOR_ARCHITECTURE
    powershell = $PSVersionTable.PSVersion.ToString()
    terminal_session = [bool]$env:WT_SESSION
    candidate = $manifestRecord.version
    package_revision = $manifestRecord.package_revision
    binary_sha256 = (Get-FileHash -LiteralPath $candidatePath -Algorithm SHA256).Hash.ToLowerInvariant()
    checks = @()
    pending = @('GUI navigation and cancellation', 'Windows Credential Manager and isolated Gitea lifecycle', 'Four client approval and cancellation UI', 'Downloaded package trust checks')
}
$savedEnvironment = @{}
$environmentIsolated = $false
$mcpProcess = $null
$mcpStarted = $false

# Invoke-Native 捕获无凭证的本地命令输出及原始退出码。
# 临时放宽 stderr 的 PowerShell 错误转换，随后由调用者检查退出码，不吞掉失败。
function Invoke-Native {
    param([string]$Executable, [string[]]$Arguments)
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $lines = @(& $Executable @Arguments 2> (Join-Path $temporaryPath 'native-stderr.txt'))
        $nativeCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousPreference
    }
    return [PSCustomObject]@{ Code = $nativeCode; Text = ($lines -join "`n") }
}

# Invoke-McpRequest 对本轮私有 stdio 进程发送单个请求并有界等待。
# 只接受对应 id；协议错误和 EOF 立即失败，通知不会被误认为调用结果。
function Invoke-McpRequest {
    param([int]$RequestId, [string]$Method, [hashtable]$Parameters)
    $request = @{ jsonrpc = '2.0'; id = $RequestId; method = $Method; params = $Parameters }
    $mcpProcess.StandardInput.WriteLine(($request | ConvertTo-Json -Depth 30 -Compress))
    $mcpProcess.StandardInput.Flush()
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    while ([DateTime]::UtcNow -lt $deadline) {
        $lineTask = $mcpProcess.StandardOutput.ReadLineAsync()
        $remaining = [Math]::Max(1, [int]($deadline - [DateTime]::UtcNow).TotalMilliseconds)
        if (-not $lineTask.Wait($remaining) -or $null -eq $lineTask.Result) {
            throw 'MCP request timed out or reached EOF.'
        }
        $reply = $lineTask.Result | ConvertFrom-Json
        if ($reply.id -eq $RequestId) {
            if ($reply.error) { throw 'MCP returned a protocol error.' }
            return $reply.result
        }
    }
    throw 'MCP request deadline exceeded.'
}

try {
    if ($env:OS -ne 'Windows_NT' -or -not [Environment]::Is64BitProcess) {
        throw 'Run this script in 64-bit PowerShell on Windows.'
    }
    if ($manifestRecord.os -ne 'windows' -or $manifestRecord.arch -ne 'amd64' -or $record.binary_sha256 -ne $manifestRecord.binary_sha256) {
        throw 'Candidate hash or platform does not match BUILD-INFO.json.'
    }
    $system = Get-CimInstance -ClassName Win32_OperatingSystem
    $record.operating_system = $system.Caption
    $record.os_build = $system.BuildNumber
    New-Item -ItemType Directory -Path $temporaryPath | Out-Null
    $environmentIsolated = $true
    foreach ($variable in @(Get-ChildItem Env: | Where-Object { $_.Name -like 'SOTEA_*' -or $_.Name -like 'GIT_CONFIG*' -or $_.Name -eq 'CI' })) {
        $savedEnvironment[$variable.Name] = $variable.Value
        [Environment]::SetEnvironmentVariable($variable.Name, $null, 'Process')
    }
    $env:CI = '1'
    $env:GIT_CONFIG_NOSYSTEM = '1'
    $env:GIT_CONFIG_GLOBAL = 'NUL'
    $env:GIT_CONFIG_COUNT = '0'
    $configuration = Join-Path $temporaryPath 'config.json'
    $configurationText = '{"version":1,"revision":7,"default_instance":"team","instances":{"team":{"url":"http://127.0.0.1:1","proxy":"direct"}},"repositories":[]}'
    [IO.File]::WriteAllText($configuration, $configurationText)
    $configurationHash = (Get-FileHash -LiteralPath $configuration).Hash
    $versionResult = Invoke-Native -Executable $candidatePath -Arguments @('version', '--json')
    $versionData = $versionResult.Text | ConvertFrom-Json
    if ($versionResult.Code -ne 0 -or $versionData.items[0].data.version -ne $manifestRecord.version) {
        throw 'Version command did not match the candidate manifest.'
    }
    $record.checks += 'candidate_hash_and_version_json'
    $stateArguments = @('--config', $configuration, '--workdir', $temporaryPath, '--instance', 'team', '--json', '--no-interactive', 'auth', 'status')
    $statusResult = Invoke-Native -Executable $candidatePath -Arguments $stateArguments
    if ($statusResult.Code -ne 0 -or ($statusResult.Text | ConvertFrom-Json).status -ne 'success') {
        throw 'The version 1 configuration could not be read.'
    }
    [IO.File]::WriteAllText($configuration, '{"version":2}')
    $unknownHash = (Get-FileHash -LiteralPath $configuration).Hash
    $unknownResult = Invoke-Native -Executable $candidatePath -Arguments $stateArguments
    if ($unknownResult.Code -eq 0 -or ($unknownResult.Text | ConvertFrom-Json).error.code -ne 'CONFIG_ERROR' -or (Get-FileHash -LiteralPath $configuration).Hash -ne $unknownHash) {
        throw 'Unknown configuration was accepted or modified.'
    }
    [IO.File]::WriteAllText($configuration, $configurationText)
    $record.checks += 'config_v1_and_unknown_version_preservation'
    $worktree = Join-Path $temporaryPath ('space ' + [char]0x4E2D + [char]0x6587)
    New-Item -ItemType Directory -Path $worktree | Out-Null
    $gitCommand = Get-Command git -CommandType Application -ErrorAction SilentlyContinue
    if ($gitCommand) {
        $gitVersion = Invoke-Native -Executable $gitCommand.Source -Arguments @('--version')
        $record.git = $gitVersion.Text
        $initialized = Invoke-Native -Executable $gitCommand.Source -Arguments @('-C', $worktree, 'init', '--quiet', '--initial-branch=main')
        if ($initialized.Code -ne 0) { throw 'Unable to initialize the isolated Git repository.' }
        $gitRoot = Invoke-Native -Executable $candidatePath -Arguments @('--workdir', $worktree, 'git', '--', 'rev-parse', '--show-toplevel')
        if ($gitRoot.Code -ne 0 -or [IO.Path]::GetFullPath($gitRoot.Text) -ne [IO.Path]::GetFullPath($worktree)) {
            throw 'Unicode or space path handling failed.'
        }
        $missing = Invoke-Native -Executable $candidatePath -Arguments @('--workdir', $worktree, 'git', '--', 'rev-parse', '--verify', 'refs/heads/p7-missing')
        if ($missing.Code -ne 128) { throw 'Git exit status was not preserved.' }
        $record.checks += 'git_unicode_space_path_and_exit_status'
    } else {
        $record.pending += 'Git executable unavailable; local Git path untested'
    }
    $installed = Join-Path $worktree 'sotea.exe'
    $staged = Join-Path $worktree 'sotea.new.exe'
    $backup = Join-Path $worktree 'sotea.previous.exe'
    Copy-Item -LiteralPath $candidatePath -Destination $installed
    Copy-Item -LiteralPath $candidatePath -Destination $staged
    $heldFile = [IO.File]::Open($installed, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::None)
    $replacementRejected = $false
    try {
        try { [IO.File]::Replace($staged, $installed, $backup) }
        catch [IO.IOException] { $replacementRejected = $true }
    } finally {
        $heldFile.Dispose()
    }
    if (-not $replacementRejected -or (Get-FileHash -LiteralPath $installed).Hash.ToLowerInvariant() -ne $record.binary_sha256) {
        throw 'A locked replacement did not preserve the installed program.'
    }
    [IO.File]::Replace($staged, $installed, $backup)
    if ((Get-FileHash -LiteralPath $backup).Hash.ToLowerInvariant() -ne $record.binary_sha256) {
        throw 'The replacement backup is missing or changed.'
    }
    $installedVersion = Invoke-Native -Executable $installed -Arguments @('version')
    if ($installedVersion.Code -ne 0) { throw 'The replaced program did not run.' }
    $record.checks += 'file_replace_success_and_locked_failure_preservation'
    # 配置路径来自本轮临时根，不含引号；按 Windows 原生命令行规则包围空格路径。
    # 协议进程采用默认只读远端模式，不启动 Git 或访问工作目录外的文件。
    if ($configuration.Contains('"')) { throw 'Unexpected quote in temporary path.' }
    $startInfo = New-Object Diagnostics.ProcessStartInfo
    $startInfo.FileName = $installed
    $startInfo.Arguments = '--config "' + $configuration + '" mcp serve'
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $mcpProcess = New-Object Diagnostics.Process
    $mcpProcess.StartInfo = $startInfo
    if (-not $mcpProcess.Start()) { throw 'Unable to start MCP.' }
    $mcpStarted = $true
    $diagnosticTask = $mcpProcess.StandardError.ReadToEndAsync()
    $initialized = Invoke-McpRequest -RequestId 1 -Method 'initialize' -Parameters @{ protocolVersion = '2025-11-25'; capabilities = @{}; clientInfo = @{ name = 'sotea-p7-windows'; version = '1' } }
    if ($initialized.protocolVersion -ne '2025-11-25') { throw 'Unexpected MCP protocol.' }
    $mcpProcess.StandardInput.WriteLine('{"jsonrpc":"2.0","method":"notifications/initialized"}')
    $mcpProcess.StandardInput.Flush()
    $inventory = Invoke-McpRequest -RequestId 2 -Method 'tools/list' -Parameters @{}
    if (@($inventory.tools).Count -ne 27) { throw 'Unexpected default MCP tool count.' }
    $contextResult = Invoke-McpRequest -RequestId 3 -Method 'tools/call' -Parameters @{ name = 'sotea_context_get'; arguments = @{ instance = 'team' } }
    if ($contextResult.isError -or $contextResult.structuredContent.status -ne 'success') {
        throw 'MCP context resolution failed.'
    }
    $mcpProcess.StandardInput.Close()
    if (-not $mcpProcess.WaitForExit(15000) -or $mcpProcess.ExitCode -ne 0 -or -not $diagnosticTask.Wait(5000) -or $diagnosticTask.Result -ne '') {
        throw 'MCP EOF or diagnostic isolation failed.'
    }
    $record.checks += 'mcp_27_readonly_tools_context_and_eof'
    Remove-Item -LiteralPath $installed
    if ((Get-FileHash -LiteralPath $configuration).Hash -ne $configurationHash) {
        throw 'The installation lifecycle changed configuration.'
    }
    $record.checks += 'uninstall_preserves_config'
    $record.status = 'smoke_passed_full_acceptance_pending'
    Write-Output 'P7 Windows smoke checks passed. Complete the manual acceptance checklist separately.'
} catch {
    $record.error_type = $_.Exception.GetType().FullName
    Write-Error $_ -ErrorAction Continue
} finally {
    if ($mcpStarted) {
        if (-not $mcpProcess.HasExited) {
            $mcpProcess.Kill()
            $mcpProcess.WaitForExit(5000) | Out-Null
        }
    }
    if ($mcpProcess) {
        $mcpProcess.Dispose()
    }
    if ($environmentIsolated) {
        foreach ($variable in @(Get-ChildItem Env: | Where-Object { $_.Name -like 'SOTEA_*' -or $_.Name -like 'GIT_CONFIG*' -or $_.Name -eq 'CI' })) {
            [Environment]::SetEnvironmentVariable($variable.Name, $null, 'Process')
        }
        foreach ($name in $savedEnvironment.Keys) {
            [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
        }
    }
    $record | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $resultPath -Encoding UTF8
    if (Test-Path -LiteralPath $temporaryPath) {
        Remove-Item -LiteralPath $temporaryPath -Recurse -Force
    }
}
if ($record.status -eq 'failed') { exit 1 }
