## 版本状态 / Release status

**{{RELEASE_TAG}} 是基于 v0.1.0-rc.2 的小型修复预发布版，不是正式稳定版。** 本次修复 Windows 一键安装命令的 BOM 解析问题；保留 rc.1／rc.2 的标签、附件和历史说明。

**{{RELEASE_TAG}} is a small bug-fix prerelease based on v0.1.0-rc.2, not a stable release.** It fixes BOM parsing in the Windows one-command installer. The rc.1 and rc.2 tags, assets, and release notes remain unchanged.

## 修复内容 / Fixes

- 修复 `irm` 下载的脚本文本包含前导 UTF-8 BOM 时，`ScriptBlock.Create` 在 `[CmdletBinding()]`／`param` 处解析失败的问题。一键命令现在先执行 `.TrimStart([char]0xFEFF)`，再创建脚本块并传入版本参数。
- 同步 README、安装指南和本次 Release 的 Windows 命令；安装脚本文件保留 BOM，继续兼容 Windows PowerShell 5.1 的直接文件执行方式，无需更改执行策略。
- 为实际文档命令补充带 BOM／无 BOM 的解析、参数绑定和已有文件保护检查；Release 安装回归增加完整的下载文本执行路径。

- Fix parsing failures at `[CmdletBinding()]` / `param` when script text downloaded by `irm` contains a leading UTF-8 BOM. The command now applies `.TrimStart([char]0xFEFF)` before creating the script block and passing the version argument.
- Update the Windows commands in the README, installation guide, and this release. The script file retains its BOM for direct execution in Windows PowerShell 5.1; changing execution policy is unnecessary.
- Add checks of the actual documented commands with and without a BOM, including parameter binding and existing-file protection. Release regression checks now also cover installation through downloaded script text.

rc.2 的 Codex MCP 配置父表修复继续包含在本版本中；本次不改变 CLI、TUI 或 MCP 的业务行为。

This release retains the Codex MCP parent-table configuration fix from rc.2. CLI, TUI, and MCP business behavior is unchanged.

## 验证范围 / Validation scope

公开 rc.2 附件已复现原始解析错误；修正后的三处文档命令在本机 PowerShell 7 的带 BOM／无 BOM 六个场景中通过。CI 分别用 Windows PowerShell 5.1 和 PowerShell 执行这些真实命令。Release 工作流在三平台原生测试、最终包检查及两种 Windows shell 的完整安装回归通过后才公开附件。所有安装回归使用隔离目录与受控下载，不修改开发者真实安装、客户端配置或用户 PATH。

The original parsing failure was reproduced using the public rc.2 asset. The corrected commands in all three documents passed six local PowerShell 7 scenarios with and without a BOM. CI executes those actual commands in Windows PowerShell 5.1 and PowerShell. The Release workflow publishes assets only after native tests on all three targets, final-archive checks, and full installer regression checks in both Windows shells pass. Installer tests use isolated directories and controlled downloads without changing the developer's installation, client settings, or user PATH.

未修改 Gitea API 路径、认证或请求／响应契约，本次不重复七版本业务矩阵；已有兼容证据与下方真实平台／客户端待验收项分别保留。

Gitea API paths, authentication, and request/response contracts are unchanged, so this patch does not repeat the seven-version workflow matrix. Existing compatibility evidence and the pending platform/client checks below remain separate.

## 安装 / Installation

从本版本的 Assets 下载对应平台的压缩包和 `SHA256SUMS`，或使用同一版本的 `install.sh`／`install.ps1`。支持 macOS arm64、macOS amd64、Windows amd64；运行无需 Go 或 Node.js。

Download the archive for your platform and `SHA256SUMS` from this release's Assets, or use the accompanying `install.sh` / `install.ps1`. Packages are provided for macOS arm64, macOS amd64, and Windows amd64. Go and Node.js are not required at runtime.

安装脚本默认选择最新稳定版；安装 RC 时必须显式传入本版本标签。也可先下载并检查脚本，再执行；步骤、PATH 及自定义目录见[安装指南](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/installation.md)。

Installers select the latest stable release by default. Pass this release's exact tag to install an RC. You can also download and review the scripts before running them; see the [installation guide](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/installation.md) for PATH setup and custom directories.

### 一键首次安装 / One-command first installation

以下命令直接执行远程脚本，安装本次发布的 `{{RELEASE_TAG}}`，校验并解压程序，尝试配置用户 PATH；PATH 配置失败或使用自定义目录时可能需要手动设置。macOS 安装后重新打开终端。

These commands execute remote scripts to install this exact release, `{{RELEASE_TAG}}`. They verify the archive, extract the executable, and attempt to configure your user PATH; manual configuration may be required if that step fails or a custom directory is used. Open a new terminal after installation on macOS.

macOS · Terminal / iTerm2:

