# Gitea API 多版本差异调研

> 调研日期：2026-11-04
>
> 范围：需求中列出的七个精确版本；以 Issue、Pull Request（PR）、Release 和开发上下文为重点。
>
> 性质：官方 API 定义静态对比与部分源码核对，尚未进行实例集成测试。

> 2026-11-06 进展：七版本隔离实例已完成 P0 版本核对及最小业务数据的两轮初始化演练，见 [P0 记录](p0-readiness.md)。本文的完整兼容性分析仍未经过 sotea 客户端集成验收，不能以环境演练代替。

## 1. 结论

1. 七个目标版本的官方定义均包含核心 Issue、PR 和 Release 操作；这只能证明文档中存在对应方法和路径，不能证明权限、请求字段、响应行为或运行时语义完全一致。
2. Issue 负责人增删接口只出现在本次取样的 1.27.3 与 28.3.0 定义中。此前五个样本可通过 Issue 编辑模型设置完整负责人列表，但这不是原子增删，涉及并发覆盖风险。
3. PR 合并请求中的部分字段在 1.25.5 与 1.26.4 的定义之间发生改名。1.26.4 和 28.3.0 源码显式兼容旧字段；旧版所接受的字段及各版本行为仍应通过实例验证。
4. 不应因缺少业务命令、版本识别失败或版本未纳入静态适配矩阵而阻断用户显式构造的通用 API 请求。业务命令需要针对能力差异做明确适配，不能静默忽略用户指定的字段或筛选条件。
5. [逐版本差异明细](gitea-api-differences.md)记录全部 HTTP 方法与路径的增删，以及脚本覆盖的参数、响应声明和模型字段变化。该明细来自静态分析；新增的 P0 实例准备证据单独记录，完整客户端兼容验收未完成。

## 2. 来源与比较口径

以下均来自 Gitea 官方仓库的对应版本标签，不以会随文档站更新的 API 页面代替精确补丁版本。前六个版本使用仓库中的 Swagger 2.0 模板；28.3.0 使用同一标签下生成的 Swagger 2.0 文件，避免直接混比 Swagger 2.0 与 OpenAPI 3.0。

