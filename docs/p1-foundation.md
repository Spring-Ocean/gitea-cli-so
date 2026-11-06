# P1 基础能力与契约记录

> 日期：2026-11-06
>
> 状态：基础实现、本机自动化及 macOS arm64 冒烟通过；P1-12 与阶段退出保留 Windows 实测。
>
> 对应：[开发计划](development-plan.md)、[CLI 契约](cli-contract.md)、[机器记录](validation/p1-result.json)。

## 1. 已交付能力

| 任务 | 模块／入口 | 实际结果 |
| --- | --- | --- |
| P1-01 | internal/app | schema_version=1，公共目标、分页、逐项结果、稳定错误类别；整体 success／partial／failed／cancelled／unknown，逐项另含 skipped／not_executed。 |
| P1-02 | cmd/sotea、internal/cli | 组合根传递 context 与信号取消；帮助成功、参数错误 2、普通失败 1、部分完成 3、未知结果 4、取消 130；正常 Git 退出码原样保留。 |
| P1-03、04 | internal/config | 显式路径、SOTEA_CONFIG、默认路径；严格版本 JSON、1 MiB 上限、实例／账号引用／绑定／退出抑制、协作锁及内容摘要冲突检测。 |
| P1-05、06 | internal/git | 参数数组执行、透传与有界捕获；Git 版本、根目录、Git 目录、bare／分支／HEAD／remote；支持子目录、worktree 和空仓库。 |
| P1-07 | internal/git | credential fill／approve／reject 的有界协议封装，按仓库配置调用 Git；实际身份复用和验证在 P2。 |
| P1-08、09 | internal/gitea | 保留实例子路径、路径转义、重复 query、原始状态／头／body；认证回调、TLS／私有 CA、代理、取消、上限及禁止写入重放。 |
| P1-10 | internal/output | 基础文本与业务 JSON、诊断流分离、敏感字段／具体密钥／URL／认证头脱敏，保留大整数精度并清理文本控制字符。 |
| P1-11 | sotea git | 原始流和正常退出码透传，保留 version、帮助和补全；只注册已实现的公共参数。 |
| P1-12 | scripts/check.py | Go 1.27.3 测试／vet 入口、三个候选平台开发构建及 SHA-256；Windows 实机脚本已准备，尚未执行。 |

程序生成的帮助、错误、提示、确认、菜单、表头与 MCP 用户可见摘要统一使用英文，远端用户内容及原始 Git／API 流保留原文。该约束已写入需求、TDD、CLI 契约和本地忽略的 AGENTS.md；中文开发文档与 GoDoc 保持不变。

## 2. 使用与资源契约

~~~sh
go run ./cmd/sotea --help
go run ./cmd/sotea version --json
go run ./cmd/sotea --workdir /path/to/repo git -- status
python3 scripts/check.py
~~~

当前公共参数为 --json、--workdir、--no-interactive。Git 公共选项放在 git 之前；git 后的参数原样透传，只移除第一个可选分隔符 --。Git 透传不能套业务 JSON，且仍遵守 Git 自己的提示规则。无参数当前显示帮助，持续菜单在 P5 接入。--config 解析能力已在配置包提供，将随 P2 消费配置的命令注册。

配置快照必须由同一 Store.Load 取得后传给 Save。缺失文件不写盘；损坏、未知格式或未知字段不自动重置。保存采用独占 .lock、锁内重新读取摘要比较、同目录临时文件写入／同步／关闭后替换；失败不先删除旧文件。Windows 使用 MoveFileExW 替换路径，仍待实际系统验收。遗留锁需要核实后处理，不擅自抢锁。

锁只协调 sotea 写入者，外部编辑器不参与锁协议；读取比较与替换之间不具备面向所有进程的 CAS 保证，也不声称断电后目录项一定持久化。Snapshot.RevisionKey 提供内容摘要，长驻进程可重新 Load 后比较；文件监听、身份缓存失效等消费者逻辑属于 P2。普通配置只保存凭证引用，系统凭证存储在 P2 接入。

