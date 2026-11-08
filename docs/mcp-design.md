# sotea MCP 工具设计

> 状态：P6 完整工具实现、自动验证与七版本代表性链路通过；四客户端完整 UI 和真实目标平台待实测
>
> 更新日期：2026-11-08
>
> 对应：[requirements.md](requirements.md)、[cli-contract.md](cli-contract.md)

## 1. 启动与模式

同一可执行文件提供 sotea mcp serve，使用本地 stdio，由 MCP 客户端创建子进程。stdout 仅输出协议，stderr 用于脱敏诊断；EOF、取消及关闭时清理请求和子进程。程序不显示菜单，不要求用户保持其他 sotea 进程运行，也不登录读取协议 stdin。

默认只读；--allow-write 显式允许提供准备、提交和状态查询工具。--allow-raw-api 单独启用通用 API 工具，非读取方法同时需要 --allow-write。本地访问可选固定 --workdir、--follow-workspace 或仅远端；跟随按第 12 节的客户端根／显式适配器为每个会话选择唯一根。未授权本地访问时仍可通过显式实例和 owner/repo 使用远程业务工具。多根不自动选择或合并，跨根调用受根边界限制。

配置示意，具体包装字段由客户端决定：

~~~json
{
  "mcpServers": {
    "sotea": {
      "command": "/absolute/path/to/sotea",
      "args": ["mcp", "serve", "--workdir", "/path/to/repo", "--allow-write"]
    }
  }
}
~~~

首版只声明已实现的 tools 能力，不额外提供 prompts、resources、elicitation、HTTP 服务或客户端 roots 自动授权。

实例、账号、证书和凭证复用 CLI 配置；启动不要求所有实例在线，按工具调用返回缺少认证、上下文和网络错误。客户端可以各自创建进程；缓存和待提交计划只属于当前进程，不共享内存。需登录或换 token 时用户运行 CLI 的 auth 命令；服务检测配置变化后重新解析凭证。

## 2. 输入、输出和资源边界

每个工具定义明确的 JSON Schema，输入及公共结果外层关闭未定义属性，禁止无约束的 args 对象；原始 API 的受校验 query／headers 和服务端返回的 data 扩展字段为明确例外，不为关闭属性而丢弃合法远端字段。公共上下文支持 instance、account、repo；如需本地上下文，workdir 只能是启动根或其真实子目录。参数 > 对应环境 > 保存的绑定，歧义返回错误，不弹出终端。目标 repo 不允许通过 .. 或绝对 URL 逃逸实例，字符串按路径段编码。

列表输入使用 page（从 1 开始，默认 1）和 limit（默认 30，上限 100）；默认一页，不在工具内部无界抓取全部页面。业务列表可提供明确筛选项，具体版本走共享适配层；输出说明是否还有后续页面。读取超时默认 30 秒，写入 60 秒、上传 300 秒；上限可通过服务启动配置调整，取消可提前结束。

所有工具返回结构化内容，并提供准确的英文短摘要及同一结果的 JSON 文本副本供客户端展示／兼容读取；使用协议与所选 SDK 支持的 structuredContent / outputSchema。共同输出包括 schema_version=1、status、context、items、error；操作结果沿用 cli-contract.md。错误包含 code、message、HTTP 状态及可用诊断，不返回密钥或完整认证头。可识别错误类别：

| 类别 | 例子 |
| --- | --- |
| 参数和上下文 | INVALID_ARGUMENT、CONTEXT_REQUIRED、CONTEXT_AMBIGUOUS、PATH_OUTSIDE_ROOT |
| 身份和权限 | AUTH_REQUIRED、AUTH_IDENTITY_MISMATCH、PERMISSION_DENIED、WRITE_DISABLED |
| 网络和版本 | NETWORK_ERROR、TLS_ERROR、VERSION_UNKNOWN、COMPATIBILITY_ERROR |
| 对象和状态 | NOT_FOUND、CONFLICT、PRECONDITION_CHANGED |
| 执行和确认 | CANCELLED、PARTIAL_FAILURE、RESULT_UNKNOWN、PLAN_EXPIRED、PLAN_CONSUMED |

协议格式错误用相应 JSON-RPC 错误；参数已合法但业务执行失败时用工具结果和 isError，不崩溃或关闭服务。部分失败也标识 isError 并保留全部成功项；是否已发送写请求必须可判断。

默认最多 4 个并发读取，同一实例／账号／仓库的专用业务写入串行；原始 API 使用第 7.5 节独立锁，不声称可识别任意路径所属仓库；不同客户端或服务端用户仍可能并发，本地串行不能代替远端锁。工具输出上限默认 256 KiB，按第 7.2 节返回有界结果或完整错误；允许的预览截断标明 truncated 和完整获取方式，JSON 必须保持完整；不在 MCP 中返回大型二进制附件，完整流式处理使用 CLI。标题、正文、评论、附件名等远端内容作为数据返回，不能作为指令改变实例或提交计划。

