# P6：MCP 完整工具与客户端接入

> 更新日期：2026-11-08
>
> 状态：实现、自动验证与七版本最终产物回归通过；四类客户端完整 UI／真实 Windows 和目标 macOS 验收仍待实测。
>
> 依据：[MCP 设计](mcp-design.md)、[P6 计划](development-plan.md)、[客户端说明](mcp-client-setup.md)

## 1. 交付范围

同一 sotea 可执行文件提供 mcp serve。P2 单诊断原型已由完整 Host 替代：默认 27 个远端读取工具；--allow-write 增加 3 个计划工具；--allow-raw-api 增加 api_read；明确 --workdir 增加 git_context，完整模式 32 个工具。21 种写 action 覆盖 Issue 9 种、PR 8 种、Release 3 种及原始 API 1 种。登录、退出、菜单、任意 shell 与 Git 写入不映射为 MCP 工具。

工具通过明确 instance／account／repo 或启动默认调用；未授权本地根时不发现客户端 cwd。账户／组织目录没有“必须先选仓库”的依赖，不建立服务端隐含活动仓库。默认只读、原始 API 和写入分别授权；服务不能将 confirmation_token 或工具 annotations 视为真人审批证明。

## 2. 实现位置与复用

| 模块 | 结果 |
| --- | --- |
| internal/app/session | 无 Cobra／终端／SDK 依赖的上下文、认证组合和版本证据服务；CLI 与 MCP 共用，逐调用重读配置，缓存／写锁由进程复用。 |
| internal/mcp/host.go、schema.go、read.go | 严格工具注册、权限模式、全部只读映射、固定 Git、执行槽／排队／超时、stdio 生命周期。 |
| internal/mcp/prepare.go、registry.go | 原领域 Plan 注册、规范化意图摘要、随机计划／凭据、5 分钟准备期限、并发 prepare／commit 去重、status、身份与仓库 ID 前置校验。 |
| internal/mcp/result.go | 共同 envelope、MCP meta、isError、结构化结果与相同 JSON 文本副本、最终消息预算、完整目标／步骤状态和结果缓存脱敏。 |
| internal/app/api；internal/mcp/api.go | CLI 与 MCP 共用路径／头／query、正文／multipart 构造与单次发送；CLI 保持原始流，MCP 增加固定请求预览、授权文件与有界结果。 |
| internal/fileinput/scope.go；Release 文件入口 | os.Root 授权文件打开、symlink 根边界、普通文件／大小检查、私有固定内容快照和配额；授权在实际文件使用位置生效。CLI 默认普通文件行为保留。 |
| internal/gitea/client.go | 非读取发送边界观察，记录 write_attempted；已有 HTTP 写入不重放和断连 unknown 语义继续复用。 |
| scripts/check.py、p6-mcp-smoke.py、p6-client-smoke.py | P6 三目标构建、七版本实际 MCP 流程、真实 Codex MCP 转发及 Claude Code 健康检查；实际证据与最终产物摘要绑定。 |

所有新增命名 Go 函数有中文函数头说明并使用多行排版。保持 Go 1.27.3、原模块路径及锁定依赖，没有新增或升级库。MIT 及 springocean 署名未变更。

## 3. 写入与资源契约

prepare 验证身份、版本、目标及实际字段，仅发送读取请求；保留原领域 Plan，完整预览放不下时不给可提交 token。批量预检混合失败保留所有原始目标及固定可执行子集；全部失败不给可提交计划。prepare 相同 request_id／意图复用原计划，不延长期限；不同意图冲突。登记后的准备失败保留终态，重试新准备使用新 ID。

commit 只接受原 plan_id／confirmation_token；错误 token 不消费计划。真实仓库 ID、配置／账号、文件及领域关键状态重核后，由唯一执行者调用原 Plan；不重新 Prepare，不在 MCP 外层重复获取领域仓库锁。重复提交返回 executing 或已保存结果；完成结果不因原准备期限届满而丢失。partial／unknown 保留资源 ID、成功／失败／跳过／未知／未执行与步骤，不自动重试或补偿。

status 独立于长上传执行槽，不要求远端在线。配置／身份绑定变化只返回最小状态，不暴露旧身份预览或结果。服务重启无计划恢复，也不承诺跨进程幂等；应查远端后决定新意图。

| 边界 | 默认值／语义 |
| --- | --- |
| 协议 | go-sdk v1.8.0；2025-11-25／2025-06-18；stdout 仅 JSON-RPC。 |
| 输入／输出 | 输入帧 256 KiB；最终 CallToolResult 256 KiB，含结构化内容、英文摘要和完整 JSON 文本副本。 |
| 列表 | page=1、limit=30，最大 100；不暴露 all；共享版本策略可有界完整扫描后分页。 |
| 并发与队列 | 4 读取／准备槽，4 commit 槽，最多 64 个排队调用；队列等待计入超时。 |
| 超时 | 读取／准备 30 秒，普通写 60 秒，上传／原始 API 提交 300 秒；客户端配置应覆盖该上限。 |
| 注册表 | 100 未完成计划、1000 总终态索引／记录、64 MiB 缓存；prepare 预留终态槽和最小结果空间，未知结果不淘汰。 |
| 文件 | 显式单根，单请求文件总量 64 MiB，快照合计 256 MiB；MCP 可配置，CLI 原有能力保留。 |
| 关闭 | EOF／取消后主动取消调用与排队，最多 10 秒收尾；崩溃／强杀不保证清理，不意味着远端回滚。 |

