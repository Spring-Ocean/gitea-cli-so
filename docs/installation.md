# 安装、升级与卸载

sotea 的一个可执行文件同时提供 CLI、交互菜单和本地 MCP 服务。运行预编译程序无需安装 Go；本地仓库发现、Git 凭证复用和 Git 命令需要系统 Git。

## 选择平台

| 文件平台标识 | 适用设备 | 压缩格式 |
| --- | --- | --- |
| darwin-arm64 | Apple Silicon Mac | tar.gz |
| darwin-amd64 | Intel Mac | tar.gz |
| windows-amd64 | Windows 10 22H2／Windows 11，Intel 或 AMD 64 位 | zip |

支持范围与实际验证状态见 [兼容性说明](compatibility.md)。公开安装包通过仓库 GitHub Releases 的 Assets 分发；下载地址以对应版本实际公开的附件为准。GitHub 自动生成的 Source code 是源码，不是预编译程序。包名包含具体版本、平台，历史内部候选还可能包含打包修订号，请以实际文件为准。

## 脚本首次安装

安装脚本自动选择架构，从固定仓库下载压缩包和 SHA256SUMS，核对 SHA-256 与程序版本，再安装到用户目录。无需管理员权限；不覆盖已有程序，已有安装使用 `sotea update install`。无版本参数时只选择最新稳定版；目前为 RC，必须显式指定 `v0.1.0-rc.4`。没有稳定版时会清晰报错，不悄悄切换到 RC。

macOS，在 Terminal／iTerm2 复制整段执行：

```sh
(
    set -e
    installer=$(mktemp)
    trap 'rm -f "$installer"' EXIT
    curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL \
        https://github.com/Spring-Ocean/gitea-cli-so/releases/download/v0.1.0-rc.4/install.sh \
        -o "$installer"
    sh "$installer" --version v0.1.0-rc.4
)
```

默认安装至 `~/.local/bin/sotea`，为 zsh 的 `~/.zshrc` 或 bash 已有的登录配置追加一条 PATH 设置，不改写原有内容；bash 没有登录配置时创建 `~/.bash_profile`。重开终端后执行 `sotea`；当前窗口也可直接执行 `~/.local/bin/sotea`。`--no-modify-path` 跳过设置；`--install-dir /absolute/path` 使用自定义目录，自行配置 PATH。

Windows，在 64 位 Windows PowerShell 5.1 或 PowerShell 中执行：

```powershell
& ([scriptblock]::Create([Text.Encoding]::UTF8.GetString((Invoke-WebRequest -UseBasicParsing -ErrorAction Stop -Uri 'https://github.com/Spring-Ocean/gitea-cli-so/releases/download/v0.1.0-rc.4/install.ps1').RawContentStream.ToArray()).TrimStart([char]0xFEFF))) -Version v0.1.0-rc.4
```

上例从响应的 `RawContentStream` 取得原始字节，明确按 UTF-8 解码并移除前导 BOM，再在当前 PowerShell 中执行，不更改系统执行策略。`rc.1` 至 `rc.3` 的旧下载文本命令可能受 HTTP 字符集解码影响：BOM 被误解码后不再是 U+FEFF，仅 `TrimStart` 无法修复，因此仍会在 `CmdletBinding`／`param` 处报错。上面的新命令适用于原有附件，无需删除 BOM 或更改脚本头。

默认安装至 `%LOCALAPPDATA%\Programs\sotea\sotea.exe`，保留用户 PATH 原有项并添加安装目录，同时更新当前窗口 PATH。`-NoModifyPath` 跳过；`-InstallDirectory 'C:\your\directory'` 自定义目录。仅支持 amd64，不承诺 Windows ARM64。

也可先从同一 Release 下载脚本，审阅后以 `sh ./install.sh --version TAG` 或 `& .\install.ps1 -Version TAG` 执行；组织限制脚本执行时按组织策略操作。稳定版发布后，可将下载地址改为 `releases/latest/download/install.sh`／`install.ps1` 并省略版本参数。离线环境或不希望执行安装脚本时使用下方手动步骤。

## 校验与解压

取得与压缩包配套的 SHA256SUMS，从可信渠道核对其中的摘要。macOS 计算单个文件摘要：

```sh
shasum -a 256 /path/to/sotea-package.tar.gz
```

若下载了 SHA256SUMS 中的全部文件，也可以在该目录执行 `shasum -a 256 -c SHA256SUMS`。然后解压实际包文件：

```sh
tar -xzf /path/to/sotea-package.tar.gz
```

Windows PowerShell：

```powershell
Get-FileHash -LiteralPath 'C:\Downloads\sotea-package.zip' -Algorithm SHA256
Expand-Archive -LiteralPath 'C:\Downloads\sotea-package.zip' -DestinationPath 'C:\Downloads\sotea-unpacked'
```

上述文件名和路径是占位示例。进入实际解压目录后，查看 BUILD-INFO.json 中的版本、平台及程序摘要；候选包的 CONTENTS.sha256 还记录包内文件摘要。校验值用于核对字节，发布者身份还取决于获取渠道和签名。

当前候选尚无 Developer ID／公证或 Authenticode 签名：macOS arm64 只有 Go 链接器 ad-hoc 签名，Intel Mac 和 Windows 文件未签名。系统或组织策略可能阻止执行，应按相应策略处理，不以关闭系统保护作为安装步骤。

## macOS 安装

在含有 `sotea` 文件的解压目录，用 Terminal 或 iTerm2 执行：