## 3. 只读工具

以下工具纳入首版。按 P5 已有功能补齐账户／组织目录与负责人候选后，共 29 个读取／诊断工具（其中 Git 与原始 API 条件开放），另有第 4 节的 3 个写计划工具，完整模式合计 32 个。公共参数及分页规则见第 2 节，资源编号是仓库内编号，Release 使用标签解析 ID。可选的额外字段不能据此扩展未确认的高级命令范围。

| 工具名 | 关键输入 | 返回内容 |
| --- | --- | --- |
| sotea_context_get | 可选 instance、account、repo、workdir | 已解析实例、账号、仓库、Git 上下文及来源；不修改活动账号。 |
| sotea_instance_info | instance，可选 refresh | 原始服务版本、获取时间、缓存及定义／实例测试证据；不笼统承诺全部兼容。 |
| sotea_auth_status | instance、account，可选 check | 非敏感凭证来源、身份验证结果；不返回 token。 |
| sotea_account_repo_list | instance、account、page、limit | 当前已验证账号的仓库目录，范围以 /user/repos 实际返回为准；含稳定仓库 ID 与 full_name。 |
| sotea_account_org_list | instance、account、page、limit | 当前账号所属组织，含稳定组织 ID 与登录名。 |
| sotea_org_repo_list | instance、account、organization、organization_id、page、limit | 先核对组织 ID 再读取一页组织仓库；不改变活动仓库或本地 Git。 |
| sotea_repo_view | repo，可选 expected_repository_id | 仓库信息、默认分支和可供服务端返回的权限；来自目录的选择可用预期 ID 核对目标。 |
| sotea_repo_assignee_list | repo、page、limit | 仓库负责人候选，复用既有 assignees 端点，供 Issue／PR 字段明确选择。 |
| sotea_branch_list / sotea_tag_list | repo、page、limit | 分支或标签列表。 |
| sotea_commit_list / sotea_commit_status | repo、分页／ref | 提交列表或指定引用汇总状态。 |
| sotea_label_list / sotea_milestone_list | repo、page、limit | 仓库标签或里程碑，供用户明确选择。 |
| sotea_issue_list | repo、state、q、labels、page、limit | Issue 列表；显式排除 PR。 |
| sotea_issue_view | repo、index | Issue 详情，验证资源类型。 |
| sotea_issue_label_list | repo、index | 当前 Issue 标签，供移除或精确检查，不与仓库全部标签混淆。 |
| sotea_issue_comment_list | repo、index、page、limit | Issue 普通评论。 |
| sotea_pr_list / sotea_pr_view | repo、state、分页／index | PR 列表或详情。 |
| sotea_pr_files / sotea_pr_commits | repo、index、page、limit | PR 文件或提交。 |
| sotea_pr_comment_list / sotea_pr_review_list | repo、index、page、limit | 分别返回普通讨论或审查记录，不混为同一类。 |
| sotea_release_list / sotea_release_view | repo、分页／tag | Release 列表或按标签查询。 |
| sotea_release_asset_list | repo、tag、page、limit | 附件元数据，不读取任意下载 URL。 |
| sotea_git_context | workdir | 固定范围内的仓库根、分支、remote、status 元数据；需显式本地根。 |
| sotea_api_read | instance、method、path、query、headers | 仅在 --allow-raw-api 下提供，接受 GET / HEAD / OPTIONS，返回有界状态、头和 body。 |

业务只读工具使用 readOnlyHint=true，标注其预期行为；这些提示不是认证或权限保证。Git 工具仅执行已实现的固定查询，不接受 shell 或任意 Git 参数，禁用外部 pager 等交互。通用 API 的方法名不能证明自定义端点无副作用，因此工具明确提示实际服务器语义由用户负责，不授予绕过客户端审批的标签。

## 4. 写入工具

工具接口采用“准备 → 展示 → 提交”，实际业务动作共用 CLI 应用服务。首版不提供任意 shell、任意 Git 写操作、账号登录／退出工具，避免 MCP 调用修改用户全局 Git 或认证状态。

| 工具名 | 输入 | 行为 |
| --- | --- | --- |
| sotea_write_prepare | request_id、action、context、action 对应的严格参数 | 校验权限配置、目标及兼容规则，生成预览和有时限计划，不发送业务写请求。 |
| sotea_write_commit | plan_id、confirmation_token | 校验并执行已准备内容；不接受新的目标或 payload。 |
| sotea_write_status | request_id 或 plan_id | 返回同一进程中保存的执行结果；不再次执行。 |

request_id 由客户端提供，同一会话相同 request_id 与相同输入返回原计划或原结果；相同 ID 但不同输入报冲突。输入／身份校验尚未进入去重注册时的失败可以修正后重新准备；进入 preparing 索引后的失败保存同 ID 结果，再次尝试需新 request_id。已有计划的过期、失效与重复响应见第 7.3 节，不能通过同一 request_id 重新生成计划。已发送结果未知的操作不能通过同一 request_id 自动重发。

