# P4 PR 与 Release 完整业务链路

日期：2026-11-07。对应 P4-01 至 P4-18；工作区实现与验证结果独立归档，正式平台门槛不因本机测试通过而关闭。最终记录见 [p4-result.json](validation/p4-result.json)。

## 1. 模块与入口

PR 服务位于 internal/app/pullrequest，Release 服务位于 internal/app/release；两者通过 internal/app/write 共用固定实例／账号、配置修订、仓库锁、单次 JSON 写入和逐步骤结果。Issue 的标签／里程碑引用解析也供 PR 复用，普通文件输入移至 internal/fileinput，正文、通用 API 和业务附件采用同一特殊文件拒绝边界。

应用服务不依赖 Cobra、终端或 MCP 输出，不通过执行 Issue 命令来操作 PR。CLI 负责缺参补全、选择、诊断流预览和默认 No 确认，最终结果仍为 schema_version=1；JSON、CI 和无终端不等待输入，写入必须完整参数与 --yes。持续菜单和完整 MCP 保持 P5／P6 范围。

| 领域 | 已接通命令 | 关键行为 |
| --- | --- | --- |
| PR 读取 | list、view、files、commits | 状态／关键词／base／author；需要本地筛选时先完整有界扫描再分页，保留合并状态。 |
| PR 创建与编辑 | create、edit | 标题、正文／文件、负责人、标签、里程碑；编辑支持共同的 base 字段和明确清空，head 不可编辑。 |
| PR 状态 | close、reopen | 1–100 个去重目标，已达状态 skipped，已合并不可重开，批量保留失败与未执行项。 |
| PR 讨论与审查 | comment list／add、review list／create、reviewer add | 普通讨论和 PR 级审查分开；approve、comment、request-changes；显式用户／团队及可选待提交审查 ID。 |
| PR 合并 | merge | 默认 merge、不删源分支；显式方法需仓库允许，发送 head_commit_id，不提供 force 或隐含自动合并。 |
| Release | list、view、create、edit | 标签解析 ID；默认草稿／非预发布，布尔字段存在性明确，--publish 显式发布。 |
| 附件 | asset list、asset upload | 单个普通文件、显式名称、流式 multipart、同名拒绝；不删除／覆盖或自动发布。 |

## 2. PR 适配与安全边界

创建时非交互必须明确 --title、--base、--head；交互只显示当前分支／默认分支为候选，最终由用户输入确定。远端分支必须已存在，不自动 push。跨仓库支持 owner:branch 和更明确的 owner/repo:branch；客户端只在目标仓库及其有界可见 fork 列表中核对关联与实际仓库，不按同名仓库猜测 fork。预览显示实际源／目标仓库、分支和 SHA，提交前重新核对；API 仍使用其规定的 owner:branch 并核对创建后的实际仓库 ID。

1.22.6 不在创建请求中发送审查者字段，预览列出创建与补充审查请求两步。创建后核对字段再执行补充步骤；后续失败保留已创建 PR、成功步骤和未完成步骤，不重建。其他六个精确版本使用创建模型的原生 reviewers／team_reviewers 并读回审查请求。

PR 编辑的标签、负责人采用明确集合替换；里程碑 0 在旧版 PR PATCH 中会被忽略，因此清空使用已验证 PR 的共享 issues 资源独立步骤。步骤前重核里程碑，步骤失败保留此前已完成编辑；普通讨论也直接使用共享 comments 资源，但必须先通过独立 PR 类型核对。

审查请求权限遵循服务端规则：1.22.6 的只读协作者也可请求用户／团队审查者，不能用仓库 push 权限自行禁止这项操作；无权回归使用公开仓库的非协作者。

审查默认绑定准备时 PR head，也可 --commit 指定完整远端提交 SHA。comment 和 request-changes 必须非空正文，approve 可无正文，不实现代码行回复或解决。--review-id 提交已有 PENDING 审查时核对账号所有权、状态和提交；旧版提交接口会绑定当前 head，因此旧 head 的 pending 审查在发送前拒绝，不能声称按旧提交完成审查。

