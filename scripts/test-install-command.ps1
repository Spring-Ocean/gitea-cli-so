#Requires -Version 5.1
# 验证文档原命令通过真实 HTTP 原始流解析 UTF-8 安装器。
# 本地 HTTP 仅替换固定脚本地址，已有文件保护阻止安装和外部下载。
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$FixtureBaseURL
)

$ErrorActionPreference = 'Stop'
if ($FixtureBaseURL -notmatch '^http://127[.]0[.]0[.]1:[0-9]+$') {
    throw 'The bootstrap fixture must use an explicit loopback HTTP origin.'
}
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$originalOS = $env:OS
$originalArchitecture = $env:PROCESSOR_ARCHITECTURE
$testDirectory = Join-Path ([IO.Path]::GetTempPath()) ('sotea-bootstrap-' + [Guid]::NewGuid().ToString('N'))

try {
    New-Item -ItemType Directory -Path $testDirectory | Out-Null
    $target = Join-Path $testDirectory 'sotea.exe'
    [IO.File]::WriteAllText($target, 'keep-existing')
    # 非 Windows 只验证解析／绑定／已有文件边界，不模拟 Windows 二进制运行。
    # Windows 完整安装与文件／文本两条入口由独立的安装器回归核验。
    $env:OS = 'Windows_NT'
    $env:PROCESSOR_ARCHITECTURE = 'AMD64'
    $version = [IO.File]::ReadAllText((Join-Path $repositoryRoot 'cmd/sotea/VERSION')).Trim()
    foreach ($document in @('README.md', 'docs/installation.md', 'docs/release-notes.md')) {
        $template = [IO.File]::ReadAllText((Join-Path $repositoryRoot $document))
        $blocks = [regex]::Matches($template, '(?s)```powershell\r?\n(.*?)\r?\n```')
        $commands = @($blocks | Where-Object { $_.Groups[1].Value.Contains('/install.ps1') })
        if ($commands.Count -ne 1) {
            throw "Expected one Windows install command in $document."
        }
        $command = $commands[0].Groups[1].Value.Replace('{{RELEASE_TAG}}', $version)
        $expectedUri = 'https://github.com/Spring-Ocean/gitea-cli-so/releases/download/' + $version + '/install.ps1'
        if (-not $command.Contains($expectedUri)) {
            throw "The install command does not use the exact version URL in $document."
        }
        foreach ($contentType in @('utf8', 'latin1', 'plain', 'binary')) {
            foreach ($withBOM in @($true, $false)) {
                $variant = $contentType + '-' + $(if ($withBOM) { 'bom' } else { 'no-bom' })
                $localCommand = $command.Replace($expectedUri, $FixtureBaseURL + '/' + $variant)
                $rejectedExisting = $false
                try {
                    $bootstrap = [scriptblock]::Create($localCommand + ' -InstallDirectory $testDirectory -NoModifyPath')
                    & $bootstrap
                }
                catch {
                    if ($_.Exception.Message -ne "An installation already exists at $target. Use sotea update install.") {
                        throw
                    }
                    $rejectedExisting = $true
                }
                if (-not $rejectedExisting -or [IO.File]::ReadAllText($target) -ne 'keep-existing') {
                    throw 'The install command did not preserve the existing file boundary.'
                }
                Write-Output "PASS HTTP install command: $document, $variant"
            }
        }
        # HTTP 错误必须保留为下载失败，不能解析错误页面或继续到安装路径。
        # 失败后已有目标仍必须保持不变，不把未知下载解释成可执行源码。
        $failedCommand = $command.Replace($expectedUri, $FixtureBaseURL + '/error')
        $httpRejected = $false
        try {
            & ([scriptblock]::Create($failedCommand + ' -InstallDirectory $testDirectory -NoModifyPath'))
        }
        catch {
            if ($_.Exception.Message -eq "An installation already exists at $target. Use sotea update install.") {
                throw 'The failed HTTP response reached the installer.'
            }
            $httpRejected = $true
        }
        if (-not $httpRejected -or [IO.File]::ReadAllText($target) -ne 'keep-existing') {
            throw 'The failed HTTP request changed the installation boundary.'
        }
        Write-Output "PASS HTTP install command: $document, download failure"
    }
}
finally {
    $env:OS = $originalOS
    $env:PROCESSOR_ARCHITECTURE = $originalArchitecture
    if (Test-Path -LiteralPath $testDirectory) {
        Remove-Item -LiteralPath $testDirectory -Recurse -Force
    }
}
