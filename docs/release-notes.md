## 版本状态 / Release status

**{{RELEASE_TAG}} 是基于 v0.1.0-rc.3 的小型修复预发布版，不是正式稳定版。** 本次修复 HTTPS 写入返回结果未知、Windows 一键安装文本解码，并补充模型可直接发现的 MCP 参数指引。

**{{RELEASE_TAG}} is a small bug-fix prerelease based on v0.1.0-rc.3, not a stable release.** It fixes unknown outcomes on HTTPS writes and Windows installer text decoding, and adds discoverable MCP argument guidance.

## 修复内容 / Fixes

- **HTTPS 写入**：修复写连接禁用 HTTP/2 后仍在 TLS ALPN 中宣告 h2 的协议错配。写连接现在只协商 HTTP/1.1，解决支持 HTTP/2 的实例可能出现读取／prepare 成功而 Release 等 commit 返回 RESULT_UNKNOWN 的问题；继续禁止自动重放写入。
- **Windows 首次安装**：从 Invoke-WebRequest.RawContentStream 获取原始字节，显式 UTF-8 解码后移除 BOM。rc.3 的 TrimStart 无法修复已经被 HTTP 字符集误解码的 BOM；脚本文件继续保留 BOM 和 #Requires，兼容 PowerShell 5.1 文件执行，不更改执行策略。
- **MCP 参数指引**：初始化 instructions、工具／字段描述和完整 examples 说明 tag／target 与 Gitea tag_name／target_commitish 的区别，以及创建／编辑、单个／批量编号和未知结果恢复；严格拒绝错误字段，不添加隐式别名。
- 保留 rc.2 的 Codex MCP 配置父表修复。此前公开标签、附件和说明保持原样。

- **HTTPS writes**: Fix a TLS ALPN mismatch that advertised h2 after disabling HTTP/2 on write connections. Writes now negotiate HTTP/1.1 only, fixing cases where reads/preparation succeeded but Release and other commits returned RESULT_UNKNOWN on HTTP/2-enabled instances. Automatic write replay remains disabled.
- **First installation on Windows**: Read raw bytes from Invoke-WebRequest.RawContentStream, explicitly decode UTF-8, then remove the BOM. The rc.3 TrimStart command could not repair an already misdecoded BOM. Script files retain their BOM and #Requires for PowerShell 5.1 file execution, without changing execution policy.
- **MCP argument guidance**: Initialization instructions, tool/property descriptions, and complete examples clarify tag/target versus Gitea tag_name/target_commitish, create/edit shapes, single/batch numbers, and unknown-outcome recovery. Invalid fields remain rejected without implicit aliases.
- Retain the Codex MCP parent-table configuration fix from rc.2. Earlier public tags, assets, and notes remain unchanged.

## 验证范围 / Validation scope

原 UTF-8 字符串替身不能证明真实 HTTP 下载解码正确，本轮已替换成 loopback HTTP：覆盖四种 Content-Type、BOM／无 BOM、三个实际文档命令和 HTTP 失败。Windows PowerShell 5.1／PowerShell 的原生结果须在发布前单独核验，不以 macOS PowerShell 通过代替。

The previous UTF-8 string fixture did not validate real HTTP response decoding. It is replaced with loopback HTTP covering four Content-Type variants, BOM/no-BOM responses, the actual commands in three documents, and HTTP failure. Native Windows PowerShell 5.1 and PowerShell results must be verified before publication; macOS PowerShell checks are not substitutes.

MCP 回归通过真实 SDK 初始化／工具发现核对说明与示例，并验证错误 API 字段被拒绝、正确 sotea 字段进入应用层且不发送业务写。自然语言模型生成参数和四客户端完整 UI 仍需真实客户端验收。

MCP regression checks verify instructions and examples through actual SDK initialization/tool discovery, reject raw API aliases, and accept canonical sotea fields without business writes. Natural-language argument generation and complete UI flows remain subject to real-client validation.

HTTPS 协议回归覆盖 HTTP/2 读取后单次 HTTP/1.1 写入，以及中文预发布正文的 Release 准备、创建和读回，保护零重放。未修改 Gitea API 路径、认证、字段序列化或业务请求／响应契约，不重复七版本业务矩阵。

HTTPS regression checks cover an HTTP/2 read followed by a single HTTP/1.1 write, and Release preparation, creation, and read-back with Chinese prerelease notes. Writes remain non-replayed. Gitea API paths, authentication, serialization, and business request/response contracts are unchanged, so this patch does not repeat the seven-version workflow matrix.

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