```sh
curl -fsSL https://github.com/Spring-Ocean/gitea-cli-so/releases/download/{{RELEASE_TAG}}/install.sh | sh -s -- --version {{RELEASE_TAG}}
```

Windows · PowerShell 5.1 / PowerShell:

```powershell
& ([scriptblock]::Create((irm -ErrorAction Stop https://github.com/Spring-Ocean/gitea-cli-so/releases/download/{{RELEASE_TAG}}/install.ps1).TrimStart([char]0xFEFF))) -Version {{RELEASE_TAG}}
```

## 升级 / Upgrade

已有安装执行 `sotea update check` 和 `sotea update install`，或在 Home 选择 Update。首次安装脚本拒绝覆盖已有程序。升级后重启 sotea 和客户端已运行的 MCP 进程。

For an existing installation, run `sotea update check` and `sotea update install`, or choose Update in Home. First-install scripts refuse to overwrite an existing executable. Restart sotea and any running MCP subprocesses after upgrading.

## 目标平台与待验收项 / Target platforms and pending validation

| 目标 / Target | 当前状态 / Current status |
| --- | --- |
| macOS 15+ arm64 | macOS 27 arm64 的 CLI、系统凭证、PTY 菜单、MCP 及安装升级已有验证；macOS 15 和目标 GUI 终端仍待实测。<br>CLI, system credentials, PTY menus, MCP, installation, and upgrades have been tested on macOS 27 arm64. macOS 15 and target GUI terminals remain to be tested. |
| macOS 15+ amd64 | 已构建并核查归档；原生 Intel Mac 和最低系统版本仍待实测。<br>Archives have been built and inspected; native Intel Macs and the minimum OS version remain to be tested. |
| Windows 10 22H2 / Windows 11 amd64 | 目标程序已构建并核查归档；两个系统的真实运行、安装升级、终端及 Git for Windows／GCM 组合仍待实测。<br>Target binaries and archives have been built and inspected. Native operation, installation and upgrade, terminals, and Git for Windows/GCM combinations remain to be tested on both systems. |

此前候选在七个隔离 Gitea 版本（`28.3.0`、`1.27.3`、`1.26.4`、`1.25.5`、`1.24.7`、`1.23.8`、`1.22.6`）通过了代表性的 CLI 和 MCP 业务回归；这不表示所有端点、定制实例或平台组合均已验证。

An earlier candidate passed representative CLI and MCP workflow regression checks against seven isolated Gitea versions: `28.3.0`, `1.27.3`, `1.26.4`, `1.25.5`, `1.24.7`, `1.23.8`, and `1.22.6`. This does not establish coverage of every endpoint, customized instance, or platform combination.

Codex 和 Claude Code 只有部分真实客户端流程已验证；Cursor、VS Code 的真实工具调用，以及四种客户端完整的批准／取消 UI 仍待验收。rc.1／rc.2 的公开下载、macOS arm64 首次安装及 rc.1 → rc.2 自动升级已有实测；本次产物及 rc.2 → rc.3 自动升级需绑定新版本独立复核。Windows Server 或 macOS 托管 runner 的结果不能代替目标桌面系统与终端实测。详见[兼容性与验证状态](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/compatibility.md)。

Only parts of the real Codex and Claude Code client flows have been validated. Real tool calls in Cursor and VS Code, and the complete approval/cancellation UI in all four clients, remain pending. Public downloads, first installation on macOS arm64, and the rc.1 to rc.2 automatic upgrade were tested for previous releases; these new artifacts and the rc.2 to rc.3 automatic upgrade require separate verification. Results on hosted Windows Server or macOS runners do not replace testing on the target desktop systems and terminals. See [compatibility and validation status](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/compatibility.md).

## 已知限制 / Known limitations

- 当前候选未提供 Developer ID 签名、公证或 Authenticode 签名，系统或组织策略可能阻止运行。`SHA256SUMS` 用于核对文件一致性，不能证明发布者身份。
- MCP 写入计划属于当前服务进程；重启后不能保证跨进程幂等。写入结果未知时应先核查远端，避免重复提交。
- 自动更新不会提权；安装目录不可写时需手动处理。升级后须重启 sotea 及已运行的 MCP 进程。详见[更新说明](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/updates.md)。
- 当前不承诺 Linux、Windows ARM64 或 32 位系统；运行预编译程序无需 Go 或 Node.js。

- This candidate does not provide Developer ID signing, notarization, or Authenticode signing; system or organization policies may block execution. `SHA256SUMS` checks file integrity and does not authenticate the publisher.
- MCP write plans belong to the current server process; idempotency is not guaranteed across restarts. If a write outcome is unknown, inspect the remote state before submitting again.
- Automatic updates do not elevate privileges; an unwritable installation directory requires manual action. Restart sotea and running MCP processes after upgrading. See the [update guide](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/updates.md).
- Linux, Windows ARM64, and 32-bit systems are not in the current support scope. Go and Node.js are not required to run prebuilt binaries.
