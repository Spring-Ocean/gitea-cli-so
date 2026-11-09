## 版本状态 / Release status

**{{RELEASE_TAG}} 是基于 v0.1.0-rc.4 的小型修复预发布版，不是正式稳定版。** 本次为更新器和首次安装脚本增加已有 GitHub 凭证复用，降低匿名 API 配额耗尽造成的更新失败概率。

**{{RELEASE_TAG}} is a small bug-fix prerelease based on v0.1.0-rc.4, not a stable release.** It reuses existing GitHub credentials in the updater and first-install scripts to reduce failures caused by exhausted anonymous API quotas.

## 修复内容 / Fixes

- **自动认证**：auto 按 SOTEA_GITHUB_TOKEN → GH_TOKEN → GITHUB_TOKEN → gh auth token --hostname github.com → Git HTTPS credential helper → 匿名的顺序查找。无需安装 gh 或 Git；查询在仓库外执行且禁止交互，有超时和输出限制。
- **认证边界**：仅固定仓库的 GitHub Releases API 携带认证，附件及跳转保持匿名；不读取 Gitea token，不保存、记录或通过命令参数传递 token。
- **匿名模式**：更新命令支持 --github-auth anonymous，macOS 安装脚本支持 --github-auth anonymous，Windows 支持 -GitHubAuth anonymous；环境变量 SOTEA_GITHUB_AUTH=anonymous 同时约束 Home、更新器和安装脚本。
- **回退与限流**：401 单次匿名回退并提示，不删除共享凭证；403／429 不自动重试或轮换凭证。更新器显示可用的限流恢复信息，Home 继续遵守五小时冷却。
- **首次安装**：显式版本无需查询 API 或读取凭证；未指定版本时，两种脚本查询最新稳定版。认证不能解决附件下载的 HTTP 504。
- 保留 rc.4 的 HTTPS 写入 ALPN、Windows 原始字节 UTF-8 解码及 MCP 参数指引修复；旧公开标签和附件保持不变。

- **Automatic authentication**: auto tries SOTEA_GITHUB_TOKEN → GH_TOKEN → GITHUB_TOKEN → gh auth token --hostname github.com → Git HTTPS credential helper → anonymous. Neither gh nor Git is required. Queries run outside repositories without interaction and with time/output limits.
- **Authentication boundaries**: Only the fixed repository's GitHub Releases API receives authentication. Assets and redirects remain anonymous. Gitea tokens are never read; tokens are not saved, logged, or placed in process arguments.
- **Anonymous mode**: Use --github-auth anonymous for updates and the macOS installer, or -GitHubAuth anonymous for the Windows installer. SOTEA_GITHUB_AUTH=anonymous also controls Home, updates, and installers.
- **Fallback and rate limits**: A 401 triggers one anonymous retry with a warning, without deleting shared credentials. A 403/429 does not trigger automatic retries or credential rotation. The updater reports available reset information; Home retains its five-hour cooldown.
- **First installation**: An explicit version skips API queries and credential lookup. Without a version, both installers query the latest stable release. Authentication does not resolve HTTP 504 errors when downloading assets.
- Retain the rc.4 HTTPS write ALPN, Windows raw-byte UTF-8 decoding, and MCP argument-guidance fixes. Earlier public tags and assets remain unchanged.

## 验证范围 / Validation scope

Go 全量测试、vet、更新器／CLI race、三目标构建，以及固定 API 认证、401 零额外重试、限流信息、凭证环境与 helper 作用域回归通过。sh 覆盖 18 个安装／认证场景；PowerShell 覆盖真实 HTTP 字符集／BOM 27 场景及凭证子进程、环境优先级、匿名和401回退。PS5.1／PowerShell 原生检查由 CI 与最终归档 Release 门禁执行；本机 PS7 结果不能代替 Windows 桌面验收。

Full Go tests, vet, updater/CLI race checks, three-target builds, and regressions for fixed API authentication, bounded 401 fallback, rate-limit diagnostics, credential environments and helper scopes pass. The sh tests cover 18 installation/authentication cases; PowerShell tests cover 27 real HTTP charset/BOM cases plus credential subprocesses, environment precedence, anonymous mode and 401 fallback. Native PS5.1/PowerShell checks are enforced by CI and final-archive release gates; local PS7 results do not replace Windows desktop acceptance.