### 4.1 action 清单

以下是 prepare 的封闭枚举，各 action 使用 const／oneOf 分支描述其必填参数和允许字段。单目标／多目标规则与 CLI 完全一致。

| action | 必需业务输入 | 目标数量 |
| --- | --- | --- |
| issue.create | title，可选 body、assignees、label_ids、milestone_id | 创建一个 |
| issue.edit | index、非空 changes | 一个 Issue |
| issue.close / issue.reopen | indices | 1–100 个 Issue |
| issue.comment.add | index、body | 一个 Issue |
| issue.assignee.add / issue.assignee.remove | index、users | 一个 Issue，多用户 |
| issue.label.add / issue.label.remove | index、label_ids | 一个 Issue，多标签 |
| pr.create | base、head、title，可选 body、assignees、label_ids、milestone_id、users、teams | 创建一个 |
| pr.edit | index、非空 changes | 一个 PR |
| pr.close / pr.reopen | indices | 1–100 个未合并 PR |
| pr.comment.add | index、body | 一个 PR |
| pr.review.create | index、event，可选 body、commit_id、review_id；后两项按既有审查契约核对 | 一个 PR；review_id 可显式提交当前账号已有 pending 审查 |
| pr.reviewer.add | index、users／teams，至少一个非空 | 一个 PR，多审查者 |
| pr.merge | index、method、delete_branch（默认 false） | 一个 PR |
| release.create | tag、已有标签或明确 target，可选 name、body、draft、prerelease | 创建一个 |
| release.edit | tag、非空 changes，包含显式发布选项 | 一个 Release |
| release.asset.upload | tag、file_path，可选 name | 一个 Release，一个文件 |
| api.request | method、path、query、headers、body／已授权文件 | 一个请求，非读取方法且需 --allow-raw-api |

上述 action 展开后共 21 种。changes 中未提供、设置空值和明确清空为不同分支；不使用泛型 map 默许任何字段。action 使用 const／oneOf 分支并验证 action 与 params 的对应关系，不依赖 OpenAPI discriminator 关键字代替实际 JSON Schema 校验。已确认首版之外的字段或未适配功能返回明确错误。

### 4.2 计划、确认和执行

准备阶段读取当前目标，生成 plan_id、随机 confirmation_token、有效期（默认 5 分钟）、解析后的 context、业务摘要和相关状态摘要；保存规范化输入的摘要、文件摘要和对应凭证配置版本。API token 始终不返回；这里的 confirmation_token 只是调用计划凭据，不能作为已获得人工批准的证明。

客户端应向用户展示实例、账号、仓库、全部目标及实际修改，遵循自身工具批准机制后调用 commit。服务端无法从 MCP 调用独立验证“真人是否点击确认”，也不假装 annotations 是权限控制。--allow-write 是用户对该服务提供写能力的显式选择，客户端的审批配置和 Gitea 账号权限共同决定实际授权；不支持逐次人工审批的客户端必须向用户明确自动执行风险，默认保持只读。

commit 只接受当前会话的计划及确认凭据。目标、账号、凭证配置或文件变化时，返回 PRECONDITION_CHANGED；PR 的 head SHA、Release 标签／草稿状态、编辑内容版本等关键前置状态变化时要求重新准备。七版本不都具有远端比较并交换能力，读前检查无法保证全局原子性，也不能对外宣称无并发覆盖。

同一 plan_id 在进程内只能进入一次执行；并发 commit 合并为同一操作，重复调用返回已有执行状态或结果，不重放。计划过期、账号退出或服务重启后不能提交。记录保留至当前进程退出，初版最多 100 个未完成计划、1000 个终态记录（含成功、失败、过期和失效），并为计划／结果缓存设置合计 64 MiB 字节预算；达到上限返回资源限制错误，不淘汰正在执行或结果未知的记录来允许重试。客户端可先查询状态再决定是否准备新的操作。

HTTP 超时、断连、取消或批量中断后，保留 success / failed / skipped / unknown / not_executed 各项；已发送请求但无法确认结果时返回 RESULT_UNKNOWN，不自动重试。服务重启后没有本地结果记录，查询远端对象确认，不承诺跨进程幂等或自动补偿。

### 4.3 示例

~~~json
{
  "request_id": "close-issues-001",
  "action": "issue.close",
  "context": {"instance": "team", "repo": "acme/demo"},
  "params": {"indices": [42, 35]}
}
~~~

prepare 返回计划及摘要。客户端展示“关闭 team / acme/demo 的 #42、#35”，批准后调用：

~~~json
{"plan_id": "server-generated-plan", "confirmation_token": "server-generated-token"}
~~~

