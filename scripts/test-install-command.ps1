#Requires -Version 5.1
# 验证公开文档的一键命令能解析带 BOM 的下载文本并绑定参数。
# 只读取发布脚本和合成目标文件；不联网、不安装程序或写用户 PATH。
[CmdletBinding()]
param(
    [string]$InstallerPath = (Join-Path $PSScriptRoot 'install.ps1')
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$script:downloadText = ''
$script:expectedUri = ''
$script:requestCount = 0
$originalOS = $env:OS
$originalArchitecture = $env:PROCESSOR_ARCHITECTURE
$testDirectory = Join-Path ([IO.Path]::GetTempPath()) ('sotea-bootstrap-' + [Guid]::NewGuid().ToString('N'))

# Invoke-RestMethod 模拟 HTTP 文本解码后保留 U+FEFF 的响应。
# 只接受文档中的精确安装脚本地址，不允许原始安装流程请求外网。
function Invoke-RestMethod {
    [CmdletBinding()]
    param([Parameter(Position = 0)][string]$Uri)

    if ($Uri -cne $script:expectedUri) {
        throw 'The install command requested an unexpected URL.'
    }
    $script:requestCount++
    return $script:downloadText
}

try {
    New-Item -ItemType Directory -Path $testDirectory | Out-Null
    $target = Join-Path $testDirectory 'sotea.exe'
    [IO.File]::WriteAllText($target, 'keep-existing')
    # macOS 的 PowerShell 也可核验解析和绑定，现有目标保护在任何下载／写入之前返回。
    # Windows 的原生文件执行／安装回归仍由独立的 test-installers.py 负责。
    $env:OS = 'Windows_NT'
    $env:PROCESSOR_ARCHITECTURE = 'AMD64'
    $bytes = [IO.File]::ReadAllBytes($InstallerPath)
    if ($bytes.Length -lt 3 -or $bytes[0] -ne 239 -or $bytes[1] -ne 187 -or $bytes[2] -ne 191) {
        throw 'The installer must retain its UTF-8 BOM for Windows PowerShell file execution.'
    }
    $rawSource = [Text.Encoding]::UTF8.GetString($bytes)
    $version = [IO.File]::ReadAllText((Join-Path $repositoryRoot 'cmd/sotea/VERSION')).Trim()
    foreach ($document in @('README.md', 'docs/installation.md', 'docs/release-notes.md')) {
        $template = [IO.File]::ReadAllText((Join-Path $repositoryRoot $document))
        $blocks = [regex]::Matches($template, '(?s)```powershell\r?\n(.*?)\r?\n```')
        $commands = @($blocks | Where-Object { $_.Groups[1].Value.Contains('/install.ps1') })
        if ($commands.Count -ne 1) {
            throw "Expected one Windows install command in $document."
        }
        $command = $commands[0].Groups[1].Value.Replace('{{RELEASE_TAG}}', $version)
        $script:expectedUri = 'https://github.com/Spring-Ocean/gitea-cli-so/releases/download/' + $version + '/install.ps1'
        foreach ($withBOM in @($true, $false)) {
            $script:downloadText = $rawSource
            if (-not $withBOM) {
                $script:downloadText = $rawSource.Substring(1)
            }
            $script:requestCount = 0
            $rejectedExisting = $false
            try {
                # 执行文档原命令，只追加隔离安装目录和不改 PATH；保留 Version 实参。
                # 正确解析后必须到达安装器已有文件保护，解析错误不可当作通过。
                $bootstrap = [scriptblock]::Create($command + ' -InstallDirectory $testDirectory -NoModifyPath')
                & $bootstrap
            }
            catch {
                if ($_.Exception.Message -ne "An installation already exists at $target. Use sotea update install.") {
                    throw
                }
                $rejectedExisting = $true
            }
            if (-not $rejectedExisting -or $script:requestCount -ne 1 -or [IO.File]::ReadAllText($target) -ne 'keep-existing') {
                throw 'The install command did not preserve the existing file boundary.'
            }
            Write-Output "PASS install command: $document, BOM=$withBOM"
        }
    }
}
finally {
    $env:OS = $originalOS
    $env:PROCESSOR_ARCHITECTURE = $originalArchitecture
    if (Test-Path -LiteralPath $testDirectory) {
        Remove-Item -LiteralPath $testDirectory -Recurse -Force
    }
}
