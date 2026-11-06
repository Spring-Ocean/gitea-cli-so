# P2 上下文、认证与只读链路

> 日期：2026-11-06
>
> 状态：实现、本机自动化、七版本真实链路及 macOS 系统存储通过；Windows 运行与系统凭证仍待实机。
>
> 对应：[开发计划](development-plan.md)、[CLI 契约](cli-contract.md)、[MCP 设计](mcp-design.md)、[机器记录](validation/p2-result.json)。

## 1. 交付范围

| 任务 | 实现位置 | 交付内容 |
| --- | --- | --- |
| P2-01、02 | internal/context | 逐维解析工作目录、remote、实例、仓库和账号，保留来源；多 remote 不优先 origin，支持明确跨仓库目标、SSH authority 映射和无 Git 的显式 API。 |
| P2-03 | internal/cli/runtime.go | 未登记地址先匿名检测，展示有限版本证据后确认来源；随后才能取凭证。公开仓库可以匿名读取，读取成功后保存非敏感实例／仓库映射。 |
| P2-04 | internal/auth/vault.go | macOS Keychain／Windows Credential Manager 的系统适配器，配置仅存随机引用，无明文回退；macOS 实测通过，Windows 待实机。 |
| P2-05、06 | internal/auth | 环境、自有引用、映射的 HTTP helper 来源；明确 Basic 或 token，GET /user 核对实际 login，不把 oauth2 等 helper 用户名当 API 身份。 |
| P2-07 至 09 | internal/auth、internal/cli | login／status／switch／refresh／logout，stdin 与隐藏 token 输入、401 失效标记、403 保留、更新失败保护、持久化退出抑制及单独确认共享 helper 清理。 |
| P2-10、11 | internal/compat | 七精确版与后缀解析、证据分层、成功 5 分钟／失败 30 秒进程缓存、并发检测合并、隔离与刷新；instance info 共用服务。 |
| P2-12 | internal/app/repository | repo view、branch／tag／commit／label／milestone list、commit status 全部只读服务及 CLI。 |
| P2-13 | sotea api | GET／HEAD／OPTIONS、重复 query、非认证自定义头、原始状态／头／body；不要求版本检测成功。 |
| P2-14 | internal/mcp | 官方 SDK stdio 原型，只提供 sotea_instance_info，严格输入、structuredContent、isError、配置重读、取消与 EOF。 |
| P2-15 | scripts/p2-*.py、tests/integration | 七个精确实例的真实认证／仓库链路、系统存储、实际 stdio、PTY 与平台构建记录。 |

当前仍没有 Issue／PR／Release 业务命令、通用 API 写入／上传／下载、持续菜单、完整样式或完整 MCP 工具集。这些保留在后续阶段；当前能力以 --help 为准。无参数仍显示帮助。

## 2. 当前使用方式

~~~sh
# 在仓库目录首次连接，先查看候选根地址和版本，再确认来源。
sotea auth login --credential-source git --name team
sotea auth status --check
sotea instance info --refresh --json
sotea repo view --json
sotea branch list --limit 30
sotea commit status

# 仅在明确知道 helper 中保存的是 token 时选择该类型。
sotea auth login --credential-source git --auth-type token

# 无 Git 目录也可显式登录；交互 token 输入不回显。
sotea --instance https://git.example.com/gitea auth login --credential-source token --name team
sotea --instance team --repo acme/demo repo view --json

# 公开仓库不强制登录，显式来源确认用于非交互场景。
sotea --instance https://git.example.com --repo acme/public --confirm-instance repo view --json

# 原始读取不套业务 envelope；--include 才在 body 之前输出状态和响应头。
sotea --instance team api /version
sotea --instance team api /repos/acme/demo --include
sotea --instance team api /repos/acme/demo/issues --query labels=bug --query labels=help

sotea auth switch alice
sotea auth refresh
sotea auth logout --yes
~~~

公共参数已增加 --instance、--account、--repo、--remote、--config、--ca-file、--proxy、--yes 和 --confirm-instance。--confirm-instance 表示用户明确接受展示的服务根地址，--yes 仅用于普通操作确认，不能代替来源确认或接受身份不一致。--with-token 有界读取专用 stdin，最多 1024 字节；MCP 不提供该路径。非交互时必须提供所需信息，缺少信息报错。

