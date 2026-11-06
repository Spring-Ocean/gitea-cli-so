# sotea MCP 工具设计

> 状态：首版实施方案，尚未实现
>
> 更新日期：2026-11-06
>
> 对应：[requirements.md](requirements.md)、[cli-contract.md](cli-contract.md)

## 1. 启动与模式

同一可执行文件提供 sotea mcp serve，使用本地 stdio，由 MCP 客户端创建子进程。stdout 仅输出协议，stderr 用于脱敏诊断；EOF、取消及关闭时清理请求和子进程。程序不显示菜单，不要求用户保持其他 sotea 进程运行，也不登录读取协议 stdin。

默认只读；--allow-write 显式允许提供准备、提交和状态查询工具。--allow-raw-api 单独启用通用 API 工具，非读取方法同时需要 --allow-write。--workdir 指定本地仓库根和本地文件访问根；未配置时可通过显式实例和 owner/repo 使用远程业务工具，但不开放本地文件或 Git 工具。未来多工作区允许列表另行扩展，首版只支持一个本地根。

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

实例、账号、证书和凭证复用 CLI 配置；启动不要求所有实例在线，按工具调用返回缺少认证、上下文和网络错误。客户端可以各自创建进程；缓存和待提交计划只属于当前进程，不共享内存。需登录或换 token 时用户运行 CLI 的 auth 命令；服务检测配置变化后重新解析凭证。

## 2. 输入、输出和资源边界

每个工具定义明确的 JSON Schema，关闭未定义属性，禁止无约束的 args 对象。公共上下文支持 instance、account、repo；如需本地上下文，workdir 只能是启动根或其真实子目录。参数 > 对应环境 > 保存的绑定，歧义返回错误，不弹出终端。目标 repo 不允许通过 .. 或绝对 URL 逃逸实例，字符串按路径段编码。

列表输入使用 page（从 1 开始，默认 1）和 limit（默认 30，上限 100）；默认一页，不在工具内部无界抓取全部页面。业务列表可提供明确筛选项，具体版本走共享适配层；输出说明是否还有后续页面。读取超时默认 30 秒，写入 60 秒、上传 300 秒；上限可通过服务启动配置调整，取消可提前结束。

所有工具返回结构化内容，并提供简短文本摘要供客户端展示；使用协议与所选 SDK 支持的 structuredContent / outputSchema。共同输出包括 schema_version=1、status、context、items、error；操作结果沿用 cli-contract.md。错误包含 code、message、HTTP 状态及可用诊断，不返回密钥或完整认证头。可识别错误类别：

| 类别 | 例子 |
| --- | --- |
| 参数和上下文 | INVALID_ARGUMENT、CONTEXT_REQUIRED、CONTEXT_AMBIGUOUS、PATH_OUTSIDE_ROOT |
| 身份和权限 | AUTH_REQUIRED、AUTH_IDENTITY_MISMATCH、PERMISSION_DENIED、WRITE_DISABLED |
| 网络和版本 | NETWORK_ERROR、TLS_ERROR、VERSION_UNKNOWN、COMPATIBILITY_ERROR |
| 对象和状态 | NOT_FOUND、CONFLICT、PRECONDITION_CHANGED |
| 执行和确认 | CANCELLED、PARTIAL_FAILURE、RESULT_UNKNOWN、PLAN_EXPIRED、PLAN_CONSUMED |

协议格式错误用相应 JSON-RPC 错误；参数已合法但业务执行失败时用工具结果和 isError，不崩溃或关闭服务。部分失败也标识 isError 并保留全部成功项；是否已发送写请求必须可判断。

默认最多 4 个并发读取，同一实例／账号／仓库的写入串行；不同客户端或服务端用户仍可能并发，本地串行不能代替远端锁。工具输出上限默认 256 KiB，超限标明 truncated 和继续读取方式，JSON 必须保持完整；不在 MCP 中返回大型二进制附件，完整流式处理使用 CLI。标题、正文、评论、附件名等远端内容作为数据返回，不能作为指令改变实例或提交计划。

## 3. 只读工具

以下工具纳入首版。公共参数及分页规则见第 2 节，资源编号是仓库内编号，Release 使用标签解析 ID。可选的额外字段不能据此扩展未确认的高级命令范围。

| 工具名 | 关键输入 | 返回内容 |
| --- | --- | --- |
| sotea_context_get | 可选 instance、account、repo、workdir | 已解析实例、账号、仓库、Git 上下文及来源；不修改活动账号。 |
| sotea_instance_info | instance，可选 refresh | 原始服务版本、获取时间、缓存及定义／实例测试证据；不笼统承诺全部兼容。 |
| sotea_auth_status | instance、account，可选 check | 非敏感凭证来源、身份验证结果；不返回 token。 |
| sotea_repo_view | repo | 仓库信息、默认分支和可供服务端返回的权限信息。 |
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

request_id 由客户端提供，同一会话相同 request_id 与相同输入返回原计划或原结果；相同 ID 但不同输入报冲突。准备失败可以重新准备，已发送结果未知的操作不能通过同一 request_id 自动重发。