| 目标版本 | 官方版本化 API 定义 |
| --- | --- |
| 1.22.6 | [v1.22.6 Swagger](https://github.com/go-gitea/gitea/blob/v1.22.6/templates/swagger/v1_json.tmpl) |
| 1.23.8 | [v1.23.8 Swagger](https://github.com/go-gitea/gitea/blob/v1.23.8/templates/swagger/v1_json.tmpl) |
| 1.24.7 | [v1.24.7 Swagger](https://github.com/go-gitea/gitea/blob/v1.24.7/templates/swagger/v1_json.tmpl) |
| 1.25.5 | [v1.25.5 Swagger](https://github.com/go-gitea/gitea/blob/v1.25.5/templates/swagger/v1_json.tmpl) |
| 1.26.4 | [v1.26.4 Swagger](https://github.com/go-gitea/gitea/blob/v1.26.4/templates/swagger/v1_json.tmpl) |
| 1.27.3 | [v1.27.3 Swagger](https://github.com/go-gitea/gitea/blob/v1.27.3/templates/swagger/v1_json.tmpl) |
| 28.3.0 | [v28.3.0 生成的 Swagger](https://github.com/go-gitea/gitea/blob/v28.3.0/templates/swagger/v1-swagger.generated.json) |

用户提供的 [Gitea API 文档入口](https://docs.gitea.com/api/)可用于浏览；28.3.0 也有[官方发布说明](https://blog.gitea.com/release-of-28.3.0/)。模板中的版本和子路径是服务端渲染占位符，本次只解析 JSON 并比较 API 契约，不把占位符内容当作实际实例信息。源文件的 SHA-256 记录在差异明细顶部。

一次操作定义为一组 HTTP 方法和路径；相邻版本按方法及路径的原始字符串比较。模型按 Swagger definitions 名称比较，字段比较覆盖类型、引用、枚举、默认值、必填项及脚本列出的约束。明细也覆盖参数、响应声明、媒体类型和安全声明的部分变化。枚举顺序不作为差异；描述、示例、服务端具体实现、引用目标内部变化与行为变化不能仅凭操作差异判断。详尽范围以脚本实现为准。

下文的“新增”只表示在**相邻两个已选样本**中，后一个定义有而前一个没有；它不证明该能力最早出现在后一个补丁版本，也不能外推到未检查的中间版本。API 文档可能存在遗漏或生成格式变化，实际支持必须以指定版本的隔离实例测试为准。

## 3. 全局变化概览

| 对比的精确版本 | 新增操作 | 移除操作 | 代表性变化 |
| --- | ---: | ---: | --- |
| 1.22.6 → 1.23.8 | 10 | 0 | 标签保护、分支重命名及合并上游等定义增加。 |
| 1.23.8 → 1.24.7 | 36 | 0 | Actions runner、工作流、制品、Issue 锁定等定义增加。 |
| 1.24.7 → 1.25.5 | 15 | 0 | Actions 运行、任务及仓库扩展内容接口增加。 |
| 1.25.5 → 1.26.4 | 11 | 7 | 增加 PR 行评论解决／取消解决和 Actions 重试；部分旧 runner 注册及 ActivityPub 等路径不再列出。 |
| 1.26.4 → 1.27.3 | 11 | 0 | 增加 Issue 负责人增删、PR 审查评论回复及 token 查询等定义。 |
| 1.27.3 → 28.3.0 | 55 | 0 | 增加仓库、组织及用户项目看板接口，以及部分 Actions 操作等。 |

移除的路径不一定意味着整个功能消失：例如上述旧 runner 注册 GET 与已有 POST 形式需要结合目标版本文档分别判断，不能仅按名称自动替换。完整方法与路径见[差异明细](gitea-api-differences.md)。

## 4. 核心能力矩阵

表内版本简写按从左到右依次为 1.22.6、1.23.8、1.24.7、1.25.5、1.26.4、1.27.3、28.3.0。✓ 表示该精确版本的 Swagger 定义存在此方法和路径；— 表示未在该定义中找到，不等同于已经证明运行时返回 404。路径省略前缀 /api/v1，R 表示 /repos/{owner}/{repo}。

| 能力 | 方法与路径 | 1.22 | 1.23 | 1.24 | 1.25 | 1.26 | 1.27 | 28 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 仓库详情 | GET R | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Issue 列表 / 创建 | GET / POST R/issues | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Issue 详情 / 编辑 | GET / PATCH R/issues/{index} | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Issue 普通评论 | GET / POST R/issues/{index}/comments | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Issue 添加标签 | POST R/issues/{index}/labels | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Issue 负责人增删 | POST / DELETE R/issues/{index}/assignees | — | — | — | — | — | ✓ | ✓ |
| PR 列表 / 创建 | GET / POST R/pulls | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| PR 详情 / 编辑 | GET / PATCH R/pulls/{index} | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| PR 文件 / 提交 | GET R/pulls/{index}/files；GET R/pulls/{index}/commits | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| PR 创建审查 / 请求审查 | POST R/pulls/{index}/reviews；POST R/pulls/{index}/requested_reviewers | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| PR 合并 | POST R/pulls/{index}/merge | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| PR 行评论解决 | POST R/pulls/comments/{id}/resolve | — | — | — | — | ✓ | ✓ | ✓ |
| PR 行评论回复 | POST R/pulls/{index}/comments/{id}/replies | — | — | — | — | — | ✓ | ✓ |
| Release 列表 / 创建 | GET / POST R/releases | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| 按标签查询 Release | GET R/releases/tags/{tag} | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| 编辑 Release | PATCH R/releases/{id} | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| 上传 Release 附件 | POST R/releases/{id}/assets | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| 分支 / 标签列表 | GET R/branches；GET R/tags | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| 提交列表 / 汇总状态 | GET R/commits；GET R/commits/{ref}/status | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| 服务版本 | GET /version | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |

## 5. 业务命令重点差异

### 5.1 Issue

**负责人。** 1.22.6 至 1.26.4 五个样本均有 PATCH R/issues/{index}，其 EditIssueOption 包含 assignees；但专用负责人 POST/DELETE 接口仅见于 1.27.3 和 28.3.0。1.22.6 的[服务端实现](https://github.com/go-gitea/gitea/blob/v1.22.6/routers/api/v1/repo/issue.go)注明该数组是完整替换，空数组会清空。

首版已确定七版本均须提供负责人增删。新版样本使用独立增删接口；旧版读取完整列表、计算用户要求的增量，再提交完整集合。该读改写不是原子操作，并发更新可能被覆盖；写前重读、进程内串行和写后核对只能减少或发现部分冲突，无法保证全局无丢失。预览说明旧版语义，出现可见冲突或不确定结果时返回对应错误，不自动覆盖重试。正常场景和并发边界都须逐版本测试，不能将“返回不支持”视为已完成适配。具体发布门槛见 TDD 第 7.3 节。

**模型字段。** EditIssueOption 的 content_version 在 1.26.4 样本中出现；CreateIssueOption、EditIssueOption 和 Issue 的 projects 在 1.27.3 样本中出现。Issue 的 time_estimate 在 1.25.5 样本中出现。客户端需要区分字段不存在和零值，写入时不得静默丢弃不支持的选项。content_version 是否具备所需乐观锁语义，以及与负责人操作的关系，必须单独验证，不能直接假定可解决负责人并发问题。

**列表。** 七个样本均声明 GET R/issues 的 type 参数；可请求 issues 以区分 PR。筛选条件、默认排序、分页和返回内容仍需跨版本验证。

### 5.2 Pull Request

**合并请求字段。** 在 1.22.6、1.23.8、1.24.7 和 1.25.5 的 MergePullRequestOption 中，Swagger 列出必填 Do 及 MergeTitleField、MergeMessageField、MergeCommitID；在 1.26.4、1.27.3 和 28.3.0 中，定义变为必填 do 及 merge_title_field、merge_message_field、merge_commit_id。

[1.25.5 源码](https://github.com/go-gitea/gitea/blob/v1.25.5/services/forms/repo_form.go)中的旧字段没有这些新名称的 JSON 标签；[1.26.4 源码](https://github.com/go-gitea/gitea/blob/v1.26.4/services/forms/repo_form.go)和 [28.3.0 源码](https://github.com/go-gitea/gitea/blob/v28.3.0/services/forms/repo_form.go)提供自定义 JSON 解码，同时接收新旧名称。因此这是**文档及字段模型变化，不能据此断言新版拒绝旧字段**。建议针对每个目标版本验证实际请求，采用相应版本文档的字段名；未知版本使用非破坏性的明确提示，不自动尝试多次合并。

**创建与查询。** CreatePullRequestOption 中 reviewers、team_reviewers 在 1.23.8 样本中出现，allow_maintainer_edit 在 1.26.4 样本中出现；PR 列表的 poster 在 1.23.8 样本中出现，base_branch 在 1.24.7 样本中出现。对于目标版本不支持的服务端过滤条件，应返回明确错误，或仅在结果、分页和排序语义均可保证等价时使用明确告知的客户端过滤。

**审查。** 普通讨论评论使用 Issue 评论接口；审查和行评论使用 PR 接口。解决行评论的专用操作只出现在 1.26.4、1.27.3、28.3.0 样本中；回复行评论的专用操作只出现在 1.27.3、28.3.0 样本中。不要用普通评论模拟代码行回复或解决，避免改变语义。

**内容版本。** EditPullRequestOption 和 PullRequest 的 content_version 在 1.26.4 样本中出现；冲突状态、必填规则和覆盖保护效果需要实例测试。PR 合并接口的 403 在 1.26.4 起的样本中被列入响应定义，这只说明文档列出的状态有变化，不证明旧服务不会返回 403。

### 5.3 Release 与开发上下文

CreateReleaseOption 的 tag_message 在 1.25.5 样本中出现；Release 列表的 tag_filter 只出现在 28.3.0 样本中。要求按标签筛选时，旧版不能静默忽略筛选；若考虑客户端过滤，必须覆盖所有必要页面并明确限制。

分支列表的 q 参数在 1.27.3 样本中出现；提交列表的 since、until 在 1.25.5 样本中出现。按标签查询 Release 后修改或上传附件时，需使用返回的 Release ID；Release、标签与附件的生命周期和权限均需分别验证。

### 5.4 认证和错误

七个样本的 Swagger 全局安全定义均列出 BasicAuth、Token、AccessToken、AuthorizationHeaderToken、SudoParam、SudoHeader 和 TOTPHeader；这不是本项目承诺支持所有认证方式，也不能证明每个实例对匿名访问、token scope 和权限的实际配置相同。保留 401、403、404、409、422、429、5xx 等原始 HTTP 状态和响应上下文，不把单一状态直接归因于版本差异。

## 6. 对 CLI 与 MCP 的设计建议

2026-11-06 决策补充：七个目标版本必须支持全部已冻结首版共同功能。以下未知版本和不支持字段的处理针对未列版本或共同基线之外的扩展；不得用它跳过七版本的必需功能。实施规则见 [TDD 第 7.3 节](technical-design.md#73-七版本必需支持的实施规则)，首次使用和确认见 [CLI 契约](cli-contract.md)，工具方案见 [MCP 设计](mcp-design.md)。

远端版本通过实例的 `GET /api/v1/version` 获取。七个目标版本的官方定义均声明该接口及响应模型中的字符串字段 `version`。根地址、反向代理子路径、认证、版本解析、缓存和失败处理的完整流程见 [TDD 第 7.1 节](technical-design.md#71-检测与适配)。这一接口返回的是服务器声明的版本，不能单独证明权限、功能配置或逐操作兼容性。

1. **能力表按操作和字段管理**：记录版本样本、方法路径、请求字段、筛选参数、兼容策略及验证证据。版本和实例配置仅用于选择已有证据支持的行为，不把静态定义等同于实例功能开关。
2. **通用 API 原样调用**：保留用户指定的方法、路径、查询、请求体和原始响应；不为通用入口自动改写字段或执行重复写请求。
3. **业务命令共享适配层**：CLI 和 MCP 复用同一操作实现，Issue 负责人、PR 合并、Release 筛选等差异集中处理；不支持时返回可识别的原因。
4. **未知版本安全降级**：版本查询失败时允许通用请求；专用写操作如无法保证语义，要求显式选择或返回不支持，不用猜测版本、静默忽略字段或用可能改变含义的操作替代。
5. **结果与安全**：兼容可选响应字段和未知字段；请求体区分未设置、空值及清空。对非幂等写入不因超时自动重试，提示结果未知并允许先查询状态。

## 7. 验证计划和当前状态

初始调研时七个版本均为**定义已比较、P0 环境演练通过、客户端兼容验收未完成**；后续阶段证据见本文末尾，不能把初始状态误读为当前实现状态。原验收计划包括：通用 API 与版本检测；Issue 创建、列表类型过滤、负责人新增移除与并发覆盖；PR 创建、普通评论、审查、按版本字段合并以及无权限／冲突；Release 创建、按标签查询、附件上传及筛选；MCP 对相同能力的调用。验证私有 CA、token scope、分页、写入超时和错误状态，并记录服务端镜像摘要、配置、账号权限及测试结果。

使用模拟服务器验证请求构造和错误路径；模拟测试不能替代上述真实实例测试。必要时补充各版本 OpenAPI 之外的发行说明和源码审查。

## 8. 复现差异报告

需要 Python 3 和 curl。以下示例将官方精确标签的定义下载到临时目录，并使用仓库内脚本生成明细：

~~~sh
mkdir -p /tmp/gitea-api-spec
for v in 1.22.6 1.23.8 1.24.7 1.25.5 1.26.4 1.27.3; do
  curl -fsSL "https://raw.githubusercontent.com/go-gitea/gitea/v${v}/templates/swagger/v1_json.tmpl" -o "/tmp/gitea-api-spec/gitea-swagger-${v}.json"
done
curl -fsSL https://raw.githubusercontent.com/go-gitea/gitea/v28.3.0/templates/swagger/v1-swagger.generated.json -o /tmp/gitea-api-spec/gitea-swagger-28.3.0.json
python3 scripts/compare-gitea-api.py /tmp/gitea-api-spec > docs/gitea-api-differences.md
~~~

重新生成后核对源文件 SHA-256；标签如被移动、网络返回了不同内容或脚本算法调整，先查明原因再更新结论。脚本不会连接 Gitea 实例或执行写操作。

## P3 实测进展（2026-11-06）

七个固定精确版本均已用同一最终 sotea 产物执行 Issue 创建／编辑／清空、评论、标签／负责人增量、筛选／分页、批量状态、PR 类型拒绝、确认边界及通用 API 文件传输。结果与镜像摘要见 [P3 记录](p3-issue.md)及 [机器证据](validation/p3-result.json)。该结果覆盖 P3 场景，不将七版结论外推到 PR／Release／完整 MCP、全部 API 或未验证的平台；本文早期静态比较记录保留为研究过程。

## P4 实测进展（2026-11-07）

同一最终 macOS arm64 产物已在七个精确版本完成专用 PR／Release 链路，包括旧版创建后审查请求、共同字段清空、用户／团队审查、三种事件与已有 pending 提交、改名 fork、五种合并方式、保护／真实冲突／无权拒绝，以及新旧标签、默认草稿、显式发布、空／二进制附件和同名保护。合并按精确版本采用 Do／do；列表关键词与共同筛选采用完整有界读取后本地筛选，不依赖新版本新增字段。

测试仅以隔离准备 API 和只读后台状态核验建立场景，业务操作均由专用命令执行。逐版策略、镜像摘要、清理与最终产物证据见 [P4 记录](p4-workflows.md)及 [机器证据](validation/p4-result.json)。完整 MCP、目标 OS／GUI 终端与未覆盖 API 仍按各自计划验收，不把此结论扩展为全部 API 一致。

权限补核：1.22.6 允许只读协作者请求审查，公开仓库非协作者的用户／团队请求被拒绝；拒绝可使用 422，并非固定 403。sotea 保留实际 http_status，不把权限／校验拒绝推断为版本不兼容；测试不以“只读账号对所有写 API 均无权”为前提。详细依据及结果见 P4 记录。

## P5账户目录实测（2026-11-07）

七个指定版本均已通过 `/user/repos`、`/user/orgs`、`/orgs/{org}`、`/orgs/{org}/repos` 和 `/repos/{owner}/{repo}` 的实际账户浏览链路。组织名称统一接收 `name` 与旧式 `username`，仓库通过正数ID与 `full_name` 核对，分页使用共同的page／limit参数。界面将 `/user/repos` 的范围中性称为Account repositories，不扩大解释为实例全部仓库。

每版均验证账户仓库及组织仓库进入工作台、仓库详情和返回原上下文，随机组织／仓库与token精确清理。分页、当前页筛选、身份不匹配、权限拒绝、重复ID及取消等边界由隔离API和PTY测试补充。证据见 [P5账户浏览记录](validation/p5-account-browser-result.json)，不外推为组织管理写操作或全部API兼容验收。

## 增量修改的验证范围

未涉及API路径、请求／响应契约、序列化、分页／筛选、认证协议和兼容策略的展示修改，不逐个版本重复Gitea集成验证；优先检查受影响包、终端交互和输出，必要时单实例冒烟。涉及API兼容的修改验证受影响版本，首次交付及正式发布仍按既定七版本矩阵验收。全局布局、Markdown、账号向导与草稿徽标增量未改变API线协议，保留此前七版API证据；本轮范围见 [新记录](validation/p5-guided-ui-result.json)。
