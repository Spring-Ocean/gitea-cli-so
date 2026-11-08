# 安装、升级与卸载

sotea 的一个可执行文件同时提供 CLI、交互菜单和本地 MCP 服务。运行预编译程序无需安装 Go；本地仓库发现、Git 凭证复用和 Git 命令需要系统 Git。

## 选择平台

| 文件平台标识 | 适用设备 | 压缩格式 |
| --- | --- | --- |
| darwin-arm64 | Apple Silicon Mac | tar.gz |
| darwin-amd64 | Intel Mac | tar.gz |
| windows-amd64 | Windows 10 22H2／Windows 11，Intel 或 AMD 64 位 | zip |

支持范围与实际验证状态见 [兼容性说明](compatibility.md)。目前尚未正式发布，下载渠道和签名策略仍待落实；内部验收包由维护者提供。包名包含具体版本、平台，候选包还可能包含打包修订号，请以实际文件为准。

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
3. 删除安装的程序、自己保留的候选和程序备份，从用户 PATH 移除对应目录。
4. 配置默认保留；决定彻底清理时，先完成账号退出，再删除自己的 sotea 配置目录。

共享 Git helper 凭证默认保留。只有明确需要时使用 `auth logout --forget-git` 并额外确认；环境 token 需要在宿主环境中移除并重启相关进程。配置位置和凭证范围见 [配置与认证](configuration.md)。

[返回文档目录](README.md)
