# sotea 运行环境与最低版本调研

> 调研日期：2026-11-06
>
> 状态：Windows 10 范围、Git 2.43 功能基线已确定；macOS 最低版本及目标系统验收仍待完成
>
> 对应：[requirements.md](requirements.md) 第 8 章、[technical-design.md](technical-design.md) 第 11–12 章

## 1. 建议结论

Windows 已确认向下正式支持 Windows 10，最低基线采用 Windows 10 22H2 amd64，同时覆盖 Windows 11；其余项目仍建议使用以下基线。产品支持基线是团队承诺验证和处理兼容问题的范围；Go 技术下限仅说明工具链允许在哪些系统运行，不能替代产品验收。用户运行预编译程序无需安装 Go。

| 项目 | 建议最低支持／验证基线 | 推荐使用环境 | 首版处理 |
| --- | --- | --- | --- |
| macOS | macOS 15 Sequoia | 已验证且仍获更新的系统版本 | arm64、amd64 分别验证并发布；较新的 macOS 版本需另记实测结果。 |
| Windows | 已确定：Windows 10 22H2 amd64，同时覆盖 Windows 11 | Windows 11 25H2 作为较新系统的验收基线 | Windows 10 与 Windows 11 均为正式验收目标，使用同一 amd64 产物；ARM64 待有设备／CI 验证后承诺。 |
| Git | 已确定：Git 2.43，含对应厂商修补版本 | 仍获上游或系统厂商维护的较新稳定版 | P3 用官方 2.43.7 隔离样本验证 core／helper；Windows 发行组合另待实测，不要求安装 2.43.0。 |
| Windows shell | Windows PowerShell 5.1 | Windows 11 上推荐 PowerShell 7.6 LTS | 两个系统均验证 5.1，Windows 11 另测 7.6；sotea 是独立 exe，不以安装 PowerShell 7 为前提。 |
| Windows 终端宿主 | 兼容目标系统的 Windows Terminal 稳定版 | Windows Terminal；Windows 11 可搭配 PowerShell 7.6 LTS | 两个系统分别记录宿主版本，另测传统控制台的纯文本降级；不因 shell 版本推断 ANSI 能力。 |
| macOS 终端宿主 | 目标系统自带 Terminal；兼容目标系统的 iTerm2 稳定版 | 用户已有 Terminal 或 iTerm2 | 不设缺乏依据的终端小版本下限，记录实际测试版本。 |
| 凭证管理 | macOS Keychain／Windows Credential Manager；Git helper 可选 | macOS 常见 osxkeychain；Windows 常见 Git for Windows 配套 GCM | Git Credential Manager 不作为所有用户的强制安装项；SSH 不自动提供 API 身份。 |

macOS 13／14 可列为按需验证的兼容环境，不纳入上述默认正式承诺；Windows 10 22H2 则必须通过正式验收，不能降为可选测试。仍在支持期内的 Windows 11 24H2 Enterprise／Education 可以单独增加测试行，不能把 Home／Pro 的生命周期套用到所有版本类型。Windows 10 更早的功能版本及 LTSC 等单独发行分支不自动纳入承诺，需要时另加验证。Linux、32 位系统暂不纳入；以上均不调整七个 Gitea 服务端版本的必需支持范围。

## 2. Go 技术下限与系统生命周期

### 2.1 Go 1.27.3

Go 1.27 发布说明要求 macOS 13 Ventura 或更新系统。Go 官方最低要求表列出的 Windows 下限为 Windows 10；Windows ARM64 还需要与其架构相符的系统及依赖条件，不能将 amd64 的结果直接外推。最终二进制的运行范围也受凭证适配器、终端库和构建选项约束。

因此，macOS 13 是当前工具链的技术候选下限；macOS 12 及更旧版本不适合在固定 Go 1.27.3 的前提下承诺支持。Windows 10 具备工具链基础，不等于本项目应对所有 Windows 10 版本作正式承诺。