合并字段按精确版本序列化：1.22.6／1.23.8／1.24.7／1.25.5 使用 Do，1.26.4／1.27.3／28.3.0 使用 do。五种候选方式为 merge、squash、rebase、rebase-merge、fast-forward-only，均须仓库允许；不允许时不自动换方式。准备与提交重核仓库写权限，最终权限、保护规则和冲突仍由服务端裁决。

已读 head SHA 必须在提交前保持一致并作为 head_commit_id 发送；合并只发送一次。只读时也会异步变化的 mergeable 缓存、审查请求列表和更新时间不单独作为编辑锁，实际正文／字段、状态、分支与 SHA 仍受前置核对保护。合并响应失败后只读核对 PR，已合并而删除源分支未确认时报告部分完成；默认不删除，显式请求删除时单独核对实际源分支，不重放合并。

编辑的写前快照检查与进程内锁不能消除其他客户端在“重读后、写入前”的竞争；七版共同路径不宣称具备服务端原子条件更新。合并额外使用服务端 head_commit_id 条件；附件名称也存在独立的跨进程检查窗口。

## 3. Release 与附件生命周期

按完整标签名调用 releases/tags 获取 ID，不把数字标签解释为 ID。标签作为路径段编码，支持合法斜杠与 Unicode；不允许 Git 非法引用片段和控制字符。Release 与 Git 标签分别查询：无 Release 不代表无标签，草稿创建也不保证已创建实际 Git 标签。

create 默认 draft=true、prerelease=false，新标签必须明确 --target；已有标签可直接作为目标，显式 target 必须指向同一提交，不自动移动旧标签。目标分支／提交解析成完整 SHA 进入预览，提交前重新核对引用；发布后核对实际标签目标。创建同标签 Release 报冲突，不覆盖旧对象。失败后只读检查可能已创建的标签，保留可观察到的成功步骤，不声称完全无副作用或自动回滚。

edit 未提供的布尔值保持不变，--draft 与 --publish 互斥，--prerelease=false 可以明确取消预发布。名称／说明与发布均在预览中可见，上传不会隐式发布。七版共同模型不保证以空字符串清空 Release 名称／说明，因此显式空编辑在发送前拒绝，不能静默忽略；PR 和 Issue 的明确正文清空选项不受此限制。

附件准备时打开普通文件，记录文件身份、大小、修改时间与 SHA-256 后关闭。提交时重新打开并核对，再检查同名附件；文件变化在发送前拒绝。上传只发送单个 multipart attachment，流式计算实际发送字节摘要，并在结束后重核文件；成功返回附件但本地文件核验失败报告 partial，保留附件 ID，不自动补传或发布。

发送后断连／超时保留 unknown；主动取消以退出 130 优先，管道生产者在关闭后等待回收，不遗留无界后台任务。已成功上传的附件和草稿保持原样，同名拒绝不删除、覆盖或改名。附件接口没有跨进程的原子“名称不存在才上传”条件：写前检查和进程内串行可减少竞争，不能宣称其他进程不会同时创建同名附件。

## 4. 验证方式

本机定向竞态覆盖合并旧新字段、head 变化、服务器拒绝／断连不重放、一次性计划、旧版创建后补充失败、预览不可修改意图；附件覆盖准备后文件变化、同名保护、已接收后断连／取消、成功后文件核验失败及标签已创建后 Release 失败。

真实脚本 scripts/p4-workflow-smoke.py 在固定七版实例创建随机 sotea-p4- 组织／仓库和改名 fork，业务操作全部通过专用 CLI；API 负责分支／内容、组织／团队、保护规则等准备和结果读取／清理；测试侧以只读 SQLite 查询区分后台 checking／mergeable／conflict，核对 README.md 确实冲突，不把“稍后重试”算作保护或冲突验收。产品不读取服务端数据库。权限场景记录受限账号实际响应；服务端隐藏资源时保留原类别，不将 404 武断解释为资源真实不存在。1.22.6 的审查请求权限／校验拒绝使用 HTTP 422，当前共享错误模型保留 SERVER_ERROR 与 http_status=422；不能把它改称 403、版本不支持或网络故障。场景包括共同字段与清空、三种审查及已有 pending 提交、用户／团队请求、批量与类型拒绝、五种合并方式、无权／保护／真实冲突、默认不删和显式删分支、特殊标签、草稿、空／二进制附件、同名拒绝、已有标签和发布。

