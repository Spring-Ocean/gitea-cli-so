# P5 完整交互与终端体验

更新日期：2026-11-07。对应 P5-01 至 P5-14；iTerm2 用户反馈及首页资料改进的历史验证仍分别见第 7、8 节。本轮新增需求与实现范围见第 9 节，其完成和验证状态以开发计划为准，不由旧二进制证据推定。目标平台和 GUI 宿主实测单独记录。

## 1. 入口与共享边界

无参数且 stdin、stdout、stderr 都是真实终端，未设置 CI、--json、--no-interactive 时进入持续菜单。任一流重定向时显示帮助并成功退出；--json 无子命令返回结构化参数错误 2，--help 优先。--yes 只跳过普通确认，不补字段或自动选择目标。

菜单从实际注册命令生成导航，覆盖认证、实例、仓库开发资源、Issue、PR、Release、API、Git 和版本。MCP 使用独立 sotea mcp serve 入口，不在全屏菜单中启动协议服务；completion 和 help 保留直接命令入口。

每次动作创建独立参数容器，复用同进程适配器与应用服务，不启动另一 CLI 或解析文本输出。字段绑定真实 flag，应用服务不读取终端。类型化 app.Result 保留最近业务逐项结果，partial／unknown 不会因返回菜单变成成功。Git／API 保留原始流，菜单另显示动作摘要。

## 2. 按键与降级

| 场景 | 行为 |
| --- | --- |
| VT 导航／单选 | ↑／↓ 或 j／k 移动，Enter 直接进入菜单或接受当前资源；导航不显示勾选框，不需要 Space。Enter 是明确选择，未按 Enter 不产生业务目标。 |
| VT 多选 | 只有批量资源使用 Space 勾选，再 Enter 继续；初始不勾选，最多 100 个目标。 |
| 候选列表搜索／分页 | / 输入搜索，Enter 查询；n／p 或 PageDown／PageUp 翻页；支持空格搜索。稳定 ID 跨页保留。 |
| 结果分组详情 | PageUp／PageDown 切换顶层章节，Tab／Shift+Tab 为替代键。↑／↓、Home／End 在当前超长章节内滚动；视口放得下时整组显示，组超出视口时才组内滚动。嵌套章节留在所属顶层组。左右键与 h／l 不切组，保留给选项备选值。 |
| 已选 | s 检查全部已选，Space 可移除隐藏选择；业务目标最多 100 个。 |
| 加载／错误 | 显示 Loading／空状态，r 重试失败读取；新请求取消旧请求，过期响应不替换当前页。 |
| 返回 | 子菜单 Esc 返回直接上层；结果页 Esc 返回刚才子菜单；字段或资源取消结束当前动作并回该业务层；搜索编辑中的 Esc 只结束搜索编辑。 |
| 退出 | 根菜单 Esc／Ctrl+C 正常退出，也有 Quit 项；进程 SIGINT 按取消退出 130。 |
| 单行输入 | Esc／Ctrl+C／Ctrl+D 取消并恢复模式；长正文使用 body-file／notes-file。 |
| 静态降级 | TERM=dumb／未知能力不启动 VT 全屏。导航／单选输入编号直接进入；多选编号切换、n／p 翻页、/query 搜索、s 检查、done 明确继续、q／Esc 取消。 |

选择器写 stderr，退出先取消并等待响应 context 的 provider 完成，再交还终端；窗口变化重新计算宽度。NO_COLOR 管理样式，不禁止具备能力的终端使用光标布局。

统一语义样式：Run with options · filters and advanced fields 使用紫色粗体；章节标题使用青绿色粗体；主要动作使用蓝色粗体，辅助提示使用灰色弱化字重，返回操作使用橙色粗体。关闭颜色或终端不支持 ANSI 时，操作文字、位置和主次结构仍保持清楚。

## 3. 命令与表单覆盖

读命令 Enter 直接查询默认页；每层的 Run with options 可先选命令再编辑筛选／分页等真实 flag。需要字段的写命令进入表单；edit 在选定目标后补字段。非交互模式从不启动补参。