这两个字符串是占位符，不是实际可用的批准凭据。提供缺少编号的参数会报错；AI 客户端可以先调用 issue_list，再和用户确认选哪些目标，服务端不读取终端选择。

## 5. 本地文件和通用 API

附件上传只接受启动 --workdir 真实目录下、用户有权访问的普通文件，检查 symlink、规范化路径和文件类型。prepare 保存文件摘要；commit 通过共享上传服务的受控文件入口重验原文件，并发送内容固定的受控快照，不能在入口检查后由服务重新按原路径无保护地打开。具体句柄与内容并发修改边界见第 7.5 节；变化时报错，不自动改传其他文件。首版不开放任意文件下载落盘和根外文件；缺少 --workdir 时只能使用无需本地文件的工具。

通用 API 仅请求已选实例的 API 根下路径；拒绝绝对 URL 和路径穿越，跨来源重定向不发送认证或自动重放写入。认证头由认证层生成，工具不能覆盖 Authorization、Cookie 或通过 query 注入另一套认证；上传、请求体和自定义头仍需有界校验。原始 body 不按业务版本重写。

## 6. 验收

以 Codex、Claude Code、Cursor、VS Code 中至少各一款约定版本客户端记录实际验证结果；具体客户端版本作为验收证据，不预先声称全部兼容。检查 stdio 启动、工具发现、只读调用、明确参数和错误、两阶段写入、取消及 EOF 清理。各客户端批准 UI 不相同时记录其限制。

自动化验证只读模式不暴露写入、Schema 拒绝未知字段、计划失效、重复并发提交不重放、身份变化、本地文件边界、协议输出纯净和敏感信息脱敏；首次 P6 交付在真实 Gitea 七版本执行 MCP 代表性业务链路，P7 汇总完整业务发布矩阵；后续非 API 修改不逐版本重复验证。协议 SDK 和具体协议版本沿用 P2 锁定结果并测试，设计依据是 [MCP Tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools) 与 [stdio 传输](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)。


## 7. 实施前固定契约（2026-11-07 复核）

本节补齐会影响实现分歧的约定；不增加工具或业务 action。各工具的 JSON Schema 已在 P6-02 由 internal/mcp/schema.go 产出并作为适配依据，不能由每个 handler 自行决定字段、默认值和错误形式。

### 7.1 输入字段与共享服务映射

| 输入位置 | 固定语义 |
| --- | --- |
| 公共上下文 | 读取工具在顶层接受 instance／account／repo，只有需要本地上下文的工具再接受 workdir；prepare 使用封闭 context 对象。相同解析优先级，无隐含活动仓库。目录只要求实例／身份，不要求 repo；commit／status 只使用计划标识，不重新选目标。 |
| 编号与标识 | index、资源／组织／仓库 ID 为正整数，milestone_id 可为 0；indices 按首次顺序去重后须有 1–100 项，原始数组最多 100 项。request_id 为 1–128 字符、不含控制字符，不能作为 shell、路径或鉴权材料。 |
| Issue create／edit | create 直接接受 title、body、assignees、label_ids、milestone_id；edit 为 index + changes，changes 只接受同五字段且至少一个。对应 Changes.Title／Body／Assignees／Labels／Milestone，不暴露第二套 labels／milestone 别名或任意内部 Request。 |
| PR create／edit | create 除 Issue 五字段外接受 base／head、users／teams（请求审查者）；edit 的 changes 只接受 Issue 五字段及 base。适配到 Changes、Base、Head、Users／Teams，edit 不接受 head。users／teams 与内部 reviewers／team_reviewers 是明确映射，不能同时作为公共别名。 |
| 集合与清空 | 未提供字段不变；body="" 明确清空 Issue／PR 正文，assignees=[]／label_ids=[] 为精确清空集合，milestone_id=0 清空里程碑。所有 null、空 title、空 base／head 均拒绝。增删 action 的 users／label_ids 至少一项；不把空数组解释成清空全部。 |
| 审查与合并 | event 仅 approve／comment／request-changes；approve 可不带 body，其余要求非空正文。commit_id 映射内部 Commit，review_id 映射 ReviewID 并检查当前账号 pending 归属；未传 commit_id 时准备阶段固定远端 head。merge 的 method 明确必填，取 merge／squash／rebase／rebase-merge／fast-forward-only，仍需仓库实际允许；delete_branch 默认 false。 |
| Release 字段 | create 使用 tag、name、body、target、draft、prerelease；name 映射内部 Title。edit 为 tag + changes，changes 仅 name／body／target／draft／prerelease 且非空；布尔 false 保留。沿用共同版本契约，edit 的空 name／body／target 拒绝，不能把不支持的清空伪装成成功。asset.upload 的可选 name 默认为原文件 basename，内部快照文件名不能改变远端附件名。 |
| API 参数 | query／headers 为名称到字符串数组的受校验映射，保留重复值；header 名按大小写不敏感校验。api.request 的 body_text、body_file、multipart 三种来源互斥，未提供表示无 body，空 body_text 是明确零字节正文；multipart 为封闭对象：fields 为 {name,value} 字符串项数组，files 为 {field_name,file_path} 文件项数组且至少一个；远端文件名使用原文件 basename，Content-Type／boundary 由服务生成，调用者不能覆盖。拒绝 Host、Authorization、Proxy-Authorization、Cookie 及传输 framing 头，不接受任意本地路径扩展。api_read 不接受请求体或本地文件；不开放 CONNECT／TRACE 或自定义方法，写方法为 POST／PUT／PATCH／DELETE。 |