测试使用合成凭证、隔离 helper 和本地服务，不读取真实开发者凭证。真实系统凭证／GCM 集成、目标桌面、GUI 终端与客户端完整 UI 仍待验收。未改 Gitea API，不重复七版本业务矩阵。

Tests use synthetic credentials, isolated helpers and local services, without reading real developer credentials. Real credential-store/GCM integration, target desktops, GUI terminals and full client UI remain pending. Gitea APIs are unchanged, so this patch does not repeat the seven-version workflow matrix.

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
& ([scriptblock]::Create([Text.Encoding]::UTF8.GetString((Invoke-WebRequest -UseBasicParsing -ErrorAction Stop -Uri 'https://github.com/Spring-Ocean/gitea-cli-so/releases/download/{{RELEASE_TAG}}/install.ps1').RawContentStream.ToArray()).TrimStart([char]0xFEFF))) -Version {{RELEASE_TAG}}
```

## 升级 / Upgrade

旧 rc.4 程序升级时仍使用旧匿名策略；凭证复用在 rc.5 安装并重启后生效。若旧版遇限流，请等待配额恢复或手动替换已校验的程序包。

An rc.4 binary still uses its previous anonymous strategy when upgrading. Credential reuse takes effect after installing rc.5 and restarting. If the old version is rate limited, wait for the quota reset or manually replace it with a verified archive.

已返回 unknown 的旧计划不会因升级而自动恢复或重试。重启 MCP 后先只读核对远端 Release／标签；确认目标状态并获得授权后，使用新的 request_id 准备写入。

Upgrading does not restore or retry an old unknown plan. After restarting MCP, inspect the remote release/tag with read-only tools; prepare a new request_id only after confirming the state and obtaining authorization.

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

Codex 和 Claude Code 只有部分真实客户端流程已验证；Cursor、VS Code 的真实工具调用，以及四种客户端完整的批准／取消 UI 仍待验收。rc.1／rc.2 的公开下载、macOS arm64 首次安装及 rc.1 → rc.2 自动升级已有实测；下一候选产物的公开下载及从前一候选的自动升级仍需绑定实际版本独立复核。Windows Server 或 macOS 托管 runner 的结果不能代替目标桌面系统与终端实测。详见[兼容性与验证状态](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/compatibility.md)。

Only parts of the real Codex and Claude Code client flows have been validated. Real tool calls in Cursor and VS Code, and the complete approval/cancellation UI in all four clients, remain pending. Public downloads, first installation on macOS arm64, and the rc.1 to rc.2 automatic upgrade were tested for previous releases; the next candidate artifacts and automatic upgrades from the preceding candidate require separate verification. Results on hosted Windows Server or macOS runners do not replace testing on the target desktop systems and terminals. See [compatibility and validation status](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/compatibility.md).

## 已知限制 / Known limitations

- 当前候选未提供 Developer ID 签名、公证或 Authenticode 签名，系统或组织策略可能阻止运行。`SHA256SUMS` 用于核对文件一致性，不能证明发布者身份。
- MCP 写入计划属于当前服务进程；重启后不能保证跨进程幂等。写入结果未知时应先核查远端，避免重复提交。
- 自动更新不会提权；安装目录不可写时需手动处理。升级后须重启 sotea 及已运行的 MCP 进程。详见[更新说明](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/updates.md)。
- 当前不承诺 Linux、Windows ARM64 或 32 位系统；运行预编译程序无需 Go 或 Node.js。

- This candidate does not provide Developer ID signing, notarization, or Authenticode signing; system or organization policies may block execution. `SHA256SUMS` checks file integrity and does not authenticate the publisher.
- MCP write plans belong to the current server process; idempotency is not guaranteed across restarts. If a write outcome is unknown, inspect the remote state before submitting again.
- Automatic updates do not elevate privileges; an unwritable installation directory requires manual action. Restart sotea and running MCP processes after upgrading. See the [update guide](https://github.com/Spring-Ocean/gitea-cli-so/blob/{{RELEASE_TAG}}/docs/updates.md).
- Linux, Windows ARM64, and 32-bit systems are not in the current support scope. Go and Node.js are not required to run prebuilt binaries.