| 命令 | 直接输入 | 缺参／菜单行为 |
| --- | --- | --- |
| auth login／refresh | 来源、认证类型，安全 token 或专用 stdin | 补来源，环境 token 可直接验证；手输不回显。refresh 限已有账号。菜单不启用 with-token，可安全手输。 |
| auth status／switch／logout | check、账号、退出选项 | 歧义明确选择；切换验证身份，退出遵循抑制与环境凭证契约。 |
| instance info | 实例、refresh | 缺实例补齐，版本不替代权限验证。 |
| repo view；branch／tag／commit／label／milestone list | 上下文、分页 | 缺上下文补齐；commit status 可给 REF，缺有效匹配 HEAD 时明确输入。 |
| issue list | 状态、关键词、标签、负责人、里程碑、分页 | 直接展示默认页；Run with options 编辑筛选，不要求选择单个 Issue。 |
| issue view／edit／comment list／comment add／label list | 一个 NUMBER，字段／正文来源 | 单选编号；edit 缺字段进表单，无修改可返回；评论缺正文输入。 |
| issue close／reopen | 1–100 个 NUMBER | 跨页多选，完整预览编号；直接编号同样确认。 |
| issue label／assignee add／remove | 一个 NUMBER，多个 label／user | 单选 Issue、多选值；移除列当前集合，添加列有界仓库候选。 |
| pr list | 状态、关键词、base、author、分页 | 同直接参数的筛选表单，识别已合并状态。 |
| pr view／files／commits／comment list／review list | 一个 NUMBER | 单选 PR，不用 Issue 候选替代。 |
| pr create | 标题、base、head、共同字段 | 缺标题输入，分支建议须明确接受；字段页可选远端分支，fork 明确填写，不 push。 |
| pr edit／comment add | 一个 NUMBER，字段／正文 | 单选；缺字段进表单，未给字段不变。 |
| pr close／reopen | 1–100 个 NUMBER | 多选，排除不适用的已合并 PR。 |
| pr review create | 一个 NUMBER，event、body、commit／review-id | 单选，明确选择三种事件；comment／request-changes 缺正文输入；计划固定 SHA。 |
| pr reviewer add | 一个 NUMBER，多个 user／team | 单选 PR；明确输入账号或多选仓库负责人候选，权限仍由 Gitea 验证；团队明确填写，不枚举全站账号。 |
| pr merge | 一个 NUMBER，method、delete-branch | 单选未合并 PR；字段页设置方法／删分支，默认 merge／留分支，预览 SHA。 |
| release list／view／asset list | 筛选／分页，或 TAG | 缺标签单选实际 Release 的完整标签，不把标签当 ID。 |
| release create | TAG、target、title、body／notes-file、状态 | 选已有 Git 标签或明确输入；新标签明确 target，字段页可选分支、说明文件和状态，默认草稿。 |
| release edit | TAG、明确字段 | 单选 Release；缺字段进表单，无修改返回，布尔值明确 true／false。 |
| release asset upload | TAG、一个 FILE、可选 name | 单选 Release，输入一个普通文件路径；中文／空格路径作为一个值，不开原生文件对话框。 |
| api PATH | 方法、查询、头、正文／文件／multipart、输出路径 | 字段页提供 API 选项，PATH 明确输入；保持来源互斥和原始文件流。 |
| git -- ARGS | 参数数组 | 逐个输入字面参数，空格保留在同一参数；Done 后结束渲染再交接 Git，结束恢复菜单，保留原码。 |
| version | 无业务输入 | 查看本地版本，不访问实例。 |

Skip 和空行表示保留，不是清空。Clear 正文映射到 clear-body 或允许的空正文；集合／里程碑使用各自 clear 选项。已提供 flag 标识 set，只有主动编辑才修改。冲突来源由同一参数／应用层拒绝，不静默覆盖文件来源或状态；Issue／PR 标题要求非空。

## 4. 会话与确认

首页卡片显示实例、仓库、API 用户资料和目录；窄屏可用 Context → Inspect 查看完整根地址与来源。会话显式值与命令参数同优先级，显示为 argument。动作中明确补齐的上下文保留到本会话；remote／配置推导值继续每次解析，不固化旧身份。

Context 普通切换不保存。切换实例清除显式账号／仓库／remote，并停止当前目录自动推导，要求新仓库明确提供；Reset 恢复环境／配置及本地推导。Save defaults 独立预览、默认 No，验证已有账号后保存默认实例／活动账号，仅为匹配本地 remote 保存绑定。非仓库目标不伪造本地绑定。

每次重读配置内容摘要，外部修改／另一进程退出登录在下一动作生效。认证服务重新核对身份，不缓存系统凭证或 helper 材料；环境 token 仍由外部管理。保存比较原快照，确认期间外部修改返回 CONFIG_CONFLICT，不覆盖。

