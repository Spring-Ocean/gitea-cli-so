# 在隔离目标中验证真实PowerShell安装器的认证请求边界。
# API使用固定响应替身，附件调用在写文件前停止，不读取任何真实凭证。
param([string]$InstallerPath = (Join-Path $PSScriptRoot 'install.ps1'))
$ErrorActionPreference = 'Stop'
Add-Type @'
public sealed class SoteaAuthResponse { public int StatusCode = 401; }
public sealed class SoteaAuthRejected : System.Exception {
    public SoteaAuthResponse Response = new SoteaAuthResponse();
}
'@
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('sotea-auth-test-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temporary | Out-Null
$env:OS = 'Windows_NT'
$env:PROCESSOR_ARCHITECTURE = 'AMD64'
$global:SoteaAuthCalls = 0

# Invoke-RestMethod 检查固定API和预期认证，模拟401后匿名成功。
# 只使用合成token；任何重定向授权、错误顺序或额外重试都直接失败。
function Invoke-RestMethod {
    param([string]$Uri, [hashtable]$Headers, [int]$MaximumRedirection, [int]$TimeoutSec)
    if ($Uri -cne 'https://api.github.com/repos/Spring-Ocean/gitea-cli-so/releases/latest' -or $MaximumRedirection -ne 0) { throw 'Unexpected API scope.' }
    $global:SoteaAuthCalls++
    $expected = switch ($global:SoteaAuthScenario) {
        'dedicated' { 'Bearer first' }
        'gh-env' { 'Bearer second' }
        'github-env' { 'Bearer third' }
        'rejected' { if ($global:SoteaAuthCalls -eq 1) { 'Bearer rejected' } else { $null } }
        default { $null }
    }
    if ($Headers['Authorization'] -cne $expected) { throw 'Unexpected authorization.' }
    if ($global:SoteaAuthScenario -eq 'rejected' -and $global:SoteaAuthCalls -eq 1) { throw [SoteaAuthRejected]::new() }
    return @{ tag_name = 'v1.0.0'; draft = $false; prerelease = $false }
}

# Invoke-WebRequest 在下载入口验证无凭证后立即停止安装流程。
# 不创建可执行文件、不改PATH，标记只表示已进入正确的下载边界。
function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile, [int]$TimeoutSec, [hashtable]$Headers)
    if ($Headers -and $Headers['Authorization']) { throw 'Credential leaked to asset.' }
    if ($Uri -cne 'https://github.com/Spring-Ocean/gitea-cli-so/releases/download/v1.0.0/sotea-v1.0.0-windows-amd64.zip') { throw 'Unexpected asset.' }
    throw 'AUTH_FIXTURE_COMPLETE'
}
try {
    # 只载入凭证函数的实际语法节点，使用当前PowerShell作为合成子进程。
    # 检查stdin、工作目录和环境隔离，不执行gh／git或任何真实凭证操作。
    $parseTokens = $null
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($InstallerPath, [ref]$parseTokens, [ref]$parseErrors)
    if ($parseErrors.Count) { throw 'Installer parsing failed.' }
    $queryFunction = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Invoke-CredentialQuery' }, $true)
    $queryFixture = $queryFunction.Extent.Text
    $queryFixture = $queryFixture.Replace("catch { return '' }", 'catch { throw }')
    $queryFixture = $queryFixture.Replace('if ($process.ExitCode -ne 0) { return '''' }', 'if ($process.ExitCode -ne 0) { throw ''Synthetic credential child exited unsuccessfully.'' }')
    Invoke-Expression $queryFixture
    $env:GIT_DIR = 'injected-repository'
    $env:GIT_CONFIG_COUNT = '99'
    $executable = [Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
    $childArguments = '-NoProfile -NonInteractive -Command "if ($env:GIT_DIR -or $env:GIT_CONFIG_COUNT -or $env:GIT_TERMINAL_PROMPT -ne ''0'' -or $env:GCM_INTERACTIVE -ne ''Never'') { exit 2 }; if ([Console]::ReadLine() -ne ''query'') { exit 3 }; [Console]::Write(''helper-fixture'')"'
    $queried = Invoke-CredentialQuery $executable $childArguments "query`n" $temporary
    if ($queried -cne 'helper-fixture') { throw 'Credential subprocess isolation failed.' }
    Write-Output 'PASS PowerShell credential subprocess isolation'
    foreach ($global:SoteaAuthScenario in @('dedicated', 'gh-env', 'github-env', 'anonymous', 'rejected')) {
        $env:SOTEA_GITHUB_TOKEN = if ($global:SoteaAuthScenario -eq 'rejected') { 'rejected' } else { 'first' }
        $env:GH_TOKEN = 'second'
        $env:GITHUB_TOKEN = 'third'
        if ($global:SoteaAuthScenario -in @('gh-env', 'github-env')) { $env:SOTEA_GITHUB_TOKEN = '' }
        if ($global:SoteaAuthScenario -eq 'github-env') { $env:GH_TOKEN = '' }
        $mode = if ($global:SoteaAuthScenario -eq 'anonymous') { 'anonymous' } else { 'auto' }
        $global:SoteaAuthCalls = 0
        try {
            & $InstallerPath -InstallDirectory (Join-Path $temporary $global:SoteaAuthScenario) -NoModifyPath -GitHubAuth $mode
            throw 'Installer unexpectedly completed.'
        }
        catch {
            if ($_.Exception.Message -ne 'AUTH_FIXTURE_COMPLETE') { throw }
        }
        $expectedCount = if ($global:SoteaAuthScenario -eq 'rejected') { 2 } else { 1 }
        if ($global:SoteaAuthCalls -ne $expectedCount) { throw 'Unexpected API retry count.' }
        Write-Output "PASS PowerShell installer authentication: $global:SoteaAuthScenario"
    }
}
finally { Remove-Item -LiteralPath $temporary -Recurse -Force }
