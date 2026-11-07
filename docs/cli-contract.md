# sotea 首版运行与认证契约

> 状态：P2 至 P5 业务 CLI、上下文、认证、持续菜单和终端输出已实现；MCP 为诊断原型，完整工具与目标宿主验收待推进
>
> 更新日期：2026-11-07
>
> 依据：[requirements.md](requirements.md)、[technical-design.md](technical-design.md)

## 1. 三种入口

程序界面统一使用英文，包括帮助、参数说明、交互菜单、输入提示、确认、表格标题、错误与诊断，以及 MCP 用户可见摘要。远端标题、正文、评论等用户内容和 Git／通用 API 原始输出保留原文；中文项目文档不受此规则影响。后续新增入口同样遵守该约定。

| 入口 | 首版行为 |
| --- | --- |
| 交互终端执行无参数 sotea | 从启动目录识别仓库，进入持续菜单；提供首次连接、账号、Issue、PR、Release、Git 和上下文切换入口。 |
| sotea 加子命令 | 参数完整时按命令执行；必需参数缺失且有交互终端时补全，写操作确认后执行。 |
| sotea mcp serve | 本地 stdio 服务；客户端启动和管理进程，按 MCP 参数调用共享应用服务，不显示菜单。 |

菜单按 Issue、PR、Release 的全部首版命令提供入口；完成操作后回到对应列表或菜单。列表和结果显示当前实例、账号及仓库。Git 菜单提供显式参数输入的系统 Git 入口，执行前暂停持续菜单并交接真实终端，Git 结束后恢复；保留 Git 自身的交互。

无参数且 stdin、stdout、stderr 任一不是终端时显示帮助并成功退出。CI、--no-interactive 和 --json 禁止交互；缺少必需参数时返回参数错误，不猜测目标。--help 总是显示帮助。--yes 仅跳过确认，不填写必需参数或勾选目标。

显式 --json 且没有子命令时返回结构化参数错误并以 2 退出，不能混入纯文本帮助；--help 是独立的显式帮助模式，优先显示帮助文本，不执行业务命令。

## 2. 默认工作目录与仓库上下文

工作目录使用启动时的当前目录；可用 --workdir 显式覆盖。用系统 Git 发现仓库，支持仓库子目录和 worktree，不要求 .git 为目录；Git 不可用时仍可通过显式实例和仓库使用 API。

按维度解析上下文，先解析目标，再选凭证：

| 维度 | 优先级和规则 |
| --- | --- |
| remote | --remote > 已保存的本地仓库 remote 绑定 > 唯一 remote。多个 remote 且没有绑定时让终端用户选择，非交互时报歧义；不默认将 origin 当作目标。 |
| 仓库 | --repo owner/repo > 所选 remote 的仓库映射 > 已保存的本地绑定；无候选时交互输入或在非交互时报错。显式仓库优先，显示其来源。 |
| 实例 | --instance > SOTEA_INSTANCE > 该本地仓库的显式绑定 > 所选 remote 唯一匹配的实例；仅在没有 remote 和绑定可用时使用全局默认实例。 |
| 账号 | --account > SOTEA_ACCOUNT > 当前实例保存的活动账号 > 当前实例唯一账号；多个账号无活动项时要求选择或显式指定。 |

显式实例与 remote 指向不同主机时不悄悄替换：显式 --instance 和 --repo 同时齐全可跨仓库操作；缺少明确仓库且存在矛盾时返回上下文冲突。fork 不自动切换上游，用户选择的 remote 决定推断目标。菜单切换只改变当前会话，保存为默认需明确选择。

HTTPS remote 可提出去掉仓库路径后的服务根地址候选；保留非标准端口和可能的代理子路径，不从仓库名判断服务类型。SSH 主机别名、SSH 端口和 HTTP 端口不等价：先查保存的映射，无映射时要求输入或选择 HTTP 根地址。保存过的绑定优先于推测。

## 3. 判断是否为 Gitea

首次对未登记的地址先进行不携带凭证的有限请求，访问根地址下的 /api/v1/version；解析 version 后，再用仓库 API 验证 owner/repo。不能只用域名、Git 协议、首页标题或版本字符串证明平台身份。

认证代理、401 / 403、404 或网络错误表示待配置或未知，不能直接判断为非 Gitea。返回类似 Gitea 的版本响应也可能来自兼容实现；首次建立实例时显示候选地址和检测证据，让用户确认将其作为 Gitea 实例使用。显然返回其他平台的内容时不自动登录，要求修正地址。

私有实例可先确认根地址并提供凭证，再重试版本和身份检测；私有 CA 在检测前配置，不能自动禁用 TLS。登录成功通过 GET /api/v1/user 获取真实账号，再查询仓库；不使用 Git user.name 或 user.email 充当远端身份。检测不保证识别全部兼容分支，首版支持承诺仍针对七个指定 Gitea 版本。

## 4. 首次使用与认证

流程：安装并配置 PATH → 在目录启动 → 发现仓库及 remote → 确认服务根地址与平台 → 选择账号及凭证来源 → 验证 GET /user → 验证仓库 → 保存非敏感绑定 → 进入菜单或继续命令。没有仓库时允许先配置实例和登录，再显式选择或输入仓库，不自动 clone。

Git fetch / push 等继续使用系统 Git 已配置的凭证辅助程序和 SSH；sotea 的 API 身份复用流程采用以下顺序：