只读过滤字段逐项对应实际 Filter，P6-02 给出封闭字段表；不得为了省事丢弃 CLI 已有的负责人／里程碑、PR base／author、Release 搜索等筛选。旧版本需本地过滤时沿用完整有界扫描后分页；超过共享扫描上限返回 FILTER_LIMIT，不把当前页过滤当完整结果。远端 commit.status 必须显式 ref；仅在授权本地上下文存在时才可采用已解析且与目标对应的本地 HEAD，不猜远端默认提交。

### 7.2 输出形状、文本兼容与超限

MCP 结果保留 schema_version=1、status、context、items、error 五个共同字段，额外的可选 meta 仅属于 MCP，关闭未知属性；不修改 CLI JSON。meta 下只定义以下有类型对象，各工具只返回适用部分：

| 对象 | 内容与位置 |
| --- | --- |
| plan | request_id、plan_id、action、plan_state、committable、created_at、适用的 expires_at；plan_id 在实际生成计划后才有值。仅成功注册计划的 prepare 响应附 confirmation_token，status／commit 不重新发 token。成功 prepare 的共同 status=success 只表示准备成功；status 查询 executing 时不宣称业务成功，摘要须明确仍在执行。时间输出为 UTC RFC3339，内部期限用单调时间计算。完整业务预览在 items[].data，不混入内部 Plan 指针。 |
| execution | write_attempted 为 no／yes／unknown，表示是否曾进入业务写请求发送边界，不证明服务端完成；实际结果以逐项／逐步骤 status 为准。进入发送后无确认结果标 unknown；不能以 HTTP 错误、取消或 EOF 推断回滚。 |
| truncation | truncated、omitted_fields、reason、next_action；仅描述确实省略的预览／远端数据。不能省略目标、错误、步骤状态、资源标识、已发送证据或批准所需变更。 |
| api | HTTP status、受校验且脱敏的响应 headers、body_encoding（json／utf8／base64）及 body 数据，保持原始语义。编码后仍受最终消息预算约束，不返回任意下载引用。 |

分页保留共同 Page 的 page／limit／has_more，放在对应 items[].data 中。文本 content 同时包含短摘要和与 structuredContent 完全相同的、脱敏后的完整 JSON envelope；只读取文本的客户端也能取得计划、编号、分页、错误及执行结果。不能只返回“completed”摘要。锁定 SDK 在已有自定义文本时不会自动为对象结果添加 JSON 副本，适配层须明确提供并验证。

256 KiB 按最终序列化 CallToolResult 计算，含两份结果及 JSON 转义开销。详情／原始响应可省略明确字段形成预览；业务列表页过大时返回 RESPONSE_TOO_LARGE 和缩小 limit、从 page=1 重新读取的指引，不返回缺项的假完整页面。prepare 必须能放下全部实际变更及混合目标预检结果，否则不注册计划、不发 token。commit／status 先压缩远端正文等非必要数据，仍保留全部目标、步骤、资源标识和状态；执行前预留可容纳最小结果的预算，不能在发送后才因本地存储不足丢失结果。

### 7.3 计划状态、重复调用与失败准备

| 当前情况 | prepare／commit／status 的确定行为 |
| --- | --- |
| 相同 request_id 正在准备 | 进程内保留 preparing 占位并合并等待；它不是可提交计划。原准备取消／失败且没有业务写时释放执行槽／计划资源，但将同 ID 意图和失败结果保存为 completed、committable=false；再次准备需新 request_id，不因失败忘记已登记意图。 |
| prepared | 相同意图的 prepare 返回原预览、原 token 和原期限，不延长 5 分钟；5 分钟限制的是首次 commit 被接纳进入 executing 的时间，已接纳操作按调用超时执行；排队未接纳时仍检查期限。commit 校验后只有一个执行者。status 返回预览和 committable=true，但不返回 token。 |
| executing | 不受 prepared 期限届满影响，不转回 prepared；重复 prepare／commit 返回同一执行中状态或等待其结果，status 可立即查询，不重新发送请求。 |
| completed | 无论成功、失败、取消、partial 或 unknown，重复调用返回已保存结果，不以“消费过”为由丢弃已知结果；即使原准备期限已过也仍可查询。准备阶段失败也保存为不可提交的 completed 记录，尚未生成的 plan_id／token 不返回；status 可按 request_id 查询。需要新的意图或确认时使用新 request_id。 |
| expired／invalidated | 未执行计划过期或目标／身份前置条件失效。commit 分别返回 PLAN_EXPIRED／PRECONDITION_CHANGED，committable=false；同 request_id 不产生新 token，必须用新 ID 重新准备。保留有界终态索引，不能忘记旧 ID 后重放。 |
| 不存在的标识／错误 token | 同进程未知标识为 NOT_FOUND，提示可能来自另一进程／重启；错误 token 为 PERMISSION_DENIED，不消费计划、不返回正确 token。PLAN_CONSUMED 只用于没有可用缓存结果却已确认消费的异常保护，禁止据此建议盲目重试。 |