依据：[Go 1.27 release notes](https://go.dev/doc/go1.27)、[Go MinimumRequirements](https://go.dev/wiki/MinimumRequirements)。

### 2.2 为什么建议 macOS 15

Apple 的安全发布记录在调研时仍列有 macOS 15 的更新。以 15 起步能覆盖 Apple Silicon 和 Intel 的测试目标，同时减少对更旧系统及其凭证、终端环境的维护负担。这是工程取舍，不是某个现有 CLI 功能强制要求 macOS 15。

Apple 的发布记录不能解读为对所有系统作固定三年安全维护承诺；本文不为 macOS 13／14 编造统一停止支持日期。如果团队设备仍依赖这些版本，可以在 Go 下限允许范围内增加实际测试，再决定是否提升为正式支持。Intel Mac 能升级到哪个系统须按设备判断，不要求所有旧设备升级到当前最新 macOS。

依据：[Apple security releases](https://support.apple.com/en-us/100100)。

### 2.3 Windows 10 向下支持与 Windows 11 验证

用户已确认需要向下支持 Windows 10，因此将 Windows 10 22H2 amd64 纳入正式范围，不要求用户升级到 Windows 11 才能使用。Windows 10 Home／Pro 常规支持已于 2025 年 10 月结束，ESU 及 LTSC 等另有规则；sotea 的应用兼容承诺与 Microsoft 的系统维护政策分别记录。Windows 11 24H2 Home／Pro 的支持于 2026 年 10 月结束，早于本次调研日期；25H2 在调研时仍处于支持期，作为较新系统的验收基线。

Windows 10 和 Windows 11 复用同一 windows/amd64 压缩包，不单独分发功能缩减版；CLI、Git 集成、系统凭证、交互菜单及 MCP 均须在 Windows 10 验证。依赖升级时检查最低系统要求；不因新依赖只支持 Windows 11 就静默抬高既定下限。

Windows 11 Enterprise／Education 的维护周期不同。若团队仍使用处于支持期的 24H2 企业设备，可以保留兼容测试目标，无需仅因版本名低于 25H2 就阻止运行。Windows Server 不属于当前需求，不增加其支持承诺。

依据：[Windows 10 Home and Pro lifecycle](https://learn.microsoft.com/en-us/lifecycle/products/windows-10-home-and-pro)、[Windows 11 Home and Pro lifecycle](https://learn.microsoft.com/en-us/lifecycle/products/windows-11-home-and-pro)、[Windows 11 Enterprise and Education lifecycle](https://learn.microsoft.com/en-us/lifecycle/products/windows-11-enterprise-and-education)。

## 3. Git 下限与凭证管理

现有 TDD 需要系统 Git 执行仓库发现、remote／分支查询、worktree 支持、credential fill／approve／reject 和命令透传。P3 将 2.43 固定为首版集成功能的支持基线，以限定回归范围；这不是相关命令的唯一技术下限。官方 Git 2.43.7 源码的临时隔离构建已验证仓库／worktree、helper 和 CLI；该样本关闭 curl／expat／gettext／tcltk／OpenSSL，不是供用户安装的完整 HTTP／SSH Git 分发包，也不替代 Windows Git for Windows／GCM 组合的验收。

实现时应按该下限选择 Git 参数和输出格式，不未经检测就使用较新版本才增加的选项；对厂商版本字符串保留原值，再解析数字版本。遇到 Apple Git 等厂商发行版时，数字版本用于功能判断，安全维护情况依据厂商补丁记录，不能仅由上游版本号推断。若团队预装 Apple Git 低于 2.43，应先采集版本并实际验证，再决定扩展下限或在安装说明中引导安装较新 Git。

Git 缺失或低于该基线时：纯 API、显式 token 登录和不依赖本地仓库的 MCP 工具仍可用；依赖本地 Git 的能力给出安装／升级提示。显式 sotea git 透传仍可交给现有 Git 执行并保留其输出和退出码，但不把未经验证的旧版本标记为正式兼容。

Git helper 由 Git 自身按上下文调用。macOS 可以使用用户既有 osxkeychain，Windows 可以使用 Git for Windows 配套的 Git Credential Manager；不直接扫描用户的系统密钥，也不因使用某个 helper 而跳过 Gitea API 身份验证。GCM 是可选组件，其版本和操作系统要求应按实际安装组合验证，不能拿 GCM 的要求代替所有 Git 认证方式的要求。

依据：[Git 2.43 credential](https://git-scm.com/docs/git-credential/2.43.0)、[Git credential contexts](https://git-scm.com/docs/gitcredentials)、[Git rev-parse](https://git-scm.com/docs/git-rev-parse)、[Git worktree](https://git-scm.com/docs/git-worktree)、[GCM installation](https://github.com/git-ecosystem/git-credential-manager/blob/main/docs/install.md)。

## 4. PowerShell 与终端表现

PowerShell 是 shell，Windows Terminal／传统控制台是终端宿主，两者分开测试。Microsoft 的 PowerShell 生命周期表列有 7.6 LTS，其支持期到 2028 年 11 月；7.4 LTS 的支持于 2026-11-10 结束，距调研日期仅四天，不适合作为新项目的推荐安装版本。Windows PowerShell 5.1 随 Windows 的支持政策维护，仍应作为目标 Windows 系统中的兼容路径验证。

Windows Terminal 自身的安装技术下限是 Windows 10 2004／build 19041；这不扩大本项目的 Windows 正式支持范围。iTerm2 下载页所列稳定版 3.7.7 要求 macOS 13 或更新，低于上述 macOS 15 提案；终端后续更新可能改变要求，发布验收时须重新核对。

输出按终端能力处理：支持时显示颜色、加粗及表格；无 ANSI 能力时输出可读纯文本，不输出原始控制序列。颜色、强调和选中状态不能只靠颜色区分；中文宽度、窄窗口、取消恢复和跨页选择按 TDD 测试。NO_COLOR、颜色参数、管道、JSON、Git 原始输出和 MCP stdout 继续遵循既有输出契约。

依据：[PowerShell support lifecycle](https://learn.microsoft.com/en-us/powershell/scripting/install/powershell-support-lifecycle)、[Windows Terminal repository](https://github.com/microsoft/terminal)、[iTerm2 downloads](https://iterm2.com/downloads.html)。

## 5. 建议验收矩阵

| 验证组 | 必测组合 | 关键内容 |
| --- | --- | --- |
| macOS 基线 | macOS 15 arm64、amd64，各自 Terminal | 程序启动、Keychain、Git、API、菜单、中文与 MCP。 |
| macOS 其他系统／宿主 | 纳入发布支持表的较新系统及其可用架构；iTerm2 稳定版 | 记录每个实际组合，不把某架构或版本结果外推；确认输入和样式。 |
| Windows 10 基线 | Windows 10 22H2 amd64，Windows Terminal + Windows PowerShell 5.1 | 正式必测：.exe 启动、中文／空格路径、系统凭证、Git/helper、API、交互菜单及 MCP。 |
| Windows 11 基线 | Windows 11 25H2 amd64，Windows Terminal + Windows PowerShell 5.1 | 使用与 Windows 10 相同发布产物，独立验证关键流程，不用其中一个系统替代另一个。 |
| Windows 推荐组合 | 相同系统，Windows Terminal + PowerShell 7.6 LTS | 中文、重定向、参数转义、Ctrl+C、交互选择及 MCP 子进程。 |
| Windows 降级输出 | Windows 10 22H2 与 Windows 11 25H2，传统控制台 + Windows PowerShell 5.1 | 两个系统均验证纯文本可读、无转义码泄漏；交互能力不足时不阻塞并给出换宿主提示。 |
| Git 基线 | 固定 Git 2.43 测试样本；各平台正常使用的厂商修补版本／较新稳定版 | 版本解析、子目录、worktree、多 remote、隔离 helper、输出与退出码；记录精确版本。 |
| 无 Git | 显式实例与 token | 纯 API 可用，本地 Git 功能明确提示，不误报认证失败。 |
| 可选兼容 | macOS 13／14、Windows 11 企业版旧分支、Windows ARM64 | 具备实际需求和验证条件后添加，不以交叉编译替代运行；Windows 10 22H2 不属于可选项。 |

最低版本样本只在隔离测试仓库和模拟 helper 中验证，不使用开发者真实凭证。正式发布记录 OS 版本与构建号、CPU 架构、Git 原始版本、helper、shell、终端和关键依赖版本，以及通过／失败／未测试状态。

实现前核对凭证库和终端库的最低要求，保留已确定的 Windows 10 支持范围；发布前按团队实际设备确认其余提案并完成测试。初次调研仅更新文档与验收要求，未安装旧系统或运行该矩阵；后续 P2／P3 的实际实现和验证结果见下文，不能外推到矩阵中未具备的环境。


## P2 实施记录

P2 使用 go-keyring v0.2.8（Windows 分支依赖 wincred v1.2.3）、官方 MCP Go SDK v1.8.0、x/term v0.47.0 与 x/sys v0.49.0。Go 1.27.3 下的 darwin/arm64、darwin/amd64、windows/amd64 均完成开发构建；macOS 27 arm64 的 Keychain、认证只读链路、stdio 与 PTY 取消通过。

Windows 10 22H2／Windows 11、Intel macOS、候选最低 macOS 仍未实测；Git 的 P3 基线结果另见下文。上述依赖的系统 API 与构建检查不能替代对应运行记录。系统存储库的同步 OS 解锁交互受系统控制，不能宣称所有提示可强行取消。完整记录见 [p2-readonly.md](p2-readonly.md)，正式最低平台承诺与原验收矩阵保持待验证。

## P3 实施记录

Git 2.43 功能基线已定案，2.43.7 隔离样本和本机 Apple Git 的验证各自归档。交互选择器锁定 Bubble Tea v1.3.10、go-runewidth v0.0.16，Lip Gloss v1.1.0 目前为传递依赖。上述三个模块的根 LICENSE 均为 MIT；正式发布仍须保留各依赖许可和版权声明，完整输出样式按 P5 实施。POSIX PTY 的真实键盘／取消／恢复通过，不能替代 GUI Terminal／iTerm2 或 Windows 宿主实测。详见 [P3 记录](p3-issue.md)。

## P5 实施记录

Lip Gloss v1.1.0、termenv v0.16.0、uniseg v0.4.7 从已有传递依赖转为直接使用，无版本升级。macOS 27 arm64 使用真实 POSIX PTY 验证完整菜单、静态降级、窄屏字素、取消恢复和 Git 交接；三目标构建记录见 [P5 证据](validation/p5-result.json)。Windows 10／11、Intel／最低 macOS、Terminal／iTerm2 GUI 仍待实测；此前 GUI 工具明确拒绝访问 Terminal，未绕过该限制。支持矩阵不删减，TERM=dumb 的 POSIX 验证不能替代传统 Windows 控制台。

P5分组分页与账户浏览增量使用同一最终产物完成七版本真实菜单和POSIX PTY验证，语义色、分级按键、整组分页、静态降级及上下文隔离结果见 [本轮证据](validation/p5-account-browser-result.json)。Windows／GUI宿主的待实测状态保持不变。

P5 全局排版／Markdown／账号向导增量新增直接依赖 goldmark v1.8.7（MIT，Copyright (c) 2019 Yusuke Inuzuka，模块最低 Go 1.22）；已有 x/ansi v0.10.1 转为直接使用，无版本升级。goldmark 仅解析本地正文，无外部进程／网络和系统 API 依赖。构建仍用 Go 1.27.3，正式发布需附相应许可与版权声明；新增依赖不替代既定系统／GUI 验收。
