# P0 隔离验证环境

本目录准备首版所需的 Gitea 实例和测试资源。当前环境演练验证的是基础设施，不表示尚未实现的 sotea 业务命令或 MCP 已经兼容。

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
