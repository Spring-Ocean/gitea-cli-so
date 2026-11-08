#Requires -Version 5.1
# 为安装脚本提供固定 GitHub 附件，不访问真实网络或写用户 PATH。
# 由 Python 为每个用例创建独立目录，并核对失败后目标文件未改变。
$ErrorActionPreference = 'Stop'

# Invoke-WebRequest 仅复制场景中已准备的附件并记录固定来源。
# 未授权主机、路径、标签及模拟下载失败都在任何安装提交前拒绝。
function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile, [int]$TimeoutSec)
    [IO.File]::AppendAllText((Join-Path $env:SOTEA_INSTALL_FIXTURE 'requests'), $Uri + "`n")
    $prefix = 'https://github.com/Spring-Ocean/gitea-cli-so/releases/download/' + $env:SOTEA_INSTALL_TAG + '/'
    if (-not $Uri.StartsWith($prefix) -or $env:SOTEA_INSTALL_SCENARIO -eq 'download') {
        throw 'Fixture download rejected.'
    }
    $name = $Uri.Substring($prefix.Length)
    if ($name.Contains('/') -or $name.Contains('\')) {
        throw 'Fixture path rejected.'
    }
    Copy-Item -LiteralPath (Join-Path $env:SOTEA_INSTALL_FIXTURE $name) -Destination $OutFile
}

# Invoke-RestMethod 只模拟稳定发布解析，不提供任何远端任意 URL。
# wrong-version 场景只改变报告标签，确保候选二进制验证阻断错误安装。
function Invoke-RestMethod {
    param([string]$Uri, [hashtable]$Headers, [int]$TimeoutSec)
    if ($Uri -cne 'https://api.github.com/repos/Spring-Ocean/gitea-cli-so/releases/latest') {
        throw 'Fixture API rejected.'
    }
    return @{ tag_name = $env:SOTEA_INSTALL_TAG; draft = $false; prerelease = $false }
}

try {
    $arguments = @{
        InstallDirectory = $env:SOTEA_INSTALL_TARGET
        NoModifyPath = $true
    }
    if ($env:SOTEA_INSTALL_SCENARIO -ne 'latest') {
        $arguments.Version = $env:SOTEA_INSTALL_TAG
    }
    if ($env:SOTEA_INSTALL_SCENARIO -eq 'invalid-tag') {
        $arguments.Version = '../unsafe'
    }
    if ($env:SOTEA_INSTALL_SCENARIO -eq 'wrong-version') {
        # 复制同一 ZIP 为另一标签，并重新计算摘要，让错误准确落在程序版本检查。
        # 原始候选及发布附件保持不变，不把校验和失败冒充版本核对通过。
        $originalTag = $env:SOTEA_INSTALL_TAG
        $wrongTag = 'v99.0.0'
        $originalArchive = Join-Path $env:SOTEA_INSTALL_FIXTURE "sotea-$originalTag-windows-amd64.zip"
        $wrongArchive = Join-Path $env:SOTEA_INSTALL_FIXTURE "sotea-$wrongTag-windows-amd64.zip"
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $source = [IO.Compression.ZipFile]::OpenRead($originalArchive)
        $destination = [IO.Compression.ZipFile]::Open($wrongArchive, [IO.Compression.ZipArchiveMode]::Create)
        try {
            $entry = $destination.CreateEntry("sotea-$wrongTag-windows-amd64/sotea.exe")
            $inputStream = $source.Entries[0].Open()
            $outputStream = $entry.Open()
            try {
                $inputStream.CopyTo($outputStream)
            }
            finally {
                $inputStream.Dispose()
                $outputStream.Dispose()
            }
        }
        finally {
            $source.Dispose()
            $destination.Dispose()
        }
        $hash = (Get-FileHash -LiteralPath $wrongArchive -Algorithm SHA256).Hash.ToLowerInvariant()
        [IO.File]::WriteAllText((Join-Path $env:SOTEA_INSTALL_FIXTURE 'SHA256SUMS'), "$hash  sotea-$wrongTag-windows-amd64.zip`n")
        $arguments.Version = $wrongTag
        $env:SOTEA_INSTALL_TAG = $wrongTag
    }
    & (Join-Path $PSScriptRoot 'install.ps1') @arguments
    exit 0
}
catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