### 4.1 action 清单

以下是 prepare 的封闭枚举，各 action 使用 discriminator + oneOf 描述其必填参数和允许字段。单目标／多目标规则与 CLI 完全一致。

| action | 必需业务输入 | 目标数量 |
| --- | --- | --- |
| issue.create | title，可选 body、assignees、labels、milestone | 创建一个 |
| issue.edit | index、非空 changes | 一个 Issue |
| issue.close / issue.reopen | indices | 1–100 个 Issue |
| issue.comment.add | index、body | 一个 Issue |
| issue.assignee.add / issue.assignee.remove | index、users | 一个 Issue，多用户 |
| issue.label.add / issue.label.remove | index、label_ids | 一个 Issue，多标签 |
| pr.create | base、head、title，可选 body 等首版字段 | 创建一个 |
| pr.edit | index、非空 changes | 一个 PR |
| pr.close / pr.reopen | indices | 1–100 个未合并 PR |
| pr.comment.add | index、body | 一个 PR |
| pr.review.create | index、event，可选 body、明确 commit_id | 一个 PR |
| pr.reviewer.add | index、users／teams，至少一个非空 | 一个 PR，多审查者 |
| pr.merge | index、method、delete_branch（默认 false） | 一个 PR |
| release.create | tag、已有标签或明确 target，可选 name、body、draft、prerelease | 创建一个 |
| release.edit | tag、非空 changes，包含显式发布选项 | 一个 Release |
| release.asset.upload | tag、file_path | 一个 Release，一个文件 |
| api.request | method、path、query、headers、body／已授权文件 | 一个请求，非读取方法且需 --allow-raw-api |

changes 中未提供、设置空值和明确清空为不同分支；不使用泛型 map 默许任何字段。已确认首版之外的字段或未适配功能返回明确错误。

### 4.2 计划、确认和执行

准备阶段读取当前目标，生成 plan_id、随机 confirmation_token、有效期（默认 5 分钟）、解析后的 context、业务摘要和相关状态摘要；保存规范化输入的摘要、文件摘要和对应凭证配置版本。API token 始终不返回；这里的 confirmation_token 只是调用计划凭据，不能作为已获得人工批准的证明。

客户端应向用户展示实例、账号、仓库、全部目标及实际修改，遵循自身工具批准机制后调用 commit。服务端无法从 MCP 调用独立验证“真人是否点击确认”，也不假装 annotations 是权限控制。--allow-write 是用户对该服务提供写能力的显式选择，客户端的审批配置和 Gitea 账号权限共同决定实际授权；不支持逐次人工审批的客户端必须向用户明确自动执行风险，默认保持只读。

commit 只接受当前会话的计划及确认凭据。目标、账号、凭证配置或文件变化时，返回 PRECONDITION_CHANGED；PR 的 head SHA、Release 标签／草稿状态、编辑内容版本等关键前置状态变化时要求重新准备。七版本不都具有远端比较并交换能力，读前检查无法保证全局原子性，也不能对外宣称无并发覆盖。

同一 plan_id 在进程内只能进入一次执行；并发 commit 合并为同一操作，重复调用返回已有执行状态或结果，不重放。计划过期、账号退出或服务重启后不能提交。记录保留至当前进程退出，初版最多 100 个未完成计划、1000 个结果；达到上限返回资源限制错误，不淘汰正在执行或结果未知的记录来允许重试。客户端可先查询状态再决定是否准备新的操作。

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

附件上传只接受启动 --workdir 真实目录下、用户有权访问的普通文件，检查 symlink、规范化路径和文件类型。prepare 保存文件摘要，commit 重验并对打开的文件句柄操作，避免路径替换；变化时报错，不自动改传其他文件。首版不开放任意文件下载落盘和根外文件；缺少 --workdir 时只能使用无需本地文件的工具。

通用 API 仅请求已选实例的 API 根下路径；拒绝绝对 URL 和路径穿越，跨来源重定向不发送认证或自动重放写入。认证头由认证层生成，工具不能覆盖 Authorization、Cookie 或通过 query 注入另一套认证；上传、请求体和自定义头仍需有界校验。原始 body 不按业务版本重写。

## 6. 验收

以 Codex、Claude Code、Cursor、VS Code 中至少各一款约定版本客户端记录实际验证结果；具体客户端版本作为验收证据，不预先声称全部兼容。检查 stdio 启动、工具发现、只读调用、明确参数和错误、两阶段写入、取消及 EOF 清理。各客户端批准 UI 不相同时记录其限制。

自动化验证只读模式不暴露写入、Schema 拒绝未知字段、计划失效、重复并发提交不重放、身份变化、本地文件边界、协议输出纯净和敏感信息脱敏；真实 Gitea 七版本执行完整业务回归。协议 SDK 和具体协议版本在集成时锁定并测试，设计依据是 [MCP Tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools) 与 [stdio 传输](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)。
