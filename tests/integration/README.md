# P0 隔离验证环境

本目录准备首版所需的 Gitea 实例和测试资源。P0 环境演练仅代表基础设施；P6 已完成七版本 MCP 代表性业务验证，具体产物和范围见 docs/p6-mcp.md，不能外推到未验收平台或客户端。

## 文件与隔离范围

| 文件 | 用途 |
| --- | --- |
| compose.yaml | 固定项目 sotea-p0、七个服务及各自命名卷；端口仅绑定 127.0.0.1。 |
| images.lock.json | 实际获取并核对的精确版本、镜像摘要和观察到的架构；Compose 已使用摘要固定镜像。 |
| scope.json | 首版命令、MCP 工具及 action 到模块、需求和开发任务的映射。 |
| .state/ | 自动生成的合成账号、测试 CA、私钥及原始演练结果；整个目录被 Git 忽略。 |
| ../../scripts/p0-environment.py | 初始化、读取核对、停止、固定仓库重置及项目清理。 |
| ../../scripts/p0-fixtures-smoke.py | 隔离 helper、HTTP 故障及私有 CA 的最小演练。 |

开发验证依赖 Python 3、Git、Docker Engine／Compose，TLS 演练还需 OpenSSL。这些是开发工具，不是未来预编译 sotea 程序的用户运行依赖。本次演练在 macOS arm64 和 Linux arm64 容器完成，其他平台尚未执行这些脚本。

## 启动与初始化

以下命令均在仓库根目录执行：

~~~sh
docker compose -p sotea-p0 -f tests/integration/compose.yaml config --quiet
python3 scripts/p0-environment.py prepare --rounds 2
python3 scripts/p0-fixtures-smoke.py
~~~

prepare 启动所选服务，等待版本入口就绪，检查声明版本与目标完全相同，再创建三个合成账号。每轮删除固定的测试仓库后重新初始化并只读核对；默认两轮，第二轮用于验证重置及重复使用。只有 prepare 会进行完整数据初始化，不应在希望保留手工修改的测试仓库上随意运行。

只处理某个版本：

~~~sh
python3 scripts/p0-environment.py prepare --versions 1.22.6 --rounds 2
~~~

| 版本 | 本机端口 | Compose 服务 |
| --- | --- | --- |
| 1.22.6 | 32206 | gitea-1-22-6 |
| 1.23.8 | 32308 | gitea-1-23-8 |
| 1.24.7 | 32407 | gitea-1-24-7 |
| 1.25.5 | 32505 | gitea-1-25-5 |
| 1.26.4 | 32604 | gitea-1-26-4 |
| 1.27.3 | 32703 | gitea-1-27-3 |
| 28.3.0 | 32830 | gitea-28-3-0 |

各版本使用独立的 /data 命名卷和 SQLite 数据库，不挂载生产数据；每个服务设置 512 MiB 内存上限。镜像当前观察架构是 arm64，不将该结果直接当作 amd64 主机或其他 OS 的测试证据。

## 最小数据集

| 资源 | 初始化内容 | 后续用途 |
| --- | --- | --- |
| p0-owner | 非管理员、拥有 p0-owner/sotea-p0 私有仓库。 | 正常读写与账号验证。 |
| p0-reviewer | 仓库 write 协作者。 | 多账号、负责人及审查请求。 |
| p0-limited | 无该私有仓库访问权。 | 权限失败；初始化核对接受实际 403 或 404。 |
| Issue | 3 个，其中 1 个 closed；附负责人、标签、里程碑及普通评论。 | page limit=2 的两页查询、类型区分、状态及关联资源。 |
| PR | p0-feature 与 p0-conflict 两个源分支的 PR。 | 文件／提交／审查候选；README 分叉形成冲突数据，main 配置必需状态检查。 |
| Release | p0-v0.0.1 草稿和一个 p0.txt 附件。 | 标签到 ID、草稿及附件读取／冲突场景。 |

初始化核对检查账号身份、仓库访问、Issue 类型与两页数量、两个 PR 可读取、草稿及附件存在，并核对 clone URL 使用对应本机端口。合并是否被保护规则拒绝、PR 审查／写入、多版本业务适配等属于后续业务阶段，不能由数据存在推断已验收。

合成密码随机生成，只存在 .state/credentials-<version>.json 和隔离数据库中。脚本捕获账号创建输出和 HTTP 认证，不打印密码或 Authorization。请不要把 .state/ 加入版本控制、发布包或共享报告。若只删除命名卷却保留凭证文件，脚本会尝试使用原合成密码重新创建账号；已存在账号但密码不同会明确失败，需检查该隔离环境。

## 读取、停止与清理

初始化后只读核对现有数据：