Git Capture 默认 30 秒、stdout／stderr 各 1 MiB；超限返回错误，内部捕获结果可能含 helper 密钥，禁止直接日志或序列化。默认禁止 Git／askpass／常见凭证管理器提示；显式交互 helper 仍有超时。POSIX 捕获清理独立进程组，透传保留前台终端关系；Windows 取消使用 taskkill /T /F，失败回退根进程 Kill，自行脱离的后代不在该策略的强保证范围。Windows 后代清理仍需实测。

HTTP 默认总预算 30 秒，包括重试及读取响应体；默认响应上限 1 MiB。调用者必须关闭 Body，并检查读取错误。ProxyURL 为空使用环境代理，direct 关闭代理，其他值为显式 HTTP(S) 代理。TLS 始终校验，显式 CA 追加系统信任；不提供不安全跳过验证选项。

读取重试默认关闭，可配置 0 至 3 次；仅无 body 的 GET／HEAD／OPTIONS 在传输失败或 502／503／504 时有限重试。只允许读取在同源 API 根下有限跳转；其他跳转保留原始 3xx。写请求使用独立 HTTP/1 连接、不保留可重放 body，不因幂等头自动重放；已发送后传输中断或响应读取中断报告 RESULT_UNKNOWN。响应超限单独报告 RESPONSE_TOO_LARGE，不能视为完整成功或据此自动重复写入。

## 3. 验证结果

| 检查 | 实际环境／行为 | 结果 |
| --- | --- | --- |
| go test -race ./... | Go 1.27.3、macOS 27.0 arm64；公共状态／退出码、配置损坏／冲突／取消、Git 缺失／上限／超时、捕获进程后代清理、隔离 helper、worktree、HTTP/TLS、脱敏／大整数。 | 通过 |
| go vet ./... | 同一最终 Go 源码。 | 通过 |
| python3 scripts/check.py --build-only | CGO_ENABLED=0、trimpath、注入 dev-提交号；darwin/arm64、darwin/amd64、windows/amd64。 | 三个构建通过；脚本明确未重复 test／vet，前两行独立记录其结果 |
| macOS arm64 产物冒烟 | 版本文本／JSON、帮助、无参数、JSON 参数错误 2、中文／空格目录、Git 原始错误 128、拒绝 Git 业务 JSON。 | 通过；macOS 27.0 build 26A428，Git 2.50.1 |
| 代码／语言检查 | 修改函数两行注释、多行声明及生产字符串巡检。 | 通过；无中文程序提示 |
| Windows 10／11、Intel macOS、最低 macOS／Git | 当前会话没有对应实际运行环境。 | 未测试；交叉编译不代替实测 |

测试只使用临时仓库、合成 helper 与可控 HTTP/TLS 服务，没有访问真实团队凭证。P0 七个 Gitea 服务未为本阶段重新启动；本阶段未重新执行七版本业务兼容验证。

## 4. Windows 验证与下一步

Windows 10 22H2 amd64 和 Windows 11 上分别执行以下命令，记录 OS、构建号、PowerShell 和程序摘要：

~~~powershell
powershell -NoProfile -File scripts/windows-p1-smoke.ps1
go test -race ./...
go vet ./...
~~~

仅运行预编译产物可先执行第一条，输出记录在 tests/integration/.state/windows-p1-result.json；后两条需要 Go 1.27.3 及相应 race 构建环境。冒烟脚本覆盖启动、中文／空格路径和 Git 退出状态，进程取消／helper／配置替换需结合包测试及真实终端补充验收，不能仅用脚本通过宣称全部平台能力完成。

P1-01 至 P1-11 在本机基础范围验收通过；P1-12 及阶段整体为实现通过／待实测。Windows 10 初始运行仍是未关闭退出项，Windows 11、最低版本和终端验证继续按平台计划追踪。后续可进入 P2 的上下文／认证独立工作，不能以缺少设备删除平台要求。