1. 显式配置的环境凭证优先，其次当前实例和账号的系统安全存储凭证，再尝试当前 HTTPS remote 对应的 Git credential helper。环境变量 SOTEA_TOKEN 只绑定本次选定实例，不遍历其他实例使用；可用 SOTEA_TOKEN_INSTANCE 明确限制实例，设置后不匹配必须拒绝。
2. 查询 Git 凭证时调用 git credential fill，使用所选 HTTPS remote 的凭证上下文：协议、主机及显式端口、已知凭证用户名和原始仓库路径，路径是否参与辅助程序匹配由 Git 的 credential.useHttpPath 等配置决定，详见第 4.3 节。捕获辅助程序输出并脱敏，设置超时；自动检测、脚本和 MCP 禁止提示，只有显式登录的交互流程允许用户解锁或输入。
3. Git 辅助程序返回的 password 不一定是 API token。仅使用来源明确的认证类型验证：普通 HTTPS 用户名密码走 BasicAuth，用户选择 token 类型时走 token 认证；不在无证据情况下遍历尝试认证方案。GET /user 成功后才关联账号，403 不作为密码失效证据。
4. 没有可复用凭证、API 验证失败或使用 SSH remote 时，交互提示使用独立 API token，可从实例的个人设置创建后安全输入。Git 的 SSH 登录不自动产生可调用 API 的 token；当前文档提及的 SSH HTTP 签名属于独立认证机制，首版不将其作为七版本通用自动登录路径。
5. API token 验证后写入系统安全存储，配置只记录引用。需要让 HTTPS Git 同时使用时，在用户明确选择后再调用 credential approve；不得默认覆盖已有 Git 凭证。若已有辅助程序不能持久化，明确反馈；API 不静默退回明文文件。Git 未安装不阻止显式 token 登录。

首版不自动创建或撤销服务端 token；不实现浏览器 OAuth 兑换或自动续期。账号密码、2FA 或组织策略不能按支持路径完成时，引导使用具备对应权限的 token，不假设所有 Git 凭证均能用于 API。

显式要求 API 账号 A，但 GET /api/v1/user 验证为账号 B 时，默认返回 AUTH_IDENTITY_MISMATCH，禁止继续业务请求、覆盖 A 的绑定或以 B 执行写入。交互式登录可以展示实际身份，只有用户明确同意切换到 B 后才按 B 保存并继续；非交互、--yes 和 MCP 均不能自动接受身份替换。比较对象是已解析的 API 账号和远端实际身份，不能把 helper 的 oauth2 等占位用户名直接当成 API 账号。

已登记实例的日常操作可自动尝试该账号保存的凭证，但必须遵守下述退出抑制规则；首次未知服务地址必须先确认来源，再读取和发送对应凭证。匿名用户可读取公开资源，所有业务写入及账号管理必须有经验证的身份；不为公开只读操作强制登录。

### 4.1 认证命令

| 命令 | 行为 |
| --- | --- |
| auth login | 推断或选择实例，选择 git / token 来源，输入必要信息，验证后保存账号；支持 --instance、--account、--credential-source 和 token 类型的 --with-token stdin 输入。 |
| auth status | 显示凭证来源及上次验证状态；--check 调用 GET /user，失败返回对应错误，不展示密钥。 |
| auth switch | 选择已保存账号并验证，成功后设为实例活动账号，令该进程相关缓存失效。 |
| auth refresh | 重新获取当前来源凭证或安全输入替换 token，验证成功再替换旧引用；失败保留旧记录并标明其验证状态。 |
| auth logout | 确认后删除 sotea 的该账号引用和独立存储密钥，清理活动身份、认证绑定及缓存，保存非敏感退出抑制标记；不撤销服务端 token，也不默认删除其他工具共享的 Git 凭证。 |
| auth logout --forget-git | 显示实际 Git helper 匹配范围并额外确认后调用 reject；可能影响 Git 和其他工具，无法保证只影响当前仓库。 |

非交互登录必须显式指定必要信息，密钥用环境或 --with-token 提供；MCP 不使用 stdin 登录。账号切换和退出只改变 sotea 的活动身份；Git 的 commit 作者、SSH key 及 Git 自身账号选择仍由 Git 配置决定，界面须说明当前凭证来源。

退出抑制标记按完整实例根地址和已验证账号持久化，跨进程生效。退出后不得在下次业务操作中通过 Git helper 自动重新登录该账号；退出导致实例没有活动账号时，也不能借助“唯一账号”或未限定账号的 helper 查询绕回原身份。公开读取可以匿名继续，需要认证时返回 AUTH_REQUIRED 并引导显式 auth login；不自动切换到另一个保存的账号。显式登录并验证成功后才清除对应标记；用户仍可通过 auth switch 明确选择另一个已保存账号。退出不删除仓库到实例的非敏感映射。

环境凭证属于外部管理，sotea 无法修改父 shell 或 MCP 启动配置中的 SOTEA_TOKEN。退出结果必须分别说明本地清理结果和仍存在的环境来源；auth status 也应展示该来源，不能声称 token 已删除、撤销或所有入口均已退出。显式提供的环境凭证仍遵循来源优先级，退出抑制只阻止隐式 helper 重登；要停止环境认证，用户需移除 shell／客户端配置中的变量，并重启继承旧环境的 MCP 子进程。输出不得包含变量值。退出、切换或替换凭证后，长驻进程需使相关缓存和未提交写入计划失效。