status 必须恰好提供 request_id／plan_id 之一，否则 INVALID_ARGUMENT。批量预检的目标错误全部进入完整预览；存在合法可执行目标时可注册 committable=true 的 partial 计划，同时 isError=true，客户端必须展示失败目标与固定可执行子集。全部目标预检失败时不注册可提交计划。commit 只处理预览里固定的子集，失败目标不在确认后被重新“修好”并加入执行；最终仍保留全部原始目标的结果。

成功、失败、过期、失效的 request_id 都有进程内终态索引。100 个未完成计划、1000 个终态记录及 64 MiB 总缓存均计入准备占位／索引／预览／结果；满额拒绝新准备，不自动丢弃旧 ID。接纳 preparing 时即预留终态索引槽，进入 commit 前再预留最小结果字节预算，防止并发准备在完成时突破终态限额。过期释放不再需要的文件资源和大快照，但保留去重状态；执行中及 unknown 记录不可淘汰。身份或配置变化后保留最小终态以防重放。首次 commit 的前置重验尚无写入时发现配置修订／身份／目标变化，统一映射 PRECONDITION_CHANGED 并标 invalidated；不将领域层 CONFIG_CONFLICT 暴露成可忽略的重试提示。已完成的执行结果不因后来过期或配置变化而重算，身份边界只控制其可见内容。

### 7.4 身份变化与离线结果查询

读取业务、prepare 和首次 commit 都按既有来源边界重新解析当前配置并验证所选身份。write_status 及已完成结果的重复查询只核对本地身份绑定／相关配置修订，不额外要求远端在线，因此断网或 token 过期后仍可查询本进程已知结果；这些查询绝不刷新 token 或发业务写。

本地配置／账号／凭证来源与计划绑定不一致时，未执行计划失效；status 仅返回计划 ID、状态、committable=false 和 PRECONDITION_CHANGED，不返回旧身份的预览、token 或业务结果。MCP 进程无法获得 CLI 退出前继承的环境变量变化；环境 token 被外部替换时须重启 MCP，重启后的旧计划不可恢复。未登记来源的匿名诊断沿用 P2；有身份的业务和原始 API 写入要求来源确认，不能把显式 URL 当作发送凭证授权。

### 7.5 文件访问、固定内容与进程资源

--workdir 启动参数须为存在的绝对目录，启动时解析为唯一真实根；工具传入的相对 workdir／file_path 以该根解析，与进程 cwd 无关，绝对路径也必须在根内。Git worktree 的实际工作区必须在根内；共享 Git 元数据可位于 Git 验证的真实管理目录，仅由固定 Git 查询访问，不据此给通用文件动作扩大根。客户端 roots 通知、工具参数和环境变量均不能扩大启动授权根。

路径授权必须接到 Release／API 共享服务最终打开文件的边界；只在 MCP handler 提前检查不够。同一打开句柄可以防止路径被换成另一文件，但不能防止同一文件被原地修改。因此 commit 在受检原文件上核对身份／内容，并生成仅本进程使用、内容固定的受控快照后发送；快照摘要必须与 prepare 相同。准备后的内容／身份／根映射变化拒绝提交；发送期间原文件变化不能使上传字节偏离已确认摘要。快照不按用户提供路径重开，完成／取消／过期／EOF 删除内部临时资源，不作为用户下载落盘功能。

默认单次文件请求的文件总量上限 64 MiB（Release 单文件、API multipart 所有文件合计），以元数据预检并在实际复制时再次有界校验，可通过服务启动配置调整；快照磁盘／内存合计默认另限 256 MiB，可通过启动配置调整；创建前预留配额，不能因流式发送免除限额。保持 CLI 原有文件能力，MCP 超限给出 CLI 完整处理指引。业务原文受已有 256 KiB 字段上限和更小的协议帧实际预算共同约束，禁止工具接收 stdin／无界管道。关闭快照和文件失败应记录，不能悄悄继续使用其他来源；只能清理当前进程创建的精确临时资源，不扫描或删除其他进程目录。程序崩溃／强制终止不能保证清理，客户端说明记录这一限制。