写入沿用 P3／P4 单次计划。空确认等同 No，y／yes 或明确 --yes 接受。最近业务结果可完整回看；取消不代表回滚，未知写入不重试。

## 5. 输出与依赖

列表按模型选择 NUMBER、STATE、TAG NAME、LOGIN、NAME、TITLE、DRAFT、SIZE、FILENAME、SHA 或 ID 列，避免重复内部 ID；无共同列时输出完整对象。表头和状态可加粗／着色，含明确文字。真实终端按宽度截断并显示省略号，管道保留完整列值，view／--json 保留完整字段；详情采用可读标签、章节和缩进层级，不再转储 JSON；完整正文保留换行。

stdout／stderr 分别判断终端。--color=auto 遵守非空 NO_COLOR、终端和 TERM；always／never 优先于环境。JSON、原始 API、Git、MCP 不经业务样式包装，JSON 预览也无样式。远端控制符和双向覆盖字符在文本展示前清理；原始流保持自身契约。

直接使用已有传递依赖 Lip Gloss v1.1.0、termenv v0.16.0、uniseg v0.4.7，无版本升级。独立 renderer 避免全局样式状态，uniseg 处理完整字素／Emoji 宽度；go-runewidth v0.0.16 保留为传递依赖，Bubble Tea 仍 v1.3.10。上述模块根 LICENSE 为 MIT；分发仍需保留依赖版权，完整许可盘点按 P8。Go、模块路径、项目 MIT 署名不变。

## 6. 验证口径

~~~sh
python3 scripts/check.py --phase p5
go test -race ./...
python3 scripts/p5-terminal-smoke.py
python3 scripts/p5-gitea-menu-smoke.py --versions 1.22.6
~~~

真实菜单脚本在七个固定隔离版本上创建随机 p5 前缀资源和独立 token，从子目录无参数进入，验证登录、候选、拒绝、关闭、草稿与上传，再按精确 ID 清理。说明／附件路径含中文和空格。Docker 沿用 P3 主机镜像归档及摘要核对轮换，只操作本项目资源，保留卷，不执行全局清理。

合成 PTY 覆盖 VT／静态会话、非仓库补参保留、会话切换不落盘、入口矩阵、加载取消／resize、无修改、清空、unknown 保留及不重放、审查、草稿、Git 空格参数／非零、保存／并发冲突、窄屏字素／颜色／完整 JSON。P2 隐藏输入／取消、P3 跨页／搜索、P4 PR／Release 选择及 MCP 原型另作回归，不外推为完整 P6。

| 目标 | 验收口径 |
| --- | --- |
| macOS 27 arm64，真实 POSIX PTY | 本机键盘、I/O、取消及恢复；七版实际 Gitea 链路。 |
| Terminal／iTerm2 GUI | 待实测。此前 GUI 工具明确拒绝控制 Terminal，应用安装不算宿主验收。 |
| Intel／最低 macOS | 待实测，交叉编译只证明构建。 |
| Windows 10 22H2／Windows 11，Terminal + PS5.1 | 待实测，支持要求保留。 |
| Windows 11 + PS7.6、传统控制台 | 待实测，POSIX 的 dumb 降级不替代 Windows 控制台。 |

P5 为实现通过、目标宿主待实测；宿主矩阵闭合前不声明全平台验收通过。

## 7. iTerm2 反馈后的持续界面修订

用户在 iTerm2 实机发现旧版将导航项当作批量资源勾选，且结果退屏后才进入 shell。该反馈是缺陷发现证据，不能算完整宿主验收通过。

本次修订使用单一 Bubble Tea Program 和一次持续全屏：导航、资源选择、输入、确认预览、加载和结果页共用终端所有权。列表和完整详情留在屏内，可用 ↑／↓、PgUp／PgDn、Home／End 滚动；Esc 返回当前操作的直接上级，保留菜单焦点。最近结果仍来自类型化 app.Result，不通过解析 CLI 输出恢复业务状态。显示缓存有 2 MiB 上限并明确提示省略；原始 API／Git 不经此缓存，按既有语义暂停渲染、交接真实流，再恢复会话。

VT 持续界面启动及回到主页时重新发现仓库和配置：先匿名读取版本，再仅对已登记完整根地址验证身份、读取仓库。已登记实例匿名版本被 401／403 拒绝时可重新核对已授权凭证后探测；未知来源不会读取或发送 token/helper。未确认来源的公开读取可匿名探测，但业务首次连接仍保留来源确认。没有仓库、地址不可用、非目标 API 响应或身份无效时显示 Repository unavailable 和具体原因，保留 Account、Context、Instance、Git、Version、Refresh 与退出入口。404、401、网络及代理错误不直接断言远端不是 Gitea。首页只有真实验证过的身份才显示 Signed in as；无身份显示 Anonymous，未知来源额外标注 Source confirmation required，来源细节在 Context 中查看。

