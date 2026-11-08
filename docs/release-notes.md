## 版本状态 / Release status

**{{RELEASE_TAG}} 是 sotea 的首个发布候选版本，不是正式稳定版。** 本说明记录已有验证范围；新发布的文件必须单独核验，历史候选结果不会自动转移到新产物。

**{{RELEASE_TAG}} is the first sotea release candidate, not a stable release.** The validation scope describes existing evidence; newly released artifacts require separate verification, and results from earlier candidates do not automatically apply.

## 主要功能 / Highlights

- 通过 CLI 和交互工作台管理 Gitea 仓库、Issue、Pull Request、Release 和附件；支持 Git 透传、原始 API 和 JSON 输出。
- 提供本地 stdio MCP 服务及 Codex、Claude Code、Cursor、VS Code 配置向导；MCP 默认只读，写入需要单独授权和确认。
- Home 可后台检查 GitHub Releases；也可使用 `sotea update check` 和 `sotea update install` 检查与安装更新。

- Manage Gitea repositories, issues, pull requests, releases, and assets through the CLI or interactive workspace; Git passthrough, raw API access, and JSON output are available.
- Use a local stdio MCP server and setup wizards for Codex, Claude Code, Cursor, and VS Code. MCP is read-only by default; writes require separate authorization and confirmation.
- Home can check GitHub Releases in the background. Use `sotea update check` and `sotea update install` to check for and install updates.

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
& ([scriptblock]::Create((irm -ErrorAction Stop https://github.com/Spring-Ocean/gitea-cli-so/releases/download/{{RELEASE_TAG}}/install.ps1))) -Version {{RELEASE_TAG}}
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

Codex 和 Claude Code 只有部分真实客户端流程已验证；Cursor、VS Code 的真实工具调用，以及四种客户端完整的批准／取消 UI 仍待验收。首个公开 Release 的下载、首次安装和更新也需要实际验证。Windows Server 或 macOS 托管 runner 的结果不能代替目标桌面系统与终端实测。详见[兼容性与验证状态](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/compatibility.md)。

Only parts of the real Codex and Claude Code client flows have been validated. Real tool calls in Cursor and VS Code, and the complete approval/cancellation UI in all four clients, remain pending. Downloads, first installation, and updates from the first public Release also require verification. Results on hosted Windows Server or macOS runners do not replace testing on the target desktop systems and terminals. See [compatibility and validation status](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/compatibility.md).

## 已知限制 / Known limitations

- 当前候选未提供 Developer ID 签名、公证或 Authenticode 签名，系统或组织策略可能阻止运行。`SHA256SUMS` 用于核对文件一致性，不能证明发布者身份。
- MCP 写入计划属于当前服务进程；重启后不能保证跨进程幂等。写入结果未知时应先核查远端，避免重复提交。
- 自动更新不会提权；安装目录不可写时需手动处理。升级后须重启 sotea 及已运行的 MCP 进程。详见[更新说明](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/updates.md)。
- 当前不承诺 Linux、Windows ARM64 或 32 位系统；运行预编译程序无需 Go 或 Node.js。

- This candidate does not provide Developer ID signing, notarization, or Authenticode signing; system or organization policies may block execution. `SHA256SUMS` checks file integrity and does not authenticate the publisher.
- MCP write plans belong to the current server process; idempotency is not guaranteed across restarts. If a write outcome is unknown, inspect the remote state before submitting again.
- Automatic updates do not elevate privileges; an unwritable installation directory requires manual action. Restart sotea and running MCP processes after upgrading. See the [update guide](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/updates.md).
- Linux, Windows ARM64, and 32-bit systems are not in the current support scope. Go and Node.js are not required to run prebuilt binaries.