默认 4 个读取／准备执行槽、4 个 commit 执行槽、全服务最多 64 个排队调用；status 使用独立轻量路径，不能被长上传阻塞。排队计入调用超时，满队列返回 RESOURCE_LIMIT；同一槽位只在入口获取一次，内部服务不重复获取导致死锁。锁等待响应取消；原始 API 无法可靠推导仓库时按完整实例根／账号的独立原始 API 写锁串行，不假称与专用仓库动作具有跨入口原子性。EOF／信号取消后最多 10 秒有界收尾，无法确认的发送保留 unknown；进程退出后仍需按远端对象核对，不承诺本地恢复。

### 7.6 原始 API 与版本的能力边界

api.request 的 prepare 固定 method、实例 API 根内 path、query／headers、body 字节／文件摘要；只做本地输入、来源、身份和文件检查，不擅自用 GET／HEAD 探测任意路径，也不假设任意端点具有 ETag 或仓库修订前置条件。预览明确提示“固定请求，不提供业务对象并发保护”；若用户显式提供合法 If-Match 等条件则原样发送，不替用户构造或删除。非读取方法不自动跟随重定向重发，跨来源重定向拒绝，不携带认证。

原始 API 沿用 CLI 的未知版本直接请求能力；专用业务写入沿用七个已研究精确版本和共享适配，不能借 raw 开关绕过专用工具的语义校验。原始 API 返回 2xx 但响应处理失败时保留已接受证据；无响应且可能已发送则 unknown，HTTP 失败保留状态和有界 body，不自动重试。meta.execution.write_attempted 由实际 HTTP 发送边界记录，不由工具是否“被调用”猜测。

## 8. 开工检查结论