--name 只给新实例命名，不重命名已有实例。登录不擅自设置全局默认实例；仓库映射仅在当前本地 remote 与已验证目标匹配时保存。--proxy direct 关闭代理，其余值为显式 HTTP(S) 代理；私有 CA 通过 --ca-file 指定，TLS 校验始终启用。

SSH 映射保存在实例 ssh_mappings 中，以 authority 为键，例如 git-alias 或 git.example.com:2222，值为完整 HTTP 实例根地址。SSH 账号和端口不用于推测 API 地址；没有映射时要求明确 --instance 和 --repo。MCP 未显式 --workdir 时不读取宿主当前目录的 Git 上下文。

## 3. 身份、退出及配置变更

API 引用绑定完整实例根地址与已验证账号；helper 引用另外保存实际协议、host、用户名、有效 path、认证类型和原始仓库配置目录。helper 记录不复制成独立 token，也不默认 approve。日常请求只查询所选来源，验证身份后才进入业务接口；显式账号 A 验证为 B 时立即停止，不能以 --yes 接受替换。

独立 token 先验证、存入新随机系统引用，再比较快照并保存配置；失败清理新引用，成功后才删除旧引用。替换 token 验证失败不污染未使用的旧凭证。401 只标记实际使用的保存来源失效，环境覆盖失败不能误标其他系统凭证；403 不删除或 reject。

退出先持久化抑制，再删除系统引用和账号记录。清理失败保留被抑制的引用供重试；退出当前账号后不会通过唯一账号或 helper 自动接替。退出其他非活动账号不改变当前活动身份。环境 token 属于父 shell／MCP 宿主管理，状态中的 environment_present 会如实展示；要停止该来源，应移除外部变量并重启继承旧环境的进程。

auth logout --forget-git 必须展示有效匹配范围，并另外确认；非交互使用 --confirm-forget-git 明确接受该范围。空 path 可能影响同主机其他仓库或工具，不能承诺只删除当前仓库的共享凭证。该操作不撤销服务器 token。

每次 CLI／MCP 应用调用重新读取配置。版本缓存按完整实例、验证后的账号、完整配置内容摘要、代理、CA 内容摘要与环境凭证指纹隔离，最多保存 128 项，不保存 token 原文或业务权限。版本检测默认 5 秒，成功／失败 TTL 分别 5 分钟和 30 秒；同 key 合并在途请求，等待者可独立取消。认证验证不使用这个版本缓存，轮换未改变引用时可显式 --refresh。

## 4. 分页、原始流与版本证据

列表默认 page=1、limit=30，上限 100；--all 从所选页向后读取，最多 10000 项，检测重复页。缺少明确服务端总数时 has_more 表示继续探测下一页，不能视为原子快照。超限或重复页报错，不把部分内容报告为完整成功。commit status 的默认值仅来自映射到同一目标的明确本地 HEAD，无 HEAD 时要求 REF。

原始 api 默认只输出 body，HTTP 非 2xx 返回非零并将安全诊断写 stderr；即使传 --json 也不改成业务 envelope。Host、Authorization、Proxy-Authorization、Cookie 由上下文／认证边界管理，P2 不允许自定义这些头。写方法、请求 body、上传下载与更完整通用 API 在 P3 接入。

版本只接受明确三段数字及合法后缀，原始字符串保留；最长 256 字节。仅七个精确正式字符串命中定义矩阵，开发版、自定义后缀和相邻补丁版保持未研究状态。instance_evidence=not_tested 是对当前目标实例完整业务验收的保守状态；版本请求成功和本阶段只读回归都不能证明全部 API 兼容。业务错误保留 HTTP 状态，不把 401／403／404 或 HTML 一律判为不兼容。

## 5. 依赖与 MCP 原型

| 依赖 | 锁定版本 | 用途与来源核对 |
| --- | --- | --- |
| github.com/zalando/go-keyring | v0.2.8 | 系统凭证；包根 MIT，Darwin 代码另含 Apache-2.0 版权头，发布时保留对应许可。macOS 使用 security 交互 stdin，不把密钥放 argv；Windows 使用 wincred。 |
| github.com/modelcontextprotocol/go-sdk | v1.8.0 | 官方 MCP SDK；本版根 LICENSE 说明 MIT 向 Apache-2.0 过渡，必须按代码许可及附带文本保留声明，不能简单写成全包单一 MIT。 |
| golang.org/x/term | v0.47.0 | 终端检测和模式；BSD 风格许可。实际单行输入通过可取消的本地终端读取，确保恢复状态。 |
| golang.org/x/sys | v0.49.0 | POSIX poll／Windows 控制台系统边界；确切版本见 go.mod／go.sum。 |

