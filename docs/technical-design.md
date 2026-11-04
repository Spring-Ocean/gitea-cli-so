# Gitea CLI 技术设计文档

> 状态：技术方案讨论稿
>
> 更新日期：2026-11-04
>
> 对应需求：[requirements.md](requirements.md)

## 1. 目标与设计边界

本设计描述面向自部署 Gitea 的 CLI、Git 集成、通用 API 和本地 MCP 服务。项目使用 Go 1.27.3 构建，交付 macOS 和 Windows 预编译程序；首版同时覆盖通用 API 调用及常用业务操作。具体业务命令和 MCP 工具清单仍需确认。

本文的命令名 gitea-cli 是设计占位符，示例和接口均为建议，尚未代表最终命令契约。除用户已确定的要求外，架构、组件选型和平台矩阵均为待评审方案。版本兼容目标为 28.3.0、1.27.3、1.26.4、1.25.5、1.24.7、1.23.8 和 1.22.6；目前均未完成实例验证。28.3.0 的官方 API 文档与发布说明已可查，测试产物和实际兼容性仍待验证。

## 2. 总体架构

建议使用一个可执行程序，同时提供 CLI 和 stdio MCP 服务。两个入口调用同一应用服务，统一处理仓库上下文、权限、API 版本差异和错误。MCP 直接调用应用服务，不通过运行 CLI 子进程并解析文本输出实现。

~~~mermaid
flowchart LR
    CLI[CLI 命令] --> APP[应用服务]
    MCP[MCP stdio 服务] --> APP
    APP --> CTX[实例和仓库上下文]
    APP --> GIT[Git 适配器]
    APP --> API[Gitea API 客户端]
    APP --> AUTH[配置和凭证]
    API --> COMPAT[版本能力适配]
    GIT --> SYSTEMGIT[本机 Git]
    API --> SERVER[Gitea 实例]
~~~

| 组件 | 职责 |
| --- | --- |
| CLI 入口 | 参数解析、交互、输出格式与进程退出状态。 |
| MCP 入口 | 协议生命周期、工具描述、输入校验及结构化结果。 |
| 应用服务 | 定义业务操作与共用流程，不依赖终端或 MCP 传输。 |
| 上下文 | 解析实例、账号、本地仓库和远程仓库，处理歧义。 |
| Git 适配器 | 调用系统 Git、读取仓库上下文、按需访问凭证辅助程序。 |
| API 客户端 | 认证、HTTP 请求、分页、上传下载、错误及取消。 |
| 兼容层 | 获取服务端版本，按操作适配已验证的差异。 |
| 配置与认证 | 管理多实例配置、凭证引用和安全存储。 |

## 3. 工程组织与依赖

开发、CI 和发布构建使用 Go 1.27.3；工具链升级应经过兼容性和测试评估。HTTP、进程管理及基础编码优先评估标准库。CLI 命令解析与终端样式拟采用 Cobra 和 Lip Gloss，职责见第 4.7 节；具体依赖版本及 MCP SDK、系统凭证库在实现前评估确定，并记录许可证和维护状态。

建议目录如下，名称和边界可随实现调整：

~~~text
cmd/gitea-cli/       程序入口
internal/cli/        命令行适配
internal/mcp/        MCP 服务及工具
internal/app/        应用服务
internal/context/    实例与仓库上下文
internal/git/        Git 进程和仓库信息
internal/gitea/      HTTP API 客户端
internal/compat/     版本能力与适配
internal/config/     配置读写
internal/auth/       凭证来源及存储
internal/output/     输出和脱敏
testdata/            测试数据
tests/integration/   集成测试
docs/                设计与使用文档
~~~

依赖方向为入口层调用应用层，应用层通过明确接口调用基础设施；基础设施不依赖 CLI 或 MCP 的输出格式。接口以实际变化边界为依据，避免为每个函数抽象接口。

## 4. 命令与上下文

### 4.1 范围与设计约定