测试账号仍为 P0 合成账号。组织准备额外使用每版固定的 p4-bootstrap 隔离 fixture：其测试管理员身份只服务组织／团队准备，合成密码保存在受忽略、权限 0600 的状态文件；不提升原有 P0 账号权限，不修改团队实例。Gitea 不允许删除最后一个管理员，该 fixture 作为验证环境组成部分保留并明确记录。随机业务组织、主仓库与 fork 在每轮 finally 按精确名称清理，失败也保留清理状态，不将 fixture 账号留存伪装成账号已删除。

1.25.5 起部分仓库配置修改需要同时明确 has_pull_requests；测试先读回确认合并方式配置生效。各合并方式使用独立目标分支，避免前一合并引发的后台重检查影响后一场景；创建后的异步检查采用有界只读轮询。保护规则前确认 PR 本身可合并，冲突场景先核对后台确认为 README.md 冲突，再验证专用命令拒绝；不重放失败合并。七版真实结果、请求字段策略和最终源码／产物摘要在机器记录中逐项保存；28.3.0 另核验未推送／相同 head 拒绝，恢复 1.22.6 后补核空审查正文、用户／团队请求无权限、Release 无权限及目录／不存在／不可读附件输入均不新增资源。

新入口另由 scripts/p4-selector-smoke.py 在真实三流 POSIX PTY 验证：PR 明确多选、Release 完整 Unicode／斜杠标签单选、Enter 不默认选择、确认前零写入、取消 130 和终端恢复。P3 的跨页／跨搜索选择及 Issue 真机服务链路也使用最终 P4 产物回归。

~~~sh
python3 scripts/check.py --phase p4
python3 scripts/p4-workflow-smoke.py
go test -race ./...
~~~

Docker 容量不足时沿用 P3 的固定镜像主机归档／摘要核对及项目内轮换，保留全部卷，不执行全局 prune；环境恢复见 [P3 记录](p3-issue.md)。Windows 10／11、Intel／最低 macOS、真实 GUI 终端与 Harness 仍按既定平台矩阵保留待实测，交叉编译或共享选择器的 POSIX PTY 不替代实机验收。本轮已盘点到 Terminal／iTerm2，但 GUI 控制工具拒绝访问 com.apple.Terminal，未取得宿主操作证据；不将应用已安装视为实测通过。

## 5. 对照依据

- [1.22.6 PR 路由实现](https://github.com/go-gitea/gitea/blob/v1.22.6/routers/api/v1/repo/pull.go)：创建／编辑、合并和共享 Issue 字段边界。
- [1.26.4 PR 路由实现](https://github.com/go-gitea/gitea/blob/v1.26.4/routers/api/v1/repo/pull.go)：新合并字段及兼容行为；客户端仍按精确版本序列化。
- [1.23.8 PR 状态模型](https://github.com/go-gitea/gitea/blob/v1.23.8/models/issues/pull.go)与[合并检查](https://github.com/go-gitea/gitea/blob/v1.23.8/services/pull/check.go)：API mergeable 的布尔表示与后台状态有区别，测试据此单独核对冲突检查完成；拒绝不自动重试。
- [1.22.6 审查请求校验](https://github.com/go-gitea/gitea/blob/v1.22.6/services/issue/assignee.go)与[审查路由](https://github.com/go-gitea/gitea/blob/v1.22.6/routers/api/v1/repo/pull_review.go)：无权选择用户／团队审查者等校验错误可返回 422，客户端保留实际 HTTP 状态。
- [1.22.6 Release 实现](https://github.com/go-gitea/gitea/blob/v1.22.6/routers/api/v1/repo/release.go)与[附件实现](https://github.com/go-gitea/gitea/blob/v1.22.6/routers/api/v1/repo/release_attachment.go)：Release／标签／附件是独立步骤，静态源码不替代七版实测。