原始 API 的请求预览固定完整请求和文件摘要，不自动探测任意业务端点，也不承诺通用对象并发保护；显式合法条件头原样发送。凭证、身份代理与 framing 覆盖被拒绝，跨来源不泄露认证，写入不跟随重定向重发。非 UTF8 响应先遮蔽已知凭证再编码为有界 base64，不能借编码绕过脱敏。

## 4. 自动化与真实实例验证

阶段记录见 [验证汇总](validation/p6-result.json)与 [客户端记录](validation/p6-clients-result.json)。本轮 macOS arm64 SHA256：5fae34a53decc6165080215182193251b49e03a5e879cb28e705bcf72740aa04。

已完成 Go 全量 test／vet、MCP／共享业务／文件／HTTP／CLI 的 race 检查、darwin/arm64、darwin/amd64、windows/amd64 构建。本机真实 stdio 验证默认／写／raw／完整模式工具发现、严格参数、业务失败后继续、完整 JSON 文本副本和 EOF；风险回归覆盖重复并发提交、错误 token、期限／身份／request_id 冲突、部分预检、断连 unknown、文件变化／symlink、配额、预览预算、二进制脱敏、固定 Git 禁用 fsmonitor 和关闭时取消排队。

七版本脚本使用随机组织／仓库和 P0 合成账号，通过两个真实 stdio 进程完成所有 21 动作的代表性业务、全部远端读取工具、CLI 仓库 ID 对照、重复 commit／status 和 EOF。准备夹具可使用直接 HTTP，业务结果必须通过 MCP；报告不保存 token／计划确认凭据。七个精确版本 1.22.6、1.23.8、1.24.7、1.25.5、1.26.4、1.27.3、28.3.0 已全部通过并完成定向清理，见 [七版本记录](validation/p6-mcp-result.json)。

测试中发现并处理的环境／夹具问题：

- Docker 虚拟磁盘满导致 1.22.6 启动失败；仅删除本项目已停止且可重建的容器层，保留所有数据卷、镜像及其他项目缓存／容器，之后实例恢复。
- 同一 PR 立即 close → reopen → merge 会受后台异步状态影响，1.24.7 返回 405；状态转换与合并改用独立 PR，并以有界只读等待检查稳定状态，没有自动重放合并。
- 所有测试资源按本轮随机名字和稳定 ID 核对后定向清理，token 定向撤销；不清空 P0 仓库或开发者凭证。

## 5. 真实客户端与平台状态

| 客户端／环境 | 已有证据 | 尚未验收 |
| --- | --- | --- |
| Codex CLI 0.167.0 | 实际 app-server：32 工具及联合 Schema 发现、诊断／身份读取、prepare／commit／status、重复提交只发送一次；没有调用模型或修改用户配置。 | 自然语言参数生成、真人批准 UI、客户端取消交互。 |
| Claude Code 2.1.318 | 隔离 CLAUDE_CONFIG_DIR／工作区，实际 add-json／mcp list 显示 Connected。 | 完整业务调用、联合 Schema 参数生成、批准及取消／EOF UI。 |
| Cursor 3.27.6 | 真实应用版本与官方配置格式已核对。 | MCP 工具发现、业务／审批／取消 UI。 |
| VS Code 1.143.0 | 真实应用路径和版本已核对；本机 code 别名是 Cursor，未混用证据。 | MCP 工具发现、业务／审批／取消 UI。 |
| macOS arm64 当前主机 | Go 验证、最终二进制、真实 stdio、Git／文件及上述客户端部分链路。 | 候选最低 macOS、Intel 真实运行及尚缺 GUI 场景。 |
| Windows 10 22H2 amd64／Windows 11 | 同一 windows/amd64 构建通过。 | 真实 Windows 凭证、路径／junction／UNC、子进程、stdio／客户端；不能用交叉编译代替。 |

客户端脚本使用回环合成 HTTP 服务验证 Harness 传输，不将其当作七版本 Gitea 实测。四类客户端的完整验收仍未通过，P6 阶段保持“实现通过／待实测”，P6-16 不能标验收通过。没有自行修改用户的客户端配置、执行模型任务、提交或推送。

## 6. 重现与下一步

~~~sh
python3 scripts/check.py --phase p6
python3 scripts/p6-mcp-smoke.py
python3 scripts/p6-client-smoke.py
go test -race ./internal/mcp ./internal/app/... ./internal/fileinput ./internal/gitea ./internal/cli
~~~

Gitea 脚本依赖既有 P0 七版本固定镜像／数据卷和合成账号；不使用生产实例替代。客户端脚本要求已安装的实际 Codex／Claude Code，macOS 产品路径按脚本核对。文档／UI／纯协议摘要等未涉及 API 的后续修改按影响范围验证，不每次重跑七版本。

下一步：完成四客户端的 UI／批准／取消矩阵与真实 Windows／目标 macOS 验收，再由 P7 汇总同一发布候选产物；环境和证据缺项继续保留在开发计划，不能用配置说明关闭。