2026-11-08 补充：客户端本地配置向导由 P6-29 至 P6-32 追加任务承接，设计见 [接入说明第 1 节](mcp-client-setup.md#1-菜单配置向导与认证)。向导不增加协议工具或权限开关，不启动共享后台服务；健康检查只做本地 MCP 初始化／发现，不调用业务工具。四客户端实际 UI／审批仍由 P6-16 单独验收。

2026-11-07 复核结论：需求、服务复用、工具覆盖、权限模式、输入输出、计划状态、身份变化、文件边界、资源限制及验收责任已形成可实施闭环，可以开始 P6-01／18／02。本结论只表示设计可开工，该结论仅为前期设计基线；后续实现及实际验收状态见 [P6 交付](p6-mcp.md)。

| 检查项 | 结论／后续责任 |
| --- | --- |
| 功能与依赖 | P2 至 P5 实际能力均有映射，缺少的分页／共享 API／最终文件打开适配列为 P6 实现任务，不误称已有。 |
| 高风险契约 | 本节 7.1 至 7.6 已固定实现语义；P6-02 固定可机读 Schema，P6-25 覆盖并发、失败、变化与资源边界。 |
| SDK／Harness | 锁定 SDK 源码已核对文本回退行为；真实客户端联合 Schema、批准交互仍由 P6-02／16 实测，不能由文档证明兼容。 |
| 平台与实例 | 四类客户端、七个指定 Gitea 版本和真实目标 OS 保留原验收门槛；环境缺口影响对应验收，不阻止独立开发。 |
| 首条实施链路 | 组合入口／契约 → 上下文与完整只读 → Issue 准备／提交／状态 → PR／Release／文件／API → 完整验证。 |


## 10. P2 原型基线（已由 P6 替代）

P2 阶段的 mcp serve 仅注册 sotea_instance_info，复用配置重读、上下文、认证及版本诊断服务，不通过启动 CLI 或解析其文本实现。官方 SDK 锁定 v1.8.0，明确协商 2025-11-25／2025-06-18，严格关闭未知输入属性，提供 readOnlyHint、outputSchema／structuredContent 和业务 isError。默认最多四个并发诊断，输入帧上限 256 KiB。

两版本真实 stdio 初始化、工具发现、诊断、非法属性、业务失败后继续调用、配置变更缓存失效和 EOF 已通过。MCP 未给 --workdir 时不从宿主 cwd 发现 Git；未登记 URL 的实例诊断仅做匿名版本读取，不读取凭证或建立登录。

该 P2 原型不提供 --allow-write／--allow-raw-api、其余读取工具、写计划或 MCP 登录，也未把 SDK 协议测试当作 Codex／Claude Code／Cursor／VS Code 验收。上述完整工具设计继续作为首版目标；原型证据及依赖许可说明见 [P2 记录](p2-readonly.md)。

## 11. P6 实施回顾与新增适配边界（2026-11-07）

2026-11-07 前期回顾时运行代码仍为 P2 实例诊断原型；2026-11-08 已交付 32 个工具和 21 种 action，实际实现、自动／七版本结果和客户端验收缺项见 [p6-mcp.md](p6-mcp.md)。逐项任务、前置依赖和验收记录见 [development-plan.md](development-plan.md) 的 P6。SDK 保持 go-sdk v1.8.0，协议仍显式协商 2025-11-25／2025-06-18；工具链、依赖和协议升级不包含在此次计划更新。

P3／P4 现有 Issue、PR、Release 服务已经具有类型化 Prepare／Preview／Commit 和一次消费保护。MCP 必须保留实际 Plan 对象，在协议层增加 ID 索引、5 分钟有效期、确认凭据、并发去重及状态查询；不能丢弃原计划，在 commit 时重新 Prepare 后执行。共享仓库锁只有一个持有层，避免 MCP 外层锁与 Service.Begin 内层重入死锁。状态机使用 plan_state 表示 prepared／executing／completed／expired／invalidated，与 app.Result.status 中的 success／partial／unknown 等业务状态分开。

P5 的 catalog.List／GetOrganization／GetRepository 可直接承接账户仓库、组织与组织仓库工具，目录每页有稳定 ID。工具返回显式上下文，后续调用携带 repo／account；不建立菜单式隐含活动仓库，也不保存默认配置。来自目录的仓库选择通过 expected_repository_id 核对；仓库业务写计划在准备时记录实际仓库 ID 并在提交前重核，防止同名仓库重建后误提交。原始 API 按明确实例、账号和完整请求意图绑定，不强制无关 repo，也不从任意 API 路径猜测仓库。负责人候选现有服务只提供完整有界目录，P6 补充真正分页的共享只读入口，不能在 MCP 中把完整目录伪装成一页。

当前通用 API 的参数／文件／输出编排仍在 CLI，MCP 不能直接调用 runtime.runAPI 或 Cobra RunE。实施时把实际需要的非终端请求校验、请求体／multipart 构造、单次发送与结果分类提取为共享应用服务；CLI 继续处理自己的确认和原始流，MCP 增加授权根和有界结构化结果。已有 fileinput.Open 不等于授权根保护，Release 上传计划的文件身份／摘要重核也不等于根内访问保证；MCP 必须在打开及最终使用同一文件句柄的边界做平台适用校验。

256 KiB 工具输出预算按最终序列化内容计算，涵盖 structuredContent、文本兼容内容及脱敏后的元数据，不直接裁断 JSON 或丢弃逐项执行状态。列表超限返回明确缩小 limit 并从一致分页起点重试的指引，不以减少返回项但沿用原 page／limit 造成跳项。详情／原始 body 可返回带 truncated 及省略字段说明的有界预览，指向直接 CLI 获取完整内容；写入预览超限则不发放可提交计划／confirmation_token。写入已执行后输出超限仍保存可确认资源、全部步骤状态和 unknown 信息，不改变为可重试失败。

首次 P6 交付需实际验证新增 MCP 链路在七个指定 Gitea 版本的代表性业务；完整发布矩阵仍由 P7 汇总。后续仅 Schema 文案、协议摘要或展示修订按影响范围验证，不重复全部 Gitea 版本。四类真实客户端、Windows 10／11 及最低 macOS 仍逐项记录；SDK 内存传输／stdio 脚本和交叉编译不能代替这些证据。


## 12. 客户端项目工作区跟随

MCP 配置向导的本地访问模式有三种：Follow client workspace、Choose a fixed directory、Remote tools only。Follow 不把目录写入客户端配置的固定 --workdir，而是在每个 MCP 会话中建立独立授权根；固定目录仍使用 --workdir，Remote tools only 不注册本地 Git 工具且文件动作返回明确错误。

Follow 模式优先读取 MCP roots/list 的唯一 file URI 根；多根、远程 host、查询／fragment、卷根、用户主目录和无法解析的路径均拒绝本地访问但不阻断远端工具。Codex 和 Claude Code 由向导写入明确的客户端适配器：Codex 记录 --workspace-source codex 并使用客户端启动项目目录，Claude Code 记录 --workspace-source claude 并使用 CLAUDE_PROJECT_DIR；没有适配器时不能把 sotea 自己的 cwd 当项目。客户端适配器是启动边界，不接受工具参数扩权。Cursor／VS Code 当前依赖 roots，真实目录传递尚待验收，不能把 Codex／Claude 的结果外推到其他客户端。

每个会话的真实根、文件句柄和 workspace_binding 独立；根通知或目录实体变化会使该会话旧计划失效，即使随后恢复原路径也不恢复旧计划。已使用 roots 的会话在根撤销后不得回退到启动适配器重新授权。不同会话不能提交彼此计划；没有唯一根时 context／账户／远端 Issue／PR／Release 仍可用。进程最多保留八个跟随根，根变更不关闭仍被在途调用使用的句柄，进程结束统一关闭；在途请求按调用开始时的快照授权，撤销从后续调用起生效。

SDK 保持原来锁定版本及 2025-11-25／2025-06-18 两协议；roots 已有明确 SDK API，未升级 MCP 协议。后续协议弃用 roots 时需要单独适配，不能悄悄允许任意工具路径。