本节以 [Gitea 28.3.0 API 文档](https://docs.gitea.com/api/)和 [OpenAPI 定义](https://docs.gitea.com/openapi3-28.json)为例，优先设计 Issue、PR 和 Release 的常用命令，仓库、分支、标签及提交状态作为工作流支撑。下表是供评审的候选设计，不表示命令名称、首版逐项范围或其他版本的兼容性已经确定；通用 API 入口继续提供完整访问能力。

下表路径均相对于实例的 API 根路径 /api/v1；R 表示 /repos/{owner}/{repo}，N 表示仓库内 Issue 或 PR 编号，ID 表示 Release ID。仓库、编号和标签应按路径段编码；Release 的标签名与 ID 不能混用。

### 4.2 Issue

| 建议命令 | 操作 | 28.3.0 API |
| --- | --- | --- |
| issue list | 列表、状态及标签等筛选 | GET R/issues |
| issue view N | 查看详情 | GET R/issues/{index} |
| issue create | 创建 Issue | POST R/issues |
| issue edit N | 编辑标题、正文及里程碑等 | PATCH R/issues/{index} |
| issue close N / reopen N | 关闭或重新打开 | PATCH R/issues/{index}，设置对应状态 |
| issue comment list N / add N | 查看或添加普通评论 | GET / POST R/issues/{index}/comments |
| issue assignee add N / remove N | 添加或移除负责人 | POST / DELETE R/issues/{index}/assignees |
| issue label list N / add N / remove N | 查看、添加或移除标签 | GET / POST R/issues/{index}/labels；DELETE R/issues/{index}/labels/{id} |

Issue 列表应显式区分 Issue 和 PR：28.3.0 的列表接口提供 type 参数，Issue 命令应请求 issues 类型；其他版本的实现及回退需验证。标签操作区分增量添加、移除和整体替换，不能把添加实现为覆盖现有标签。里程碑使用编辑接口设置，清空语义及各版本字段支持需验证。

### 4.3 Pull Request

| 建议命令 | 操作 | 28.3.0 API |
| --- | --- | --- |
| pr list / view N / create | 列表、详情和创建 | GET / POST R/pulls；GET R/pulls/{index} |
| pr edit N / close N / reopen N | 编辑、关闭或重新打开 | PATCH R/pulls/{index} |
| pr files N / commits N | 查看变更文件及提交 | GET R/pulls/{index}/files；GET R/pulls/{index}/commits |
| pr comment list N / add N | 查看或添加普通讨论评论 | GET / POST R/issues/{index}/comments |
| pr review list N / create N | 查看审查及提交审查意见 | GET / POST R/pulls/{index}/reviews；已有审查提交使用 POST R/pulls/{index}/reviews/{id} |
| pr reviewer add N | 请求审查 | POST R/pulls/{index}/requested_reviewers |
| pr merge N | 合并 PR | POST R/pulls/{index}/merge |

PR 普通评论使用 Issue 评论接口；审查记录、审查评论和代码行评论使用 PR 审查相关接口，不能视为同一种评论。首版是否提供代码行评论专用命令待定。创建 PR 需要明确源分支与目标分支，跨仓库参数依服务端能力验证；合并方式、是否删除源分支及交互确认应由用户显式选择或按已确认默认值执行，权限、冲突和仓库保护规则由服务端判定，失败不能当作成功。

### 4.4 Release

| 建议命令 | 操作 | 28.3.0 API |
| --- | --- | --- |
| release list | 列出版本 | GET R/releases |
| release view TAG | 按标签查看 | GET R/releases/tags/{tag} |
| release create TAG | 创建 Release，可指定草稿、预发布及说明 | POST R/releases |
| release edit TAG | 修改名称、说明、草稿或预发布状态 | GET R/releases/tags/{tag} 获取 ID，再 PATCH R/releases/{id} |
| release asset list TAG | 查看附件 | 按标签查询 ID，再 GET R/releases/{id}/assets |
| release asset upload TAG FILE | 上传附件 | 按标签查询 ID，再 POST R/releases/{id}/assets |

Release 与 Git 标签是不同资源。创建 Release 对不存在标签的处理、标签指向、草稿发布规则和重复附件处理，需要结合服务器版本、权限及团队约定验证；失败时明确报告实际已完成的步骤。上传采用接口要求的文件表单，流式处理文件，报告文件过大、无权限或网络中断；写入结果未知时先查询服务器再决定是否重试。Release 或附件删除和附件下载的专用命令待确认，可先使用通用 API。

### 4.5 仓库和开发上下文

| 建议命令 | 操作 | 28.3.0 API |
| --- | --- | --- |
| repo view | 查看仓库 | GET R |
| branch list | 列出分支 | GET R/branches |
| tag list | 列出标签 | GET R/tags |
| commit list / status REF | 查看提交列表及指定引用的汇总状态 | GET R/commits；GET R/commits/{ref}/status |
| label list / milestone list | 列出仓库标签及里程碑 | GET R/labels；GET R/milestones |

建议显式指定的实例、仓库或 remote 优先于自动推断；缺省值可来自当前实例配置和本地 remote。具体优先级、默认 remote 和冲突规则需形成统一契约。多 remote 或多实例无法唯一匹配时返回可操作的错误，交互式选择可作为补充，非交互模式不得猜测。显式指定实例和仓库时，在非 Git 目录也应能使用 API 命令。

### 4.6 使用示例和输出

以下仅展示预期体验，参数和默认值仍待确认：

~~~sh
gitea-cli issue list --repo acme/demo --state open
gitea-cli issue create --repo acme/demo --title "修复登录问题" --body "问题描述"
gitea-cli pr create --repo acme/demo --base main --head feature/login --title "修复登录"
gitea-cli pr review list 42 --repo acme/demo
gitea-cli pr merge 42 --repo acme/demo
gitea-cli release create v1.0.0 --repo acme/demo --draft --notes-file notes.md
gitea-cli release asset upload v1.0.0 ./build/app.zip --repo acme/demo
gitea-cli api --instance team --method GET /repos/acme/demo
~~~

列表建议默认一页，支持显式分页与全部页获取；字段、筛选和默认排序按各端点能力确定。普通命令建议提供人类可读和 JSON 输出，正常结果写 stdout，诊断信息写 stderr。非交互式写入必须提供必要输入，禁止意外进入交互；通用 API 保留原始响应和文件流能力。

### 4.7 终端兼容与输出样式

#### 支持目标与库选择

CLI 以 macOS Terminal、iTerm2 和 Windows PowerShell 为终端使用目标。PowerShell 是 shell，实际样式还取决于其所在终端宿主；Windows 验证至少覆盖 Windows Terminal，并根据团队环境确定是否覆盖传统控制台及具体 PowerShell 版本。最低操作系统版本和完整终端矩阵尚待确认，目前没有实测兼容结论。

| 组件 | 选择 | 职责 |
| --- | --- | --- |
| 命令与参数 | [Cobra](https://github.com/spf13/cobra) | 命令层级、参数、帮助及 shell 补全。 |
| 输出与布局 | [Lip Gloss](https://github.com/charmbracelet/lipgloss) | 彩色、加粗、对齐、边框与表格布局。 |

具体版本在实施时结合 Go 1.27.3、平台兼容性、许可证和维护状态评估并锁定。当前普通 CLI 不引入完整的终端交互框架；如后续需要键盘导航的交互工作台，可另行评估 Bubble Tea。业务数据和应用服务不依赖终端样式库，文本渲染集中在输出层。

#### 输出契约

- 交互式终端默认输出可读文本：Issue、PR 和 Release 列表可使用带表头的表格，状态和重点信息可用颜色或加粗辅助区分；信息不能只依靠颜色表达。详情及错误按内容选择合适布局。
- 建议提供 `--color=auto|always|never`：默认 `auto`，按对应输出流是否连接终端及终端能力判断；`always` 显式启用颜色，`never` 显式关闭。未显式设置颜色选项时尊重非空的 `NO_COLOR`；显式选项优先。stdout 与 stderr 分别判断，不因其中一个连接终端就假定另一个也连接。
- 管道和文件重定向在默认 `auto` 模式下不输出 ANSI 颜色，也不输出动画或动态进度；`--color=always` 允许用户有意输出颜色。检测不到终端能力时使用可读的无样式文本。
- JSON 输出保持可机器解析：stdout 仅含 JSON，不混入颜色、表格、进度或提示。通用 API 的原始响应及下载内容不做样式包装或修改；错误与诊断写入 stderr。
- MCP stdio 的 stdout 仅承载协议消息，不经过终端渲染；诊断写入 stderr，不向协议流输出进度、交互提示或 ANSI 样式。
- Git 透传入口继承 Git 的终端输入输出和颜色配置，不对 Git 输出二次着色或排版。
- 表格根据可用宽度布局，对中文、Emoji、长标题、空值及窄终端进行显示宽度处理；需要截断时给出可识别标记，并保留通过详情或 JSON 获取完整数据的方式。纯文本默认值和具体列集合待命令契约确定。
- 渲染服务端返回的标题和正文时，避免把其中的终端控制字符当作控制序列执行；JSON 与通用 API 原始输出保持其各自的原始语义。

示意如下，颜色和加粗在此纯文本示例中不展示：

~~~text
#    状态    标题              负责人     标签
42   OPEN    修复登录问题      chiyoshi   bug
38   OPEN    增加发布流程      alice      enhancement
~~~

## 5. Git 集成

### 5.1 执行与仓库发现

建议首版调用系统 Git，以复用用户已有配置、凭证辅助程序和 SSH 环境；需要 Git 的操作应报告缺失的 Git，并允许后续评估配置可执行文件路径。仅调用 Gitea API 的场景不依赖安装 Git。Git 最低支持版本尚待实测确定。

通过参数数组启动进程，不将用户参数拼接为 shell 命令。CLI 透传入口继承终端输入输出，保留 Git 的正常退出码和交互；启动失败、收到信号或被取消时单独报告。覆盖 Windows 路径、参数引用和子进程清理。

通过 Git 自身查询仓库根目录、当前分支和 remote，支持子目录、worktree 和 .git 为文件等情况。解析 HTTPS、SSH 和常见 SCP 风格 remote，并考虑端口、服务子路径、SSH 主机别名、fork 和多个 remote。SSH 地址无法确定 HTTP 实例时使用配置映射或要求显式指定，不能仅凭相似主机名猜测。

### 5.2 凭证与非交互执行

Git 远程操作继续使用 Git 配置的凭证辅助程序或 SSH 机制。API 认证与 Git 认证分开管理；可选地提供明确启用的 Git 凭证来源，按协议、主机、路径及必要的用户名查询辅助程序，并在用于 API 前验证适用性。获取到的密钥仅在所需期间保存在内存，不读取 SSH 私钥内容，不写入日志。

MCP 的 stdin 和 stdout 属于协议连接，不可交给 Git 进程。MCP 中的 Git 调用须捕获输出、禁止交互、设置超时，响应取消并清理子进程；还需处理辅助程序或 SSH 仍可能阻塞的情况。是否向 MCP 暴露任意 Git 子命令和写入操作待确认。

## 6. Gitea API 客户端

### 6.1 通用调用

通用入口允许选择实例、HTTP 方法、API 路径、重复查询参数、请求头及请求体，并支持 JSON、原始内容、文件上传及流式下载。保留状态码、必要响应头和原始响应体供脚本或调用者处理；不得依赖已登记的业务端点白名单。用户可访问的具体 API 仍受实例支持范围、服务器配置及账号权限约束。

实例地址可能包含反向代理子路径；URL 构造需正确编码路径段与查询参数，防止路径穿越以及将凭证发送到其他来源。重定向跨来源时不自动转发认证信息。默认校验 TLS，支持显式配置私有 CA；证书策略和代理行为需有文档。

建议默认只请求一页；自动获取全部页面须显式启用，并设置页数或数量上限及重复页检测。请求支持超时和取消；只对适合重试的读取请求执行有界重试，写请求默认不自动重试。写入后连接中断时报告结果未知，提示用户查询服务器状态，避免误报失败后重复创建。大文件流式处理；下载文件使用临时文件并在成功后替换目标的具体规则待确认。

### 6.2 业务操作

应用服务提供 Issue、Pull Request、Release 及开发上下文等常用操作，候选命令与 28.3.0 接口映射见第 4 节，参数经验证后交给 API 客户端。各命令的首版清单、写操作及输出字段待产品确认。通用 API 覆盖入口与业务命令并行：能构造服务器支持的请求并不意味着每个端点都有专用命令，或全部端点均经过逐版本测试。

## 7. Gitea 多版本兼容

### 7.1 检测与适配

建议通过实例的版本 API（候选路径 /api/v1/version）识别服务端，保留原始版本字符串；缓存以完整实例地址为边界，允许刷新。版本获取失败或版本未知时，通用 API 仍允许用户按请求尝试；业务命令只在有足够证据时使用已知适配，否则给出明确诊断，不将单个 404 直接解释为版本不支持。

兼容层按具体操作维护能力与已验证的请求、响应差异，避免每个版本复制一套完整客户端。版本判断、字段适配与响应解析集中测试。可选字段必须区分未提供、零值和清空；服务器不支持用户指定的字段或语义时应报错，不静默丢弃字段或改变写入含义。真实差异以各版本 API 定义及实际测试为依据，不预先假定。

### 7.2 首版验证矩阵

| Gitea 目标版本 | 版本来源 / 测试产物 | 通用 API、业务命令和 MCP 回归 |
| --- | --- | --- |
| 28.3.0 | [官方发布说明](https://blog.gitea.com/release-of-28.3.0/)；测试产物待确认 | 未验证 |
| 1.27.3 | 待确认 | 未验证 |
| 1.26.4 | 待确认 | 未验证 |
| 1.25.5 | 待确认 | 未验证 |
| 1.24.7 | 待确认 | 未验证 |
| 1.23.8 | 待确认 | 未验证 |
| 1.22.6 | 待确认 | 未验证 |

这些是需求指定的精确目标，表格不是已支持声明。每个版本需记录发行来源、实例构建信息、相关 API 定义、执行时间与测试结果；模拟服务仅验证客户端逻辑，不能证明实际版本兼容。其他补丁版本是否承诺支持待确认。Go 工具链版本、Gitea 服务端版本和 API 路径版本分别管理。

## 8. 配置、认证与安全

建议配置记录实例名称、完整服务地址、默认账号、仓库映射、私有 CA 路径和凭证引用；配置文件不保存明文 token。配置路径、格式及跨平台位置待确定。建议优先级为显式参数、环境变量、实例配置及程序默认值；不同配置项适用的来源、空值处理和冲突提示应在实现前逐项确定。

初步使用 API token，支持通过安全交互输入配置，CI 可通过环境变量注入；不建议通过普通命令参数传递 token，避免进入 shell 历史和进程列表。MCP 的协议 stdin 不用于登录输入。持久凭证建议交由系统凭证管理器保管，密钥与完整实例地址及账号绑定，不遍历尝试其他实例的凭证。系统存储不可用时明确报错或使用用户显式提供的环境变量，不静默回退至明文文件。

配置写入采用临时文件和安全替换，限制平台允许的文件访问权限，并预留配置版本和迁移机制。日志、错误、URL、remote 和调试输出均需脱敏；服务器响应可能包含敏感信息，展示和记录前应按输出场景处理。配置、凭证和私有 CA 的实际平台实现与故障行为需在各系统验证。

## 9. MCP 服务

服务由同一可执行文件启动，使用本地 stdio 传输。MCP 层通过选定 SDK 处理初始化、能力协商、工具发现、调用、取消和连接关闭；stdout 只承载协议消息，诊断写 stderr，进程退出时清理正在执行的操作。

建议先设计仓库、Issue、PR 的只读工具，提供明确实例、仓库及工作目录输入，并复用应用服务的认证和上下文解析。MCP 服务启动目录不一定是目标仓库，不应假定当前目录有效。工具应提供稳定的输入 schema、结构化结果和可区分的错误，并限制调用时长、并发、输出和文件访问范围。

首版需求包含约定的授权写入场景；写入和删除工具、通用 API 工具、任意 Git 工具的具体范围，以及确认由客户端还是服务端执行，仍需确定。确认机制不能替代 Gitea 的账号权限检查。不得将 token 返回给 MCP 客户端，通用工具的认证头及跨来源请求要受同样约束。

## 10. 错误、输出与可观测性

内部区分配置、认证、权限、仓库上下文、网络/TLS、服务端、版本能力、取消和写入结果未知等错误类别；保留必要上下文和底层原因。CLI 输出可操作的提示并返回非零状态，Git 透传保留正常 Git 退出码；MCP 返回协议允许的结构化工具错误。具体退出码映射和 API 响应格式待确定。

默认日志保持精简，调试日志可显式开启；所有级别均不得输出凭证。长输出和二进制响应应按流处理或限制大小，避免在 MCP 消息中无界返回。

## 11. 构建与分发

已确定为 macOS 和 Windows 提供预编译程序，用户无需安装 Go；同一程序包含 CLI 与 MCP 服务。建议首版构建 darwin/arm64、darwin/amd64 和 windows/amd64；windows/arm64 在具备运行验证条件后决定是否正式支持，Linux 支持仍待确认。最低操作系统版本、Git 版本及第三方依赖的平台限制需核实。

建议 macOS 提供 tar.gz、Windows 提供 zip，附程序版本、构建提交、使用说明、许可证和 SHA-256 校验值；通过团队 Gitea 发布页分发是待确认方案。每个平台必须运行真实环境冒烟测试，交叉编译成功不足以证明可用。建议首版手动下载替换并保留配置；签名、公证、包管理器、自动更新及回滚策略待确认。

## 12. 测试和验收方案

| 层级 | 验证内容 |
| --- | --- |
| 单元测试 | 上下文优先级、remote 解析、版本能力、字段差异、配置及脱敏。 |
| HTTP 契约测试 | 方法、路径、认证、分页、重定向、错误、超时及流式请求和响应。 |
| Git 集成测试 | 临时仓库与 worktree、多个 remote、参数和退出码、凭证辅助程序、非交互取消。 |
| Gitea 实例回归 | 对第 7 节每个精确版本验证通用 API、仓库、Issue、PR、Release 及约定 MCP 工具。 |
| MCP 测试 | 协议初始化、工具发现与调用、错误、取消、EOF 清理，并使用真实客户端验证。 |
| 终端与输出测试 | 在 macOS Terminal、iTerm2 和目标 Windows PowerShell / 终端宿主验证颜色、加粗、表格、中文及窄窗口；覆盖重定向、NO_COLOR、颜色选项、JSON、原始响应与 MCP 协议输出。 |
| 发布冒烟测试 | 各正式支持系统和架构执行版本查看、API、Git 及 MCP 基本流程。 |

重点覆盖私有 CA、缺少 Git 或凭证、remote 歧义、未知版本、包含空格或中文的路径、大文件、跨来源凭证保护和写入结果未知。业务流程覆盖 Issue 只列出 Issue、标签增量修改、PR 普通评论与审查区分、合并受保护分支失败、Release 按标签解析及附件上传。使用隔离的测试实例、组织及仓库执行有副作用的测试；测试数据和账号权限由测试环境准备。上述是后续测试计划，目前没有实现或执行结果。

发布前逐项核对需求中的验收场景，记录每个 Gitea 版本、平台和测试结果；任何未验证的版本或环境应明确标记，不能以模拟测试或构建结果代替。

## 13. 实施顺序与待决事项

建议按工程与构建、配置和通用 API、Git 与上下文、业务命令和版本适配、MCP、平台测试与发布的顺序推进；这是依赖顺序，不改变首版需求范围。

| 待决事项 | 需要确定的内容 |
| --- | --- |
| 产品和命令 | 程序名称、Git 入口、业务命令清单、参数与输出契约。 |
| 版本验证 | 所有目标版本的测试实例、28.3.0 测试产物及版本差异证据。 |
| 认证 | 支持的认证方式、多账号、凭证存储、Git 凭证复用与优先级。 |
| MCP | 工具清单、写入与删除范围、确认机制及资源限制。 |
| 平台 | 架构、最低系统与 Git 版本、Windows ARM 和 Linux 的支持条件，以及 PowerShell 版本和终端宿主验证矩阵。 |
| 发布 | 发布位置、签名、公证、安装渠道、升级与校验值的可信分发。 |
| 依赖和配置 | Cobra、Lip Gloss 的具体版本及 MCP/凭证库、配置格式与路径、版本迁移规则。 |