~~~sh
python3 scripts/p0-environment.py verify
~~~

暂停全部 P0 服务并保留数据；再次 up 不会重置数据：

~~~sh
python3 scripts/p0-environment.py stop
docker compose -p sotea-p0 -f tests/integration/compose.yaml up -d
python3 scripts/p0-environment.py verify
~~~

reset 只删除固定的 p0-owner/sotea-p0 和 p0-reviewer/sotea-p0 仓库，保留账号与实例；执行前确认该环境内的手工改动可以清理。需要重新建数据时运行 prepare。

~~~sh
python3 scripts/p0-environment.py reset --versions 1.22.6
python3 scripts/p0-environment.py prepare --versions 1.22.6
~~~

彻底删除全部 sotea-p0 容器、网络和命名卷，同时移除各版本本地合成密码文件：

~~~sh
python3 scripts/p0-environment.py down
~~~

down 不接收其他项目名或远端 URL；它保留历史演练报告和测试 CA，不触及 Docker 其他项目。手工清理前先核对项目名，禁止用全局 prune 代替本项目清理。

## helper、HTTP 故障与 CA

隔离 helper 位于 ../../testdata/p0/credential-helper.py，通过固定命令配置给临时仓库，不读取开发者全局 Git 配置或系统密钥库。演练覆盖 fill／approve／reject 的 get／store／erase，以及 useHttpPath 开关。日志仅保存协议、主机、用户名、路径和密码字段是否存在，返回值为合成凭证。

HTTP 夹具位于 ../../testdata/p0/http_fixture.py，仅监听随机 loopback 端口，支持 /ok、/status/<code>、/delay?seconds=0.3、/disconnect 和 /events。事件只保存方法、路径和 body 长度，可用于后续验证写请求是否被重放。延迟最长两秒，事件最多保留 100 项。

TLS 演练每次生成独立测试 CA 和 loopback SAN 证书，保存至 .state/ca/，有效期七天；私钥限定为当前用户读取。它验证默认拒绝未知 CA，以及显式提供 CA 后能成功连接，不修改系统信任库。需要重新生成时再次运行 p0-fixtures-smoke.py。

## 结果与诊断

每个版本的 .state/result-<version>.json 包含实际摘要、架构、版本和逐轮资源编号；fixtures-result.json 保存 helper／HTTP／TLS 结论。脱敏汇总见 [p0-readiness.md](../../docs/p0-readiness.md)，单次验证模板见 [validation-record.example.json](../../testdata/p0/validation-record.example.json)。

端口占用、镜像下载失败或版本不符均保留失败状态，不自动改端口、改版本或访问其他实例。若账号创建步骤失败，只检查该 Compose 服务的局部诊断；不要把含密钥的配置或完整日志拷入共享报告。

## P4 PR／Release 回归

~~~sh
python3 scripts/check.py --phase p4
python3 scripts/p4-workflow-smoke.py --versions 1.22.6
python3 scripts/p4-selector-smoke.py
~~~

脚本使用 P0 合成账号与隔离 helper，在明确版本实例创建随机 sotea-p4- 组织、主仓库及改名 fork。PR 和 Release 业务通过专用 CLI 执行；初始化 API 只准备组织／团队、分支、内容和保护规则，finally 按本轮精确名称删除业务资源。各合并方式使用独立目标分支；先确认 PR 可合并再配置保护规则，冲突场景通过仅限本轮仓库的只读 SQLite 状态及 README.md 冲突文件核验后验证拒绝。数据库读取仅用于隔离测试的准备核验，产品不依赖它；不重放失败的业务写请求。

组织／团队准备需要每版独立的 p4-bootstrap fixture 管理员，密码留在 .state/p4-bootstrap-<version>.json（0600）。该合成 fixture 属于隔离验证环境并明确保留，不能修改原有 P0 账号权限或拿真实团队账号代替；Gitea 不允许删除最后一个管理员，不应尝试绕过该限制清理数据库。随机组织、仓库和 fork 的清理状态与 fixture 保留分别记录。

Docker 磁盘受限时按 [P3 记录](../../docs/p3-issue.md)核验主机镜像归档、轮换本项目指定服务并保留卷，不执行全局 prune。--versions 可分批运行，合并证据前必须核对产物摘要一致。最终 P4 证据见 [P4 记录](../../docs/p4-workflows.md)与 [机器记录](../../docs/validation/p4-result.json)；未具备的目标 OS／终端宿主仍保持待实测。

## P5 菜单与终端