受控 API 身份请求返回 401 时标记失效，交互可重新登录，非交互返回 AUTH_REQUIRED；403 返回 PERMISSION_DENIED，不自动删除或 reject。网络、TLS、404 等不能直接判定 token 失效。长驻菜单和 MCP 每次调用检查配置版本，凭证或账号发生变化则失效相关计划和缓存。

### 4.2 配置

首版非敏感配置使用 JSON，默认位置为 os.UserConfigDir()/sotea/config.json，可用 --config 或 SOTEA_CONFIG 覆盖。密钥由 macOS Keychain / Windows Credential Manager 适配器保管，具体依赖实现时选择；配置使用格式版本、原子写和并发修改检查。默认不从仓库中自动加载可执行配置。

实例记录包括名称、完整 URL、CA 路径、SSH 映射、账号引用及非敏感退出抑制标记；本地仓库绑定记录规范化路径、remote 和实例，跨设备路径不自动迁移。按配置项采用显式参数 > 对应环境变量 > 已保存配置 > 默认值；上下文及凭证项的特殊顺序以上表和第 4 节为准。

P2 已将 --config 接入当前命令，按显式路径 > SOTEA_CONFIG > 默认路径解析。文件上限 1 MiB，未知字段／格式版本、损坏 JSON 均报错。保存使用排他 .lock、完整内容摘要比较、同目录临时文件写入同步和替换，成功递增 revision；遗留锁不自动删除。锁仅协调遵守协议的 sotea 写入，外部编辑器在比较后的修改仍可能竞争；Snapshot.RevisionKey 供长驻进程重新读取后检测内容变化。

### 4.3 Git 凭证匹配与复用范围

HTTPS Git 凭证由 URL 上下文描述，通常以协议和主机为基础，并受用户名、端口、路径配置及辅助程序行为影响，不能简单写成“只绑定域名”或“每个仓库单独登录”。sotea 不自行扫描系统密钥库或直接运行某个 helper；在目标仓库的 Git 配置上下文中调用 git credential fill，让 Git 应用用户既有的配置、辅助程序链和匹配逻辑。

| 上下文字段 | 首版规则 |
| --- | --- |
| protocol | 使用 remote 的协议，例如 https；不将 http 与 https 自动视为同一来源。 |
| host | 保留主机及 URL 中显式指定的端口，例如 git.example.com:8443。默认不扩展为父域、其他子域或其他端口；用户显式配置的 Git URL 匹配规则仍由 Git 应用。 |
| username | 已知凭证用户名时传入，让 Git/helper 选择相应凭证；缺少时允许 helper 返回用户名，但须验证实际 API 身份。不能用 commit 作者信息代替。 |
| path | 传入所选 Git remote 的仓库路径。HTTP(S) 默认不按路径区分外部 helper 中的凭证；credential.useHttpPath=true 时才让路径参与该匹配。sotea 不强行设置此选项。 |

以下为示例，不代表已部署服务：

| remote 对比 | 预期处理 |
| --- | --- |
| https://git.example.com/team/a.git 与 https://git.example.com/team/b.git | 默认可由 Git 返回相同凭证；useHttpPath=true 时按各自仓库路径查询，不能自行去掉路径再扩大搜索。 |
| https://git.example.com 与 https://other.example.com | 默认主机不同，不因同属 example.com 自动复用；按用户已有的 Git 配置执行。 |
| https://git.example.com:8443 与 https://git.example.com:9443 | 使用各自含端口的 host 查询，不删除端口合并凭证。 |
| http://git.example.com 与 https://git.example.com | 使用各自协议上下文，不自动跨协议尝试。 |
| 同一 host 的不同账号 | 需要明确账号与已验证的凭证用户名映射；不枚举并轮流尝试该主机所有密钥。 |

需要区分三类绑定：本地仓库到 remote 的绑定用于确定操作对象；Git 凭证上下文用于向 helper 查询；sotea API 凭证引用按完整实例根地址（含代理子路径）和已验证 API 账号绑定。同一实例的多个仓库可以使用同一账号和凭证引用，不创建每仓库一份 token。不同实例根地址不因主机相同就自动共享 API 凭证。

复用时保留原始 Git remote 的 credential 上下文，不用 /api/v1/user 或 API 请求路径替换仓库 path。先确认该 remote 和 API 根地址的映射；只有映射到已选实例且来源已确认时，才用 helper 返回的凭证验证该 API。无法映射或没有 Git remote 时，不猜测仓库路径读取 path 限定凭证，转用已保存 API 身份或显式登录。

Git 辅助程序的 username 可能是认证用占位名称，与 GET /api/v1/user 返回的实际 login 不相同；分别保存 credential_username 与已验证 API 身份的映射，不改写共享 Git 记录以强行对齐。取得凭证后仍需验证身份和权限，Git clone / push 成功不等于具备对应 API scope。

approve / reject 使用 Git 返回的有效上下文及实际匹配范围。useHttpPath=false 时，一个 erase 操作可能影响同主机其他仓库；退出提示必须展示此范围，不承诺“只清理当前仓库”。helper 来源默认保存引用和上下文，不复制为长期独立 token；若用户选择另存为 sotea 凭证，单独确认并说明之后两份记录的更新／删除独立。

SSH remote 的 key、agent 和主机别名不按上述 HTTP 凭证模式读取；Git 继续处理 SSH，API 使用明确的 HTTP 实例映射和独立可验证凭证。所有查询都设置超时、捕获密钥输出，不在 CLI 日志、JSON 或 MCP 响应回传原始 fill 结果。