```sh
./sotea version
mkdir -p "$HOME/.local/bin"
install -m 755 ./sotea "$HOME/.local/bin/sotea"
export PATH="$HOME/.local/bin:$PATH"
sotea version
```

这是首次安装流程；已有安装时使用下方升级步骤。需要长期生效时，将 `export PATH="$HOME/.local/bin:$PATH"` 加入自己的 `~/.zshrc`，重新打开终端。MCP 应配置安装后的固定绝对路径，避免使用临时解压目录。

## Windows 安装

在含有 `sotea.exe` 的解压目录打开 64 位 PowerShell：

```powershell
$ErrorActionPreference = 'Stop'
$installDirectory = Join-Path $env:LOCALAPPDATA 'Programs\sotea'
$installedBinary = Join-Path $installDirectory 'sotea.exe'
if (Test-Path -LiteralPath $installedBinary) {
    throw 'An installation already exists. Follow the upgrade instructions.'
}
New-Item -ItemType Directory -Path $installDirectory -Force | Out-Null
Copy-Item -LiteralPath '.\sotea.exe' -Destination $installedBinary
& $installedBinary version
if ($LASTEXITCODE -ne 0) {
    throw 'Unable to run sotea.'
}
$env:Path = $installDirectory + ';' + $env:Path
sotea version
```

将该目录加入 Windows 用户 Path 后，重新打开 PowerShell。`$env:Path` 只改变当前窗口。Windows PowerShell 5.1 为目标基线，Windows 11 另验证 PowerShell 7.6；实际验证状态见 [Windows 验证](windows-validation.md)。

## 首次运行

在项目目录执行 `sotea`，从 **Account** 绑定实例和账号，再使用 **Browse account** 或仓库工作台。命令行登录、私有 CA 和 SSH remote 见 [配置与认证](configuration.md)。

如果提示找不到程序，先确认 PATH 中目录与安装目录一致，或使用绝对路径运行。提示找不到 Git 时，安装符合基线的 Git 并重新打开终端；显式实例／仓库和 token 的远端 API 操作仍可使用。

## 升级与恢复

已安装本版后，可在 Home 选择 **Update → Install update**，或执行 `sotea update install`，确认后自动下载、校验并安装；脚本需显式 `--yes`。成功后重启 sotea 和已运行的 MCP 进程。包管理器安装、五小时检查、版本通道和备份说明见 [检查与安装更新](updates.md)。下面保留手动升级与恢复步骤。

先退出菜单，在 AI 客户端停止 sotea MCP，核验新包后再替换程序。配置和系统凭证保存在程序目录之外；更换程序路径后须更新 MCP 接入配置。

macOS，在新包解压目录执行下面的独立 shell 段。任一步失败都会停止，原程序只在新程序验证通过后替换：

```sh
(
    set -e
    install -m 755 ./sotea "$HOME/.local/bin/sotea.new"
    "$HOME/.local/bin/sotea.new" version
    cp -p "$HOME/.local/bin/sotea" "$HOME/.local/bin/sotea.previous"
    mv -f "$HOME/.local/bin/sotea.new" "$HOME/.local/bin/sotea"
    "$HOME/.local/bin/sotea" version
)
```

需要恢复时，将 `sotea.previous` 复制为 `sotea.new`，验证 version 后执行同目录 mv。不要先删除当前程序。

Windows，在新包解压目录执行：

```powershell
$ErrorActionPreference = 'Stop'
$installDirectory = Join-Path $env:LOCALAPPDATA 'Programs\sotea'
$installedBinary = Join-Path $installDirectory 'sotea.exe'
$stagedBinary = Join-Path $installDirectory 'sotea.new.exe'
$backupBinary = Join-Path $installDirectory ('sotea.previous-' + [Guid]::NewGuid().ToString('N') + '.exe')
Copy-Item -LiteralPath '.\sotea.exe' -Destination $stagedBinary -Force
& $stagedBinary version
if ($LASTEXITCODE -ne 0) {
    throw 'Candidate validation failed; keep the existing program.'
}
[IO.File]::Replace($stagedBinary, $installedBinary, $backupBinary)
& $installedBinary version
if ($LASTEXITCODE -ne 0) {
    throw 'Updated program failed; restore the retained backup.'
}
```

目标卷需支持 File.Replace。遇到文件被占用或替换失败，先确认 MCP 进程已停止，再检查文件和备份；不改为先删后写。恢复时把备份复制为 staging 文件，验证后再替换。Windows 文件锁与恢复步骤仍待对应实机验收。

## 卸载

1. 在 **MCP integrations** 中移除各客户端的自有 sotea 接入项，并在客户端停止对应服务。
2. 若要清理保存的账号，逐个执行 `sotea --instance team --account username auth logout`。这会清理 sotea 自有引用和凭证，不会撤销 Gitea 服务端 token。
3. 删除安装的程序、自己保留的候选和程序备份，从用户 PATH 移除对应目录；sh 安装器追加的 `# sotea user installation` 及下一行可从对应 shell 配置中移除。
4. 配置默认保留；决定彻底清理时，先完成账号退出，再删除自己的 sotea 配置目录。

共享 Git helper 凭证默认保留。只有明确需要时使用 `auth logout --forget-git` 并额外确认；环境 token 需要在宿主环境中移除并重启相关进程。配置位置和凭证范围见 [配置与认证](configuration.md)。

[返回文档目录](README.md)