使用 scripts/check.py --phase p5 构建，scripts/p5-terminal-smoke.py 验证 VT／静态菜单、会话／配置、未知结果、输出和交接；scripts/p5-gitea-menu-smoke.py --versions VERSION 在固定实例上验证真实菜单。--result 可按版本隔离记录，合并时必须核对同一二进制摘要。真实脚本只创建随机 sotea-p5- 前缀资源和独立 token，finally 按精确 ID 清理。

镜像沿用 P3 已核对的主机归档，在本项目内轮换 1.22.6 与 28.3.0，保留所有卷，结束恢复原驻留镜像并停止服务；不清理其他项目。PTY 记录不替代 Windows 或 GUI 宿主验收。详见 [P5 交付记录](../../docs/p5-terminal-experience.md)。

### P5 持续界面修订回归

2026-11-07 根据用户 iTerm2 反馈改为 Enter 导航、Esc 返回和单一屏内结果。运行 p5-terminal-smoke.py 验证 VT／静态完整会话、默认 No、unknown 不重放、配置并发、Git／API 原始流交接及终端恢复；VT 增加持续加载中取消、resize、单一 alternate-screen 生命周期和结果滚动检查。p5-gitea-menu-smoke.py 使用同一产物，在七个固定隔离版本执行登录、候选、拒绝、关闭、Release 草稿／附件与精确清理。

导航与单资源使用 Enter（静态降级输入编号即进入），多选资源才用 Space。p4-selector-smoke.py 相应保留 PR 无默认勾选和 Release 明确接受焦点标签，不再把单选 Enter 当作无效输入。修订证据见 [p5-menu-revision-result.json](../../docs/validation/p5-menu-revision-result.json)，与原 p5-result.json 分开归档；不能用 PTY 代替 iTerm2／Windows 实机验收。

### P5 首页与全部业务详情修订

首页资料和统一可读详情的回归扩展了现有脚本：p5-terminal-smoke.py 检查 API 返回的姓名／邮箱、Repository 分组、菜单 API JSON 数组／未知字段／大整数和脱敏，以及非 JSON API 原始交接；p5-gitea-menu-smoke.py 在每个固定版本先核对 API 用户和仓库概览，再执行原业务链路及清理。标准业务 --json 和直接 api 的原始字节／文件边界由原有测试继续保护。

本轮最终证据见 [p5-home-display-result.json](../../docs/validation/p5-home-display-result.json)，包含同产物七版本、完整终端会话与清理记录。

### P5 分组分页与账户浏览

`p5-terminal-smoke.py` 增加固定语义色、主要／辅助／返回键位层级、PgUp／PgDn 与 Tab／Shift+Tab 切组、左右键不切组、长组滚动及 resize。`p5-browser-smoke.py` 用隔离 API 验证账户目录分页、当前页筛选、组织工作台、上下文恢复、本地 Git 隔离和外部配置变更后的请求阻断。

`p5-gitea-menu-smoke.py` 为七版本各自增加随机个人仓库、组织和组织仓库的真实浏览链路；使用既有隔离 `p4-bootstrap` 账号创建组织，将 `p0-owner` 加入该随机组织，不修改测试账号全局权限，清理前再次核对资源 ID。记录仍绑定同一二进制摘要，不以旧结果替代当前产物验证。

### P5 全局页面与账号向导验证

`scripts/p5-guided-account-smoke.py` 使用临时 Git helper、本地合成 API 和临时配置，验证隐藏输入、取消不保存、配置变化阻断凭证查询／可重新开始、预览不保存、明确绑定、浏览续接、条件草稿徽标及 Markdown 正文。无真实开发者凭证，不修改远端资源。可选 `--screenshots PATH` 使用独立验证依赖 pyte 0.8.2 与 Pillow 12.0.0 从合成 PTY 渲染 PNG；不操作 GUI，不代表 iTerm2 验收。

~~~sh
python3 scripts/p5-guided-account-smoke.py --binary dist/p5/darwin-arm64/sotea
uv run --with pyte==0.8.2 --with pillow==12.0.0 python scripts/p5-guided-account-smoke.py --screenshots tests/integration/.state/p5-ui-screens
~~~

七版本真实菜单脚本的 Account 登录改为四步向导、显式保存和继续；保留原有资源确认与精确清理。最终记录与旧版产物分开归档，未具备的 Windows／GUI 环境保持待实测。


P6 可通过 scripts/p6-mcp-smoke.py 逐版运行真实 stdio 工作流，每次只启动一个固定实例，验证所有 21 action 代表性操作、目录／读取、CLI 对照、重复提交和定向清理；停止后只移除可重建容器层，保留数据卷与镜像。scripts/p6-client-smoke.py 使用回环合成服务和隔离客户端配置验证真实 Codex 转发／Claude Code 健康检查，不执行模型任务，不代替四客户端完整 UI 验收。脱敏结果位于 docs/validation/p6-*.json。
