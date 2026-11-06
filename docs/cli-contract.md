# sotea 首版运行与认证契约

> 状态：按用户确认的首版范围制定，尚未实现
>
> 更新日期：2026-11-06
>
> 依据：[requirements.md](requirements.md)、[technical-design.md](technical-design.md)

## 1. 三种入口

| 入口 | 首版行为 |
| --- | --- |
| 交互终端执行无参数 sotea | 从启动目录识别仓库，进入持续菜单；提供首次连接、账号、Issue、PR、Release、Git 和上下文切换入口。 |
| sotea 加子命令 | 参数完整时按命令执行；必需参数缺失且有交互终端时补全，写操作确认后执行。 |
| sotea mcp serve | 本地 stdio 服务；客户端启动和管理进程，按 MCP 参数调用共享应用服务，不显示菜单。 |

菜单按 Issue、PR、Release 的全部首版命令提供入口；完成操作后回到对应列表或菜单。列表和结果显示当前实例、账号及仓库。Git 菜单提供显式参数输入的系统 Git 入口，执行前结束菜单渲染，Git 结束后恢复；保留 Git 自身的交互。

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
2. 查询 Git 凭证时调用 git credential fill，传递匹配的协议、主机、用户名和路径，尊重 Git 的匹配配置。捕获辅助程序输出并脱敏，设置超时；自动检测、脚本和 MCP 禁止提示，只有显式登录的交互流程允许用户解锁或输入。
3. Git 辅助程序返回的 password 不一定是 API token。仅使用来源明确的认证类型验证：普通 HTTPS 用户名密码走 BasicAuth，用户选择 token 类型时走 token 认证；不在无证据情况下遍历尝试认证方案。GET /user 成功后才关联账号，403 不作为密码失效证据。
4. 没有可复用凭证、API 验证失败或使用 SSH remote 时，交互提示使用独立 API token，可从实例的个人设置创建后安全输入。Git 的 SSH 登录不自动产生可调用 API 的 token；当前文档提及的 SSH HTTP 签名属于独立认证机制，首版不将其作为七版本通用自动登录路径。
5. API token 验证后写入系统安全存储，配置只记录引用。需要让 HTTPS Git 同时使用时，在用户明确选择后再调用 credential approve；不得默认覆盖已有 Git 凭证。若已有辅助程序不能持久化，明确反馈；API 不静默退回明文文件。Git 未安装不阻止显式 token 登录。

首版不自动创建或撤销服务端 token；不实现浏览器 OAuth 兑换或自动续期。账号密码、2FA 或组织策略不能按支持路径完成时，引导使用具备对应权限的 token，不假设所有 Git 凭证均能用于 API。

辅助程序返回的凭证若验证为另一个账号，显示实际身份，不能以用户输入的别名掩盖。已登记实例的日常操作可自动尝试该账号保存的凭证；首次未知服务地址必须先确认来源，再读取和发送对应凭证。匿名用户可读取公开资源，所有业务写入及账号管理必须有经验证的身份；不为公开只读操作强制登录。

### 4.1 认证命令

| 命令 | 行为 |
| --- | --- |
| auth login | 推断或选择实例，选择 git / token 来源，输入必要信息，验证后保存账号；支持 --instance、--account、--credential-source 和 token 类型的 --with-token stdin 输入。 |
| auth status | 显示凭证来源及上次验证状态；--check 调用 GET /user，失败返回对应错误，不展示密钥。 |
| auth switch | 选择已保存账号并验证，成功后设为实例活动账号，令该进程相关缓存失效。 |
| auth refresh | 重新获取当前来源凭证或安全输入替换 token，验证成功再替换旧引用；失败保留旧记录并标明其验证状态。 |
| auth logout | 确认后删除 sotea 的该账号引用和独立存储密钥，清理绑定及缓存；不撤销服务端 token，也不默认删除其他工具共享的 Git 凭证。 |
| auth logout --forget-git | 显示实际 Git helper 匹配范围并额外确认后调用 reject；可能影响 Git 和其他工具，无法保证只影响当前仓库。 |

非交互登录必须显式指定必要信息，密钥用环境或 --with-token 提供；MCP 不使用 stdin 登录。账号切换和退出只改变 sotea 的活动身份；Git 的 commit 作者、SSH key 及 Git 自身账号选择仍由 Git 配置决定，界面须说明当前凭证来源。

受控 API 身份请求返回 401 时标记失效，交互可重新登录，非交互返回 AUTH_REQUIRED；403 返回 PERMISSION_DENIED，不自动删除或 reject。网络、TLS、404 等不能直接判定 token 失效。长驻菜单和 MCP 每次调用检查配置版本，凭证或账号发生变化则失效相关计划和缓存。