验收补充：用隔离的模拟 helper 验证同主机多仓库、不同协议／端口／子域、多个账号、useHttpPath 开／关、仓库级 Git 配置、代理子路径、来源身份不一致及 reject 影响范围。另验证退出后重启仍不隐式重登、显式登录恢复、其他账号不会自动接替、环境来源提示、占位用户名与 API 身份区分、A/B 不一致时业务请求数为零，以及 MCP 计划失效。读取和测试不得访问或输出开发者实际凭证。依据见第 8 节的 Git 官方文档。

## 5. 目标多选规则

下表为首版决定。多目标操作逐项请求，初版串行执行；最多 100 个目标，超限报错并要求分批。多值字段仍只修改单个资源。仅删除个别负责人或标签，不能将移除实现成清空整组。

| 命令组 | 资源目标数量 | 缺少目标时 | 写入确认 |
| --- | --- | --- | --- |
| Issue close / reopen | 1–100 个 Issue | close 从 open 中多选；reopen 从 closed 中多选 | 汇总目标后确认，包括直接传编号。 |
| PR close / reopen | 1–100 个 PR | 多选相应状态的未合并 PR | 汇总目标后确认，已合并 PR 不能重新打开。 |
| Issue / PR view、comments、files、commits、review list | 单个 | 单选对应资源 | 只读无确认。 |
| Issue / PR create | 创建一个 | 表单补齐必需输入 | 展示创建内容后确认。 |
| Issue / PR edit、comment add、PR review create | 单个 | 单选资源并填写修改或评论 | 展示实际目标及内容后确认。 |
| Issue assignee / label add、remove；PR reviewer add | 单个 Issue 或 PR，多名用户／标签 | 先单选目标，再多选值或显式输入 | 展示增量变更后确认。 |
| PR merge | 单个 PR | 从未合并 open PR 单选 | 必须展示 base、head SHA、方式、删除分支选项后确认。 |
| Release view / edit / asset list / upload | 单个 Release | 单选已有 Release | edit / upload 确认；upload 初版一个文件一次。 |
| Release create | 创建一个 | 选择或输入一个标签，补齐信息 | 展示标签、目标提交、草稿及预发布状态后确认。 |
| repo view / commit status | 单个仓库／引用 | 选择或输入；commit status 未指定 REF 时使用已验证的当前 HEAD，无本地 HEAD 则提示输入 | 只读无确认。 |
| 各 list 命令 | 查询一个上下文 | 默认直接显示分页列表，菜单中可继续浏览 | 只读无确认。 |

直接指定 Issue 编号也必须读回验证资源类型，不能因 Issue 与 PR 共用编号和部分 API 而误改 PR。已处于目标状态的资源报告 skipped，不重复写入；查询不到、权限失败或状态不允许转换报告失败。

### 5.1 参数基线

公共参数包括 --instance、--account、--repo、--remote、--workdir、--config、--json、--no-interactive、--yes 和 --color；它们按前文作用，不能出现相互矛盾的值。选择器的搜索和多选按 TDD，不另设“默认选第一项”。以位置参数提供编号或标签，单目标命令收到多个目标直接报参数错误。

| 命令类别 | 首版参数及默认值 |
| --- | --- |
| 列表 | --page 默认 1、--limit 默认 30 且最多 100；--all 显式拉取后续页，默认上限 10000 项。超过上限须报错并标记不完整，不能显示已读部分为完整结果。 |
| Issue list | --state open / closed / all，默认 open；--search、可重复 --label、--assignee、--milestone。 |
| PR list | --state open / closed / all，默认 open；--search、--base、--author。 |
| Issue / PR create、edit | --title、--body 或 --body-file；可重复 --assignee、--label，--milestone。创建标题必需，正文可为空；编辑至少有一项显式变更。PR 创建还需 --base 和 --head，可重复 --reviewer／--team-reviewer；编辑只接受 --base，不提供 head 改写。 |
| Issue assignee / label add、remove | 位置参数 index；分别用可重复 --user 或 --label 提供值，至少一个。标签名称必须解析为唯一 ID，有歧义报错；不模糊猜测。 |
| Issue / PR comment add | index，--body 或 --body-file 至少一个且内容非空。 |
| PR reviewer add | index，可重复 --user 和 --team，合计至少一个审查者。 |
| PR review create | index、--event approve / comment / request-changes；评论及请求修改需非空正文，approve 正文可选；可用 --commit 显式指定完整远端 SHA；--review-id 提交已有 PENDING 审查，核对所有权与当前 head，旧 head 的 pending 审查不提交。 |
| PR merge | index、--method 默认 merge、--delete-branch 默认 false；候选方式只提供实例及仓库允许且已经跨版本验证的方式。 |
| Release create、edit | tag、--title、--body / --notes-file、--target、--draft / --publish、--prerelease；create 默认草稿和非预发布，edit 不传字段即不改变原值，--draft 与 --publish 互斥。 |
| Release asset upload | tag、file，单个普通文件；上传名称默认文件名，可用 --name 指定。 |
| commit status | 可选位置参数 ref；本地 HEAD 候选在调用前解析为明确提交，无法解析时报错或交互输入。 |