修订状态：全量竞态、vet、三平台构建、完整 VT／静态 PTY、持续界面加载取消与 resize、P2 隐藏输入及 P3／P4 选择器回归通过。最终 macOS arm64 产物 SHA256 为 c625a12f82e930ba04dbd5595cd9e2cc3210540a8684d0ad4980e0c58275e43b，源码摘要与构建清单一致；七个指定版本的最终真实菜单链路及精确资源清理全部通过，见 [修订机器证据](validation/p5-menu-revision-result.json)。本项目服务全部停止，七卷保留，原 1.22.6 驻留镜像／容器已恢复。原证据不覆盖新产物；用户 iTerm2 复测与 Windows／GUI 宿主仍待实测。

## 8. 首页资料与全部业务结果展示

本轮按用户要求扩大到全部面向人的业务 JSON：列表保留表格，详情／预览／诊断使用统一可读字段树，Repository 单独按 Overview、Activity、Links、Access 分组，其余属性放入 Additional details。不会通过删字段换取简洁；未知字段、多行正文、大整数、空值和嵌套数组均保留。--json 与直接 api 的机器输出不变；持续菜单中的合法 JSON 响应（最多 2 MiB）完整格式化并留屏，非 JSON／超限响应及文件输出仍遵守原始流边界。

首页采用 sotea 品牌边框卡片和重点操作导航，展示 API 实际返回的显示名、@login、可选邮箱、实例、仓库、目录、连接状态及最近结果。资料来自认证服务同一次 GET /user，不通过额外身份请求或 Git 作者推断。无仓库但实例和身份已明确时仍显示用户；认证失效或换账号后不保留旧资料。窄／矮窗口压缩卡片和菜单可见范围，所有动作仍能通过方向键或搜索进入。用户界面使用英文，远端内容保持原文。

本轮全量竞态、vet、三平台构建、完整 VT／静态 PTY、P2 隐藏输入和 P3／P4 选择器回归通过；同一最终产物七版本资料／仓库概览及真实业务链路全部通过，详见 [本轮机器证据](validation/p5-home-display-result.json)。已检查本机合成 API 的实际终端输出：姓名／邮箱来自已验证资料，Repository 分组、JSON API 嵌套数据与脱敏均在屏内可见。前两份 P5 记录仅适用于其各自的产物，不替代本轮或 iTerm2／Windows 实机验收。

本轮 macOS arm64 二进制摘要：7dfa8c33fe85e19af145ed20c542467458490fc702c25eb5e3828fc3780c5fcc。仍使用 dist/p5/darwin-arm64/sotea 复测；目标 GUI／Windows、Intel／最低 macOS 保持待实测。本项目测试资源已精确清理、服务停止、数据卷保留。

## 9. 章节分页与账户目录增量

本节记录新增的交互契约，验证结果与产物见本节末尾。旧的 P5 机器证据只覆盖各自归档的源码与二进制；本轮自动化、七版本真实实例和目标终端状态分别记录，不以旧产物或PTY替代实机验收。

### 章节分页与语义样式

所有已有分组的可读 JSON／详情复用同一种醒目章节标题和分页行为。当前屏幕能容纳整组时显示完整组；只在该组高于可用视口时允许组内滚动。PageUp／PageDown 移至相邻顶层组，Tab／Shift+Tab 可替代切换；方向键与 Home／End 只滚动当前超长组。嵌套章节随父组整体显示，不拆散。左右及 h／l 留给候选项的备选值，不作为章节切换键。写入预览仍按完整计划确认，翻阅某组不会改变写入目标。界面持续说明确认涵盖完整计划；预览超过显示上限则阻断确认，不能对省略后的部分预览提交写入。

分组元数据由本地结果格式化器产生，远端文字不能伪造章节标题或样式角色。Run with options · filters and advanced fields 固定为紫色粗体；章节为青绿色粗体，主要操作蓝色粗体，辅助说明灰色弱化，返回操作橙色粗体。--color=never、NO_COLOR 或无颜色能力时，保留清楚的文本和主次关系；JSON、原始 API、Git 及 MCP 协议流不附加终端样式。

### Browse account

