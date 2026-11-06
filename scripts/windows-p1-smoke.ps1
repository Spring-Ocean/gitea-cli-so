# Verify the actual Windows host and the P1 executable without accessing credentials.
# Run on Windows 10 or Windows 11 with Git installed; a cross-build does not run this check.
param(
    [string]$Binary = (Join-Path $PSScriptRoot '../dist/p1/windows-amd64/sotea.exe')
)

$ErrorActionPreference = 'Stop'
$binaryPath = (Resolve-Path -LiteralPath $Binary).Path
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$stateDirectory = Join-Path $repositoryRoot 'tests/integration/.state'
$temporaryPath = Join-Path ([IO.Path]::GetTempPath()) ('sotea-p1-' + [Guid]::NewGuid().ToString('N'))
$worktree = Join-Path $temporaryPath ('space ' + [char]0x4E2D + [char]0x6587)
$record = [ordered]@{
    schema_version = 1
    time_utc = [DateTime]::UtcNow.ToString('o')
    status = 'failed'
    operating_system = $null
    os_build = $null
    powershell = $PSVersionTable.PSVersion.ToString()
    binary_sha256 = (Get-FileHash -LiteralPath $binaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
    checks = @()
}

try {
    $system = Get-CimInstance -ClassName Win32_OperatingSystem
    $record.operating_system = $system.Caption
    $record.os_build = $system.BuildNumber
    New-Item -ItemType Directory -Path $worktree -Force | Out-Null
    $result = & $binaryPath version --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $result.schema_version -ne 1 -or $result.status -ne 'success') {
        throw 'Version JSON validation failed.'
    }
    $record.checks += 'version_json'
    & git -C $worktree init --quiet --initial-branch=main
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to initialize the isolated Git repository.'
    }
    $root = & $binaryPath --workdir $worktree git -- rev-parse --show-toplevel
    if ($LASTEXITCODE -ne 0 -or [IO.Path]::GetFullPath($root) -ne [IO.Path]::GetFullPath($worktree)) {
        throw 'Working directory or Unicode path validation failed.'
    }
    $record.checks += 'unicode_space_path'
    $ErrorActionPreference = 'Continue'
    & $binaryPath --workdir $worktree git -- rev-parse --verify refs/heads/p1-missing 2>$null
    $gitExitCode = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
    if ($gitExitCode -ne 128) {
        throw 'Git exit status was not preserved.'
    }
    $record.checks += 'git_exit_status'
    $record.status = 'passed'
    Write-Output 'P1 Windows executable smoke checks passed.'
} catch {
    $record.error = $_.Exception.Message
    Write-Error $_ -ErrorAction Continue
} finally {
    New-Item -ItemType Directory -Path $stateDirectory -Force | Out-Null
    $record | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $stateDirectory 'windows-p1-result.json') -Encoding UTF8
    if (Test-Path -LiteralPath $temporaryPath) {
        Remove-Item -LiteralPath $temporaryPath -Recurse -Force
    }
}

if ($record.status -ne 'passed') {
    exit 1
}