create 的负责人／标签是初始集合；edit 的 --assignee 和 --label 表示明确替换集合，在预览中显示完整变化。增量修改用相应 add / remove 命令，不能混用语义。编辑清空使用 --clear-body、--clear-milestone、--clear-assignees、--clear-labels，和同字段赋值互斥。PR 关闭／重开不接收正文等编辑参数，避免批量修改隐藏字段。

未被所有目标版本支持的筛选条件可按 TDD 第 7.3 节在完整有界查询后本地计算；PR 搜索不能仅筛当前页却显示为整个仓库搜索。排序保留服务器结果顺序，不宣称跨版本顺序一致。

Issue 的文本搜索统一为标题／正文中不区分大小写的子串匹配，在完整有界查询集上计算，避免异步索引漏掉刚创建的数据；多个 --label 采用任一匹配。多标签、包含逗号的标签名及未知版本按同一有界扫描规则处理，再执行筛选和分页，超过 10000 项报 FILTER_LIMIT，不返回伪完整结果。

Issue 创建后按返回编号读回全部显式字段。确认已创建但字段未被保留时返回 partial／退出 3，保留已创建编号和逐步结果，提示检查权限；读回失败同样保留创建成功与核对未知的步骤。不得为补齐字段自动重建 Issue。

## 6. 写入和结果契约

### 6.1 确认与参数

所有业务写入在终端中预览并默认回答 No，包括直接传编号。--yes 表示用户明确跳过本次确认；在 --json、--no-interactive、CI、无终端模式下，写入必须参数完整并提供 --yes，否则不执行。

通用 API 的 GET / HEAD / OPTIONS 按读取方式执行，其他方法进入同一确认规则；这只是客户端分类，不保证服务器给任何方法定义的行为。原始请求内容保持用户提供的含义，不替换字段或重放写入。Git 透传保留 Git 自身语义与确认，不通过字符串分析决定是否加额外提示。

正文支持 --body 或 --body-file；Release 说明支持 --notes-file，互斥来源冲突报参数错误。空字段默认不代表清空，编辑使用明确清空选项，例如 --clear-body、--clear-milestone，不能同时设置和清空同一字段。选项 Schema 要覆盖文档中已有的标题、正文、状态、负责人、标签、里程碑、分支、审查类型及发布选项。

批量先完成参数、上下文和目标查询校验，预览明确每项类型和状态，再确认执行；批内目标去重而保留首次出现顺序。确认后个别目标冲突／无权等失败继续处理其余目标；账号失效、整体网络不可达等系统性错误停止尚未开始的项。用户取消随时停止未开始操作，按下节返回实际已完成及未执行项。

### 6.2 JSON 和退出码

业务命令 --json 返回 {schema_version, status, context, items, error}；schema_version 首版为 1。context 含实例别名、仓库和账号标识，不含密钥；items 每项含 target、status、data 或 error。status 使用 success / partial / failed / cancelled / unknown；逐项另允许 skipped、not_executed。只跳过已达目标状态的项目仍可整体成功。读取列表 data 含 items、page、limit、has_more，不能仅凭当前页数量宣称总数。

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功；或所有目标已达期望状态。 |
| 1 | 认证、权限、网络、服务端或其他操作失败，且无成功项。 |
| 2 | 参数、配置、上下文歧义、未确认或无法交互。 |
| 3 | 批量部分完成，仍有失败、未知或未执行项。 |
| 4 | 没有部分成功且写入结果未知，需先查询状态。 |
| 130 | 用户取消或中断；若已提交也保留逐项结果，不宣称已回滚。 |

有部分完成且用户主动取消时以 130 优先；详情仍报告 partial/cancelled 项。单项目跳过等细分显示不得把未验证的结果称为成功。未知不是失败可重试的同义词。

直接执行 api 默认原始响应，HTTP 非成功使用非零退出码并保留状态，--json 的业务 envelope 不包裹通用 API 的原始 body；Git 正常退出保留原退出码。MCP 使用相同应用错误类别和逐项结果，不采用操作系统退出码表示每次工具失败。

## 7. PR 和 Release 默认流程

PR 创建以当前分支作为 head 候选、仓库默认分支作为 base 候选，显示后按明确参数或选择确定；跨仓库 head 使用 owner:branch 或 owner/repo:branch，核对实际可见 fork 关联和仓库 ID，支持改名 fork，不按同名仓库猜测，不自动 push 本地提交。PR 合并默认 merge；仓库不允许时展示合法方式供用户选择或要求 --method，不能偷偷改用 squash。删除源分支默认 false，force merge 不作为首版专用选项。检查由服务器最终裁决，合并前绑定已读 head SHA，变化时要求重新确认。

Release create 默认 draft=true、prerelease=false；--publish 明确直接发布，草稿可通过 edit 的 --publish 发布。选择已有标签则核对目标；新标签要求明确 --target 或交互选择并解析成提交，不由客户端猜测 main。创建标签和 Release 的服务端具体行为须逐版本测试；已有相同标签的 Release 报冲突，不覆盖。

附件上传前检查本地文件和已有附件；同名时报冲突，不自动删除覆盖。草稿的某个附件失败时保留草稿及成功附件，可继续补传；不自动发布或回滚已有资源。任何响应不确定时先查状态，禁止自动重复创建和上传。

## 8. 资料