Home 新增 Browse account，按当前已验证身份浏览 /user/repos 返回的账户仓库和 /user/orgs 返回的组织成员列表；页面名称使用 Account repositories，不宣称覆盖实例全部仓库。进入组织后使用 /orgs/{org}/repos 获取其仓库。每页读取 30 项，筛选只针对当前页；稳定组织／仓库 ID 在进入下一层或工作台前重新核对。返回账户页保留页码、当前页筛选和焦点。

选中仓库进入现有业务工作台，并固定使用已验证实例、账号和远端仓库。工作台不继承打开 sotea 时的本地 Git 仓库，不提供本地 Git 操作，不克隆、不更改 remote，也不保存默认配置。每次请求前重读配置并核对账户浏览作用域；实例、身份或配置修订变化后中止后续查询，要求返回 Home 重新进入。真实 Gitea 版本及目标终端验收仍需按开发计划完成。

### 本轮验证与产物（2026-11-07）

全量 `go test -race ./...`、`go vet ./...`、三个目标构建以及VT／静态完整终端会话通过。P2隐藏输入、P3／P4选择器回归保持通过；新增真实PTY覆盖语义色与NO_COLOR、PgUp／PgDn和Tab／Shift+Tab切组、左右键不切组、长组滚动／resize、账户分页／筛选、组织仓库、原上下文恢复、本地Git隔离和外部配置变更阻断。

同一最终macOS arm64产物在七个指定Gitea版本完成账户仓库与组织仓库工作台、现有Issue／Release菜单链路及精确资源清理。详见 [本轮机器证据](validation/p5-account-browser-result.json)。二进制为 `dist/p5/darwin-arm64/sotea`，SHA256：`45918a3d0febd7474e0da48247b10e66d784c9334f5e2263fb26d2c45ddc9540`。

本项目测试服务已停止、七个数据卷保留，1.22.6驻留镜像／容器已恢复；28.3.0保存在已核对摘要的主机归档中。旧记录保留，Windows、Terminal／iTerm2 GUI、Intel／最低macOS仍待实机验收。

## 10. 全局布局、Markdown、账号向导与草稿徽标

全部持续页面复用页框、留白、标题／说明层级和固定操作区。菜单名称加粗，说明在下一行，选项之间留间隔；小窗口压缩密度以保持动作可达。主要操作蓝色粗体、辅助说明可读灰色、返回橙色、高级选项紫色、章节青绿色；关闭颜色仍保留层级。错误／警告／成功卡片分别显示原因、错误码、Next step和恢复动作，不自动重放写入。

正文采用goldmark v1.8.7解析CommonMark／GFM，支持标题、强调、列表／任务、引用、代码、链接和表格，按当前组及窗口宽度重排。链接显示地址、图片只显示说明、HTML为惰性文本；实体解码前后过滤控制符，只输出固定SGR。--json、直接API、Git和MCP协议不进入此渲染。Release选择器和摘要表仅在draft=true时显示标签左侧的橙色粗体◇ Draft；已发布版本没有状态徽标，详情／机器数据仍保留完整字段。

Account提供Connect account、Connection status、Switch account、Update credentials和Disconnect account。连接按完整实例URL→可选预期账号→凭证来源／隐藏输入→验证身份与明确保存四步进行，保存页默认Back，Esc回上一步。预览不落盘，最终保存前重新核对同一身份；配置变化在凭证请求前阻断并允许重新开始。Browse account未绑定可进入向导，保存后继续浏览。输入框支持左右编辑、Home／End、删除和Ctrl+U；密钥不回显也不暴露长度。

本轮全量race、vet、三个目标构建、同产物VT／静态会话、P2隐藏输入、P3／P4选择器及新向导／浏览回归通过；包括取消零保存、预览不保存、身份变化拒绝、配置阻断／重新开始、窄窗口输入、控制符和机器流隔离。合成PTY的账号输入／恢复／保存、Release菜单／选择／Markdown画面已目视检查，不代表GUI宿主验收。

本轮未改变API路径、字段、认证协议或兼容策略。按用户新要求，不继续逐版本重复Gitea验证；调整前完成的1.27.3／1.26.4／1.25.5／1.24.7冒烟单独保留，1.23.8可选回归按范围调整中止并核对无随机资源／token遗留，1.22.6和28.3.0不重跑。此前七版本证据保留，首次交付／正式发布门槛不删除。