以上依赖以下载的精确模块源码、README／LICENSE 和实际构建核对，不把交叉编译当作最低 OS 证明。Windows 使用 Generic Credential API 及控制台事件接口，具体 Windows 10 22H2／11 的存储、解锁、取消与终端行为仍需实机。系统存储库使用同步系统调用，OS 解锁对话框能否被提前中止仍受系统行为限制，不能声称所有系统交互都可强行取消；错误不会触发明文回退。

~~~sh
sotea --config /path/to/config.json --instance team mcp serve
~~~

只发现 sotea_instance_info，输入为可选 instance／account／refresh，禁止未知属性；readOnlyHint=true。明确协商 2025-11-25 和 2025-06-18，默认最多四个并发诊断，输入帧上限 256 KiB。业务错误保留完整结构化结果并设置 isError，未知工具参数不能进入应用；stdout 只承载协议，EOF 自行退出。

这是 P2 内部原型，尚未提供其余读取工具、写入计划、任意 API、本地文件工具或 MCP 登录。Codex、Claude Code、Cursor、VS Code 的实际客户端验收仍属于 P6／P7，不由 SDK／stdio 测试替代。

## 6. 实测与复现

| 验证 | 结果与范围 |
| --- | --- |
| go test -race ./...、go vet ./... | 本机通过；上下文冲突、凭证范围、替换保护、退出抑制、401／403、JSON／原始流、版本缓存、严格 MCP Schema。 |
| 七个真实 Gitea 版本 | 1.22.6、1.23.8、1.24.7、1.25.5、1.26.4、1.27.3、28.3.0 全部通过 P2 链路；精确产物沿用 P0 固定镜像摘要。 |
| 每版认证链路 | 来源确认、Git helper 登录、status --check、账号切换、刷新、退出后新进程不调用 helper、明确重新登录、单独 reject、环境 token、系统 token 登录／刷新／清理。 |
| 每版只读链路 | 七类仓库开发上下文读取、当前 HEAD 的提交状态、原始 API、非仓库目录显式访问；不代表全部后续业务命令兼容。 |
| macOS 系统存储 | 随机合成项的 Set／Get／Delete 与七版独立 token 真实链路通过；测试 token 与系统引用均清理。 |
| 真实 stdio | 两协议初始化、工具发现、结构化结果、未知属性拒绝、错误后继续调用、配置修订缓存失效、EOF 无日志退出。 |
| POSIX PTY | 确认等待时 SIGINT、隐藏 token 输入 Ctrl-C、退出 130、输入不回显及终端模式恢复；比较排除 Darwin 内核瞬态 PENDIN 标记。 |
| 三个开发构建 | Go 1.27.3、CGO_ENABLED=0；darwin/arm64、darwin/amd64、windows/amd64 通过。只有本机 arm64 有本阶段运行证据。 |

~~~sh
go test -race ./...
go vet ./...
python3 scripts/check.py --phase p2 --build-only
docker compose -p sotea-p0 -f tests/integration/compose.yaml up -d
python3 scripts/p2-readonly-smoke.py --system-storage
python3 scripts/p2-mcp-smoke.py
python3 scripts/p2-terminal-smoke.py
~~~

回归仅使用隔离 P0 账号、临时本地仓库和新建测试 token；系统存储验证是显式 --system-storage 行为。脚本不启动或重置 Gitea 数据；产生的服务器 token 在 finally 撤销，系统引用在退出中清理，记录不含密钥。用户已继续授权 P3，本轮七个 P0 服务保留运行供后续 Issue 验证；不重置数据、不操作其他 Docker 项目，后续完成后再停止。

Windows 验证至少执行 scripts/windows-p1-smoke.ps1 -Binary dist/p2/windows-amd64/sotea.exe、包测试及 SOTEA_TEST_SYSTEM_VAULT=1 的独立凭证测试；还需在实际 Windows Terminal／PowerShell 中验证提示与取消。当前没有 Windows 10／11 或最低 macOS／Git 样本，P2-04、P2-15 和阶段整体保留实现通过／待实测，不能标成正式跨平台验收完成。