- [Git credential](https://git-scm.com/docs/git-credential)：fill、approve 和 reject 的接口及影响范围。
- [Git credential contexts](https://git-scm.com/docs/gitcredentials)：协议、主机及 useHttpPath 的匹配规则。
- [Gitea API authentication](https://docs.gitea.com/development/api-usage/)：API 认证与 token。文档站描述不代表七个精确版本全部具备相同认证扩展，必须按隔离实例测试确认。

## 9. 当前可用入口（P5）

当前注册 version、help／补全、git、五类 auth、instance info、repo view、branch／tag／commit／label／milestone list、commit status、全部 Issue／PR／Release 命令、任意方法 api 和 mcp serve。交互三流无参数进入持续菜单，具体门槛见第 1 节；完整业务范围按前文保留。

公共选项已接入 --json、--workdir、--no-interactive、--instance、--account、--repo、--remote、--config、--yes，并增加 --ca-file、--proxy 和 --confirm-instance。首次来源确认不由 --yes 代替；首次公开仓库读取在来源确认及真实读取成功后只保存非敏感映射，不强制登录。--color=auto／always／never 与表格样式已接入。

Git 示例：sotea --workdir /path/to/repo git -- status。sotea 公共选项放在 git 之前；git 后全部参数原样交给系统 Git，可选的第一个 -- 仅作为分隔符移除。前置 --json 与 Git 透传不兼容；后置 --json 属于 Git 参数。--no-interactive 只禁止 sotea 提示，Git 透传仍遵守 Git 自己的交互配置。

auth login／refresh 的 token 入口包括安全终端或 --with-token；--credential-source 选 git／token／environment，Git 的 --auth-type 选 basic／token。--name 只给新实例命名；刷新必须指定已保存账号。auth logout --forget-git 的非交互额外确认使用 --confirm-forget-git，--yes 不能代替该共享凭证范围确认。

原始 api 提供任意方法、--query、--header、--include 以及第 10 节的请求体／上传／下载。版本入口未知或被拦截不会阻止明确 API 请求；未确认来源不得发送凭证。默认 stdout 为 body，--include 增加状态与头；--json 不包装原始响应，失败诊断仅走 stderr。认证／Host 相关头由上下文控制；非读取方法遵循确认契约。

mcp serve 当前只有 sotea_instance_info 原型，协商 2025-11-25／2025-06-18，协议 stdin 不参与登录或补参。其余工具及真实 Harness 验收仍按 P6／P7 实施。当前实现、依赖、边界和证据见 [P2 记录](p2-readonly.md)。


## 10. P3 通用 API 文件契约

任意方法均使用用户明确指定的路径／参数，不设置业务端点白名单。--data 提供原始 UTF-8 文本，--input 读取普通文件原始字节，--upload FIELD=FILE 可重复构造流式 multipart，三类来源互斥；--form NAME=VALUE 仅用于 multipart。请求体不按版本改写，写方法默认预览并确认，脚本必须 --yes。

默认原始响应上限 64 MiB，可用 --max-response-bytes 显式调整；--timeout 默认 30 秒，写入建议 60 秒、上传建议 300 秒，取消可提前结束。原始流不进入业务 JSON 或样式渲染。--output FILE 仅在 HTTP 成功且完整接收／同步／关闭后落盘，--output - 等于 stdout；非成功响应不创建目标文件。

写请求已收到 2xx 后，响应超限、响应读取／关闭失败或本地输出／文件提交失败均报告 PARTIAL_FAILURE／退出 3，提示先查询远端再决定后续操作；尚未取得响应的已发送写入仍按 RESULT_UNKNOWN／退出 4，主动取消仍以 130 优先。stdout 已流出的部分原始内容不会撤回或改写。

--body-file、--input 与 --upload 只接受普通文件，符号链接仅在实际目标为普通文件时可用。打开前后均核对类型；POSIX 使用非阻塞打开，防止检查后被替换为命名管道而等待写入者。目录、管道和设备拒绝输入，不通过后台无限等待读取特殊文件。

DEC-03 定案：显式下载永不覆盖已有目标，不提供隐式或强制覆盖选项。目标目录必须存在；同目录临时文件使用独立名称，接收／同步后采用不覆盖的硬链接提交，目标在传输期间被其他进程创建也返回冲突并保持其内容。失败、超限、取消清理临时文件，不留下伪完整目标。文件系统不支持所需提交语义时明确失败，不回退到可能覆盖的 rename；Windows／特殊文件系统仍需实测。

## 11. P4 实施约定

PR 关键词／base／author 筛选在完整有界集合上计算后分页，不依赖版本特有查询参数；默认不全量读取无筛选列表，超过 10000 项报 FILTER_LIMIT。Release list 支持 --search，按完整有界集合中的标签／标题子串匹配。

PR 创建／编辑通过共同目录解析标签和里程碑。1.22.6 的创建审查者为显式补充步骤；任何创建后失败保留 PR 和已完成步骤，不重新创建。旧版 PR PATCH 会忽略里程碑 0，清空通过已校验 PR 的共享 issues 资源独立完成，失败保留之前成功的编辑。普通 PR 讨论也使用共享评论资源，但不调用 Issue 命令或混淆类型。

所有合并请求绑定预览的 head SHA，默认不删除源分支；--delete-branch 显式请求时分别报告合并和实际删除结果。服务端错误不触发改字段、换方法、force 或重放合并。异步 mergeable 缓存和只读更新时间变化不单独要求重新确认，实际业务字段、状态及源目标 SHA 变化仍会停止写入。

Release create 默认草稿和非预发布，新标签必须 --target，目标解析为完整 SHA，提交前核对引用未移动。已有标签显式 target 必须一致，不自动移动标签。edit 的 --draft／--publish 互斥，--prerelease=false 明确取消预发布；未提供值不变。七版不能一致保证空字符串清空 Release 名称／说明，显式空编辑直接拒绝，不静默忽略。

release asset upload 一次只接受一个普通文件，默认文件名，可 --name 明确改名；默认 HTTP 预算 300 秒，主动取消可提前中止。准备后文件身份／大小／修改时间／SHA-256 任一变化都需重新确认。发送期间和发送后再次核验；未知结果先查附件，已创建附件后本地校验失败返回 partial，不补传、不发布、不回滚。附件名的预检查不是跨进程原子锁，不能保证其他进程同时上传时全局唯一。

当前专用命令及验收记录见 [P4 记录](p4-workflows.md)；MCP 完整工具和持续菜单仍按 P5／P6 实施。

## 12. P5 交互与终端实施契约

完整命令／缺参表、按键、会话及宿主记录见 [p5-terminal-experience.md](p5-terminal-experience.md)。菜单由实际注册命令树生成，动作使用独立参数容器和共享应用服务；MCP 不从持续终端菜单启动。

- VT 导航及单资源用 ↑↓ 聚焦、Enter 直接进入或接受；菜单无勾选框。只有批量资源使用 Space，Enter 提交选择集；/ 搜索、n／p 翻页、s 检查全部已选。Esc 返回直接上级，结果留屏可滚动，查看结束回同一业务菜单并恢复焦点。静态导航／单选输入编号直接进入，多选保留编号切换和 done。
- 持续菜单共用一个全屏和输入循环；只读命令默认直接展示，Run with options · filters and advanced fields 使用统一紫色语义样式并提供筛选和分页参数。启动及主页刷新先匿名探测版本，再仅对已登记地址验证身份和仓库；失败关闭仓库业务入口并提供登录、上下文和重试。检测失败不能断言不是 Gitea，未知来源不发凭证，空账号不显示为空字段。
- 首页 Browse account 使用当前已验证账号读取 /user/repos 与 /user/orgs；组织仓库通过 /orgs/{org}/repos 读取。每页请求 30 项，筛选只作用于当前页，Next／Previous 与刷新不隐去控制项。组织先按稳定组织 ID 重新核对；选仓库后再按稳定仓库 ID 和 full_name 核对，成功后进入现有仓库工作台。返回保留原页码、当前页筛选和焦点。接口范围按 API 对当前账号实际返回结果表述为 Account repositories，不称为实例全部仓库。
- Browse account 的仓库工作台绑定已验证实例与账号，使用显式远端仓库目标，不继承启动目录的本地 Git 仓库；不提供 Git 操作、不克隆、不修改 remote、不保存默认配置。每次查询前重读配置并核对实例、账号和配置修订；任一绑定变化即中止浏览，要求返回 Home 后重新进入。
- 结果详情中的顶层分组由可复用章节组件呈现。屏幕高度足够时完整展示当前组；组内容超出可视区域时只在该组内部滚动，章节之间用 PageUp／PageDown 切换，Tab／Shift+Tab 为替代键。↑／↓、Home／End 在当前长组中滚动；左右键及 h／l 保留给当前项的备选值，不用于切组。嵌套章节保持在所属顶层组内；写入计划与确认仍涵盖全部目标，不受可视分组影响。预览超过会话显示上限时明确报错并禁止在不完整预览上确认。
- 主要动作使用较醒目的颜色与粗体，辅助提示使用弱化颜色与字重，返回操作使用独立强调色；Run with options 为紫色，分组标题统一使用章节强调色。NO_COLOR、--color=never 或终端不支持样式时仍保留英文文字、层级和操作说明，不以颜色作为唯一含义。
- 字段页空行／Skip 不改变输入；清空须选 Clear 或适用的 clear 选项。冲突来源仍拒绝，不自动丢弃先前 flag。未选修改可返回。
- 上下文中明确补齐的维度保留到会话；推导来源每次重读。普通切换不保存，实例切换后要求重新提供仓库；Save defaults 独立预览、默认拒绝、快照冲突不覆盖。
- 空确认等同 No；最近类型化业务结果可回看，unknown／partial 不会被导航清成成功；Git 的非零码单独显示并返回菜单。
- --color=auto／always／never 管理自有样式，stdout／stderr 分别检测；非空 NO_COLOR 在 auto 下生效，显式值优先。JSON 及其预览、原始 API／Git／MCP 不加业务样式。
- 真实终端表格按完整字素宽度截断并标省略号，view／JSON 保留完整字段；管道保留完整列值。未知 TERM／dumb 使用静态输入，无 VT 全屏。

### P5 首页与可读结果修订

首页资料直接使用身份验证请求 GET /user 的 login、full_name 和可选 email，同一次响应通过账号一致性核对后才展示；不使用 Git 作者或配置中的名字冒充远端用户，不额外下载头像。每轮清除旧资料再验证，认证失败或来源未确认不能继续显示旧身份；实例已明确但尚无仓库时仍可展示有效用户资料。

普通业务文本不再输出 JSON 对象原文：资源列表保留摘要列，详情、写入预览、诊断及嵌套数据使用英文标签、缩进、章节和原文换行。Repository 使用 Overview／Activity／Links／Access／Additional details，未知字段不丢失；布尔值显示 Yes／No，null 为 Not set，空文本为 Empty，空集合明确提示。--json 字段名、类型、精度和状态契约不变。

分组结果的正文上方使用组名标签栏，当前标签使用粗体、背景色和两侧留白，其他标签弱化并以空隙分隔，不使用竖线、方括号或Group N of M数字。窄屏保留当前标签及可容纳的相邻标签，未显示的组用省略标记提示；关闭颜色以圆点标记活动标签。分组结果按顶层章节浏览，完整章节优先占据一页；仅当单组内容超出当前窗口时允许组内滚动。PageUp／PageDown 切换组，Tab／Shift+Tab 作替代键；方向键及 Home／End 在长组内滚动，左右键与 h／l 留给选项备选值。所有章节标题复用统一强调样式。主要动作、辅助说明、返回操作和 Run with options 分别有固定语义色与字重；颜色关闭或不可用时依靠文字标签和布局仍可辨别。

持续菜单中的 api 响应在不超过 2 MiB 且完整内容为合法 JSON 时使用完整字段树留屏展示，保留数组各项字段、大整数、HTTP 错误状态和脱敏边界；可选响应头也脱敏。非 JSON 或超过展示解析上限的响应交接原始流；显式文件下载不进行内容重写。直接 api、Git 与 MCP 继续遵守原流／协议约定。

### P5 账户目录浏览

首页 Browse account 先确认实例和当前 API 身份，再读取个人仓库列表和组织成员列表。个人仓库标题为 Account repositories，准确表示 /user/repos 对当前身份返回的范围；组织通过 /user/orgs 列出，进入组织前使用 /orgs/{org} 核对稳定 ID，再请求 /orgs/{org}/repos。目录每页 30 项；名称／说明筛选只覆盖当前页，不后台扫描其余结果。页码、筛选和稳定焦点在进入仓库工作台及返回后保留。

进入组织仓库前再次核对组织 ID；进入仓库前通过仓库详情核对 ID 与 full_name。工作台复用现有仓库业务入口，并使用固定的实例、账号和远端仓库。浏览上下文变化时停止发起后续请求并返回 Home 重新进入。此工作台不使用打开 sotea 时的本地 Git 仓库，不暴露 Git 操作，不克隆或更改 remote，也不将浏览目标写入默认配置。

### P5 全局布局、正文和账号向导

所有持续页面统一左右留白，正文可读宽度最多 104 列；品牌／目标、页面标题、内容和固定底部指引分区。Home、远端仓库工作台及各级菜单的操作选项提供简短英文description，解释用途、作用范围或执行后果；常规窗口放在名称下一行，不能仅有名称却留空说明区域。生成的命令菜单将名称与description分别传入，字段沿用实际参数说明，远端资源保留服务端内容，不为缺失的资源简介编造信息。常规窗口中选项名称粗体，说明单独一行并留白，低矮窗口压缩密度以保持动作可达。高级选项入口仍为紫色，错误／警告／成功采用固定语义色；关闭颜色后保留相同文字结构。错误页分开显示原因、错误码、Next step 与可选择的恢复动作；不会自动重放失败或结果未知的写入。

已知正文属性 body、description、notes、release_notes、content、readme 使用 CommonMark／GFM 渲染。支持标题、段落、强调、列表／任务、引用、代码、链接及表格；链接显示实际地址，图片只显示说明，HTML 为惰性文本，不输出远端 ANSI／OSC、不执行代码或加载网络资源。持续页面按当前宽度重排正文，沿用原分组切换和预览上限；--json、直接 api、Git 和 MCP 协议不进入 Markdown 渲染。Release 选择器与文本列表只对草稿显示标签左侧的 ◇ Draft，已发布项不附加状态；详情仍可查看真实字段，JSON 中 draft 保持布尔类型。

Account 菜单提供 Connect account、Connection status、Switch account、Update credentials、Disconnect account。连接分四步：完整实例 URL、可选预期 API 用户名、明确凭证来源与必要的隐藏输入、验证后的身份与显式 Save connection。保存页默认 Back；验证预览不写配置或密钥，保存前再次验证同一身份；来源未确认前不读取或发送凭证。输入框支持左右编辑、Home／End、删除和 Ctrl+U 清空。Esc 回上一步，离开向导恢复原会话，成功保存后选择新身份；Browse account 可在未绑定时引导并续接。

刷新使用已保存账号候选，不接受隐式替换身份；切换先验证；退出明确确认并说明保留共享 Git 凭证／外部环境变量。预览和保存前核对配置快照，外部修改时停止并允许从实例步骤重新开始；工作台内更改账号要求先返回 Home。直接 auth 子命令及静态终端保持已有参数、安全输入和确认契约。

### P5 结果标题、字段层级与底部位置

持续VT页面成功时不显示操作的exit 0／success摘要，也不为每个资源重复输出target: success。资源目标作为独立粗体标题，与下方正文保留空行；字段名使用辅助色，值使用默认前景，复杂字段和正文仍有章节标题／缩进。failed、partial、unknown、skipped、not_executed及取消状态继续明确显示，类型化最近结果不丢失。直接CLI、静态终端、JSON和原始流保持现有状态契约。

底部在按键指引上方显示当前位置，例如Home › Releases › View › Body，使用绿色普通字重，不用粗体、斜体或低对比淡化。菜单、表单、结果及资源选择均沿用此路径；活动分组名称随切换更新。颜色关闭时路径仍为可读文本。顶部保留实例／仓库与必要失败标题，成功结果的命令路径不再重复占据标题行。