当前二进制为 `dist/p5/darwin-arm64/sotea`，SHA256：`f959ef5b5839017e00cc1f34b2115a5cfc92e36f32b7c5a240f663f8ee79127f`。源码、脚本、产物和结果见 [本轮证据](validation/p5-guided-ui-result.json)。项目服务停止、七卷保留，本轮未轮换镜像。Windows、Terminal／iTerm2 GUI、Intel／最低macOS仍待实测。

## 11. 菜单选项说明补齐

Home和远端仓库工作台复用同一入口说明。补齐Branches、Tags、Commits、Labels、Milestones、Account、Instance、Git、API、Version，以及保存默认项、刷新、退出／返回仓库列表的英文description。命令生成菜单分开传递名称和说明，保持名称加粗、说明在下一行的统一层级；原有Issue／PR／Release说明继续保留。

账户浏览的翻页／刷新／返回、Context检查、Git参数输入、字段继续／布尔值／手动输入、账号取消／断开／继续和PR审查等需要解释的选项同时补齐说明。稳定动作ID及原执行行为不变，远端资源无简介时不编造说明。该增量仅改变展示，按受影响包、终端导航和构建验证，不重跑Gitea多版本矩阵。

本轮cli／tui包测试、静态检查、三目标构建和隔离PTY检查通过；验证Home新增说明、Context检查说明、浏览返回、字段继续与布尔值说明，以及原导航／退出和终端恢复，配置与业务均零写入。当前macOS arm64产物SHA256：`ad03aabd44f3e6adf4f0418ccab8127b6c032f8f3f72312d84a2d696caa800da`，路径仍为`dist/p5/darwin-arm64/sotea`；见 [本轮证据](validation/p5-description-result.json)。GUI／Windows等实机状态保持待实测。

## 12. 分组名称标签栏

详情与确认页在正文上方显示组名标签栏，例如 `Overview │ Activity │ Links │ Access │ [Additional details]`。当前标签为章节强调色及粗体，其他标签为辅助色；不显示Group N of M数字提示，关闭颜色仍以方括号识别当前组。

窄屏保留当前标签并显示能容纳的相邻组名，省略号提示还有其他组；超长当前名称仅截断标签，正文内容不删减。PgUp／PgDn和Tab／Shift+Tab切换，方向键／Home／End只在当前长组内滚动，左右键仍保留给备选值。单组也用名称标签呈现，但不提示无效的多组切换。写入确认仍涵盖完整计划。

本轮受影响包测试、vet、三目标构建与真实PTY验证通过；覆盖PgUp／PgDn、Tab／Shift+Tab、左右键保留、长正文、resize、NO_COLOR和活动标签可见性。五组Repository画面已目视检查；未重跑Gitea多版本。见 [本轮证据](validation/p5-group-tabs-result.json)。当前macOS arm64产物路径`dist/p5/darwin-arm64/sotea`，SHA256：`051643528e5182254836b118beebd5b09e9a304d4408caf14c4c06d11c4b0857`；GUI／Windows实测状态不变。

## 13. 成功结果、详情层级与底部位置修订

本节替代第12节标签外观：分组当前项改为粗体背景块，两侧留白，其他名称以间隙分隔，不用竖线或方括号。无颜色时用圆点标记当前组。原切组键位与整组正文、窄屏活动项优先保持。

成功结果去掉release view · exit 0 · success及0.6.3: success等冗余摘要，目标只作为独立资源标题，正文在空行后开始。字段名用可读灰色，值保留默认前景；正文、复杂字段和顶层章节分别有层级与留白。类型化失败、unknown、partial等信息仍显示，不因美化改成成功，直接CLI／静态流与机器契约保持原有语义。

当前位置在底部按键上方用绿色普通字重显示，例如Home › Releases › View › Body，随菜单和当前组变化。顶部保留实例／仓库以及必要失败／确认信息，成功操作路径不再与资源标题争夺层级。仅展示修改，不重复Gitea多版本矩阵。

本轮output／tui／cli测试、vet、三目标构建和同产物VT／静态完整会话通过，包含默认No、unknown不重放、输入／确认、Git原流、目录隔离和取消恢复；账号向导与Markdown回归保持。新增状态边界回归确保隐藏成功摘要不会隐藏unknown／partial，真实PTY及合成画面核对Release键值层级、绿色普通字重底部路径与五组背景标签。见 [本轮证据](validation/p5-result-layout-result.json)。当前`dist/p5/darwin-arm64/sotea`的SHA256：`777039a0c7bcf71bd6e5bd35824b6b758c67205bc1400fb5a39cb1871e37719e`；真实GUI／Windows等验收仍待完成。