### 4.2 配置

首版非敏感配置使用 JSON，默认位置为 os.UserConfigDir()/sotea/config.json，可用 --config 或 SOTEA_CONFIG 覆盖。密钥由 macOS Keychain / Windows Credential Manager 适配器保管，具体依赖实现时选择；配置使用格式版本、原子写和并发修改检查。默认不从仓库中自动加载可执行配置。

实例记录包括名称、完整 URL、CA 路径、SSH 映射及账号引用；本地仓库绑定记录规范化路径、remote 和实例，跨设备路径不自动迁移。按配置项采用显式参数 > 对应环境变量 > 已保存配置 > 默认值；上下文及凭证项的特殊顺序以上表和第 4 节为准。

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
| Issue / PR create、edit | --title、--body 或 --body-file；可重复 --assignee、--label，--milestone。创建标题必需，正文可为空；编辑至少有一项显式变更。PR 创建还需 --base 和 --head，编辑只接受服务端共同支持的分支字段。 |
| Issue assignee / label add、remove | 位置参数 index；分别用可重复 --user 或 --label 提供值，至少一个。标签名称必须解析为唯一 ID，有歧义报错；不模糊猜测。 |
| Issue / PR comment add | index，--body 或 --body-file 至少一个且内容非空。 |
| PR reviewer add | index，可重复 --user 和 --team，合计至少一个审查者。 |
| PR review create | index、--event approve / comment / request-changes；评论及请求修改需非空正文，approve 正文可选；可用 --commit 显式指定审查提交。 |
| PR merge | index、--method 默认 merge、--delete-branch 默认 false；候选方式只提供实例及仓库允许且已经跨版本验证的方式。 |
| Release create、edit | tag、--title、--body / --notes-file、--target、--draft / --publish、--prerelease；create 默认草稿和非预发布，edit 不传字段即不改变原值，--draft 与 --publish 互斥。 |
| Release asset upload | tag、file，单个普通文件；上传名称默认文件名，可用 --name 指定。 |
| commit status | 可选位置参数 ref；本地 HEAD 候选在调用前解析为明确提交，无法解析时报错或交互输入。 |

create 的负责人／标签是初始集合；edit 的 --assignee 和 --label 表示明确替换集合，在预览中显示完整变化。增量修改用相应 add / remove 命令，不能混用语义。编辑清空使用 --clear-body、--clear-milestone、--clear-assignees、--clear-labels，和同字段赋值互斥。PR 关闭／重开不接收正文等编辑参数，避免批量修改隐藏字段。

未被所有目标版本支持的筛选条件可按 TDD 第 7.3 节在完整有界查询后本地计算；PR 搜索不能仅筛当前页却显示为整个仓库搜索。排序保留服务器结果顺序，不宣称跨版本顺序一致。

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

通用 API 默认原始响应，HTTP 非成功使用非零退出码并保留状态，--json 的业务 envelope 不包裹通用 API 的原始 body；Git 正常退出保留原退出码。MCP 使用相同应用错误类别和逐项结果，不采用操作系统退出码表示每次工具失败。

## 7. PR 和 Release 默认流程

PR 创建以当前分支作为 head 候选、仓库默认分支作为 base 候选，显示后按明确参数或选择确定；跨仓库 head 显式指定所属账号／仓库，不自动 push 本地提交。PR 合并默认 merge；仓库不允许时展示合法方式供用户选择或要求 --method，不能偷偷改用 squash。删除源分支默认 false，force merge 不作为首版专用选项。检查由服务器最终裁决，合并前绑定已读 head SHA，变化时要求重新确认。

Release create 默认 draft=true、prerelease=false；--publish 明确直接发布，草稿可通过 edit 的 --publish 发布。选择已有标签则核对目标；新标签要求明确 --target 或交互选择并解析成提交，不由客户端猜测 main。创建标签和 Release 的服务端具体行为须逐版本测试；已有相同标签的 Release 报冲突，不覆盖。

附件上传前检查本地文件和已有附件；同名时报冲突，不自动删除覆盖。草稿的某个附件失败时保留草稿及成功附件，可继续补传；不自动发布或回滚已有资源。任何响应不确定时先查状态，禁止自动重复创建和上传。

## 8. 资料

- [Git credential](https://git-scm.com/docs/git-credential)：fill、approve 和 reject 的接口及影响范围。
- [Git credential contexts](https://git-scm.com/docs/gitcredentials)：协议、主机及 useHttpPath 的匹配规则。
- [Gitea API authentication](https://docs.gitea.com/development/api-usage/)：API 认证与 token。文档站描述不代表七个精确版本全部具备相同认证扩展，必须按隔离实例测试确认。
