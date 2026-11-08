# MCP 客户端接入

> 更新日期：2026-11-08
>
> 适用：P6 本地 stdio 工具；实际证据见 [p6-mcp.md](p6-mcp.md)。配置示例不等于所有客户端已验收。

## 1. 菜单配置向导与认证

打开 sotea，在 Home 选择 **MCP integrations**。此入口不要求当前目录存在可用仓库，MCP 服务仍由客户端自动启动并管理。

1. 选择 Codex、Claude Code、Cursor 或 VS Code，再选 User 或 Project 范围。Project 先输入项目目录；随后显示配置文件绝对路径，可为已知客户端 profile 明确修改位置。
2. Set up integration／Update integration 依次选择已保存的实例和可用账号、本地访问模式，再按需要设置默认仓库。模式包括 Follow client workspace、Choose a fixed directory、Remote tools only。跟随模式不保存固定路径：Codex 使用 --workspace-source codex 和客户端项目启动目录，Claude Code 使用 --workspace-source claude 和 CLAUDE_PROJECT_DIR／roots；固定模式保存绝对目录；Remote tools only 不开放 Git／文件根。跟随模式跳过并清除固定 repo，从当前项目 Git remote 推导仓库，远端工具仍允许每次显式提供 owner/repo。
3. Business writes 和 Raw API 是两个独立开关，首次安装都为 Off。两者均开才允许原始 API 写入；不修改客户端自动批准策略。
4. 查看 Review MCP configuration，核对目标文件与完整启动条目。更新／移除同时展示 Current entry 和 Proposed change；再选择 Install／Update／Remove integration 明确提交。默认 Back，--yes 不跳过这次确认。
5. 管理页提供 Check connection。它临时启动已托管配置中的 sotea，只做初始化与 tools/list，20 秒超时，完成后关闭并回收进程；不调用业务 API、不读取系统凭证或继承 SOTEA_TOKEN。成功会显示实际工具数量，不能据此断言客户端 UI、远端认证或审批策略已通过。

没有已保存账号时，先在 Home 的 Account 向导完成绑定；不在 MCP 服务内登录。环境来源账号仍需客户端安全继承其运行环境，向导不会把 token 写进文件，也不会把 CLI 当前环境冒充图形客户端的环境。

### 1.1 默认配置位置

| 客户端 | Project | User |
| --- | --- | --- |
| Codex | 项目/.codex/config.toml | CODEX_HOME/config.toml，未设置时 ~/.codex/config.toml |
| Claude Code | 项目/.mcp.json | ~/.claude.json；设置 CLAUDE_CONFIG_DIR 时为该目录内 .claude.json |
| Cursor | 项目/.cursor/mcp.json | ~/.cursor/mcp.json |
| VS Code | 项目/.vscode/mcp.json | 标准用户 profile 的 Code/User/mcp.json，基于系统用户配置目录 |

VS Code 命名 profile、Portable／Insiders 及其他自定义目录须明确填写其实际文件位置。客户端可能要求项目受信任、批准服务或重新加载配置；向导不会替用户批准。VS Code 官方文档当前将 servers 格式列为兼容格式，向导保留该明确支持的格式，不自行变更已有客户端配置方案。

### 1.2 保留设置、冲突与恢复

只改服务器集合中的 sotea：Codex 使用带 BEGIN／END 注释的独立托管块；JSON／JSONC 只编辑目标成员的字节范围，保留其他字段、服务器、注释和排版。已有同名但非本向导托管的条目不会被接管；外部修改自有条目后状态变为 changed，恢复原条目及对应托管记录或改用另一个范围，不能通过强制覆盖绕过。

每个目标文件旁保存 filename.sotea-mcp.json，只记录本地条目摘要与目标，不包含凭证。保存前后以内容快照检查并发修改，以同目录临时文件同步后替换；协作锁为 filename.sotea-mcp.lock。外部编辑器不参与锁，最终比较与替换之间仍有小的竞争窗口。原文件存在时先在系统用户配置目录的 sotea/mcp-backups 内创建 0600 的 .sotea-mcp-backup-* 恢复副本，避免把可能含旧密钥的副本放进项目仓库，并在成功页显示路径；备份可能含客户端原有敏感设置，须保持本地私有。托管日志先落盘，异常中断后旧／新两个已知条目均可被识别，重读后再处理。

移除只删除自有 sotea 条目，保留其他服务器、空容器、原文件及托管日志，不结束客户端已运行的服务。需要在客户端停止／重载对应服务。恢复备份前先退出相关客户端，核对备份与当前文件；不要覆盖之后新增的其他设置，也不要单独手改托管日志来接管未知命令。

向导是本地配置管理，不增加 Gitea API 或 MCP 工具。最新工作区跟随实现与真实客户端边界见 [p6-workspace-following.md](p6-workspace-following.md)。跟随工作区的会话边界、根变化和计划失效规则见 [MCP 设计第 12 节](mcp-design.md#12-客户端项目工作区跟随)。其隔离 POSIX PTY／stdio 证据见 [p6-mcp-integrations.md](p6-mcp-integrations.md)，不能替代四客户端及 Windows 真实验收。

### 1.3 手动接入

使用 macOS／Windows 预编译 sotea，可执行文件本身同时提供 CLI 与 MCP。先在普通终端使用既有 auth login 流程确认实例根地址并验证账号，再由客户端启动 sotea mcp serve。MCP 不读取协议 stdin 登录，不显示终端菜单；每个客户端管理自己的子进程，不需要另外运行菜单进程或 HTTP 守护服务。

配置中的 command 使用二进制绝对路径；args 是独立参数数组，不使用 shell 拼接。Windows 使用 C:\Tools\sotea\sotea.exe 一类真实绝对路径，JSON／TOML 中按各自语法转义反斜杠。实例配置通过 --config 显式指定或复用正常配置；不把 token 写入项目文件、参数或普通配置。优先使用 CLI 已验证的系统凭证；环境来源由宿主安全注入 SOTEA_TOKEN，必要时同时注入 SOTEA_TOKEN_INSTANCE／SOTEA_ACCOUNT，不能把“环境存在”当成已验证身份。

图形客户端不一定继承当前终端环境。首次连接失败时先确认程序路径、配置、私有 CA／代理及所选账号；运行 auth status --check 验证身份。更换外部环境 token 后重启客户端的 MCP 服务，已生成计划不能跨进程恢复。

## 2. 模式与超时

| 启动配置 | 提供能力 |
| --- | --- |
| mcp serve | 默认 27 个远端只读／诊断工具。 |
| 增加 --allow-write | 增加 prepare／commit／status；20 种业务 action。 |
| 增加 --allow-raw-api | 增加 api_read；GET／HEAD／OPTIONS。 |
| 两项均增加 | 原始 API 写入也可通过计划提交，合计 21 种 action。 |
| Follow client workspace | 按唯一客户端项目根增加 git_context；Codex／Claude 由明确适配器或 roots 提供目录，根缺失时远端仍可用。 |
| 任一模式明确 --workdir 绝对目录 | 增加固定 git_context；文件动作仅限该根，仍须对应写／raw 权限。完整模式 32 个工具。 |

默认读取／准备 30 秒，普通写入 60 秒，上传／原始 API 提交 300 秒；客户端的工具调用超时应略大于服务上限，例如 360 秒。服务参数 --read-timeout／--write-timeout／--upload-timeout 可按实际环境调整；--max-output-bytes 上限 262144，默认包含结构化内容和完整 JSON 文本副本。单请求文件默认总量 64 MiB，快照总量 256 MiB，可分别通过 --max-file-bytes／--max-snapshot-bytes 调整。

默认只读最适合首次接入。--allow-write 只表示允许服务提供写工具；客户端是否显示逐次批准、是否自动执行，由该客户端策略决定。confirmation_token 不是人工审批证明。准备结果必须展示实例、账号、目标、所有实际修改及预检失败项，确认后提交原 plan_id／token；不能让远端正文中的指令替用户批准。

## 3. Codex

配置使用 ~/.codex/config.toml，或当前官方支持且已信任项目中的 .codex/config.toml。下例首先保持只读；需要本地 Git 时才明确给 --workdir，写入和 raw 分别追加开关。

~~~toml
[mcp_servers.sotea]
command = "/absolute/path/to/sotea"
args = ["--config", "/absolute/path/to/config.json", "--instance", "team", "mcp", "serve"]
startup_timeout_sec = 20
tool_timeout_sec = 360
~~~

环境 token 确需继承时可使用官方支持的 env_vars 显式列出变量名，真实值由客户端启动环境提供；不要把密钥写进 TOML。实例、账号、repo 可以由工具显式传入；无 --workdir 不从 Codex 启动 cwd 推断用户仓库。复用一个实例时可在 args 中增加 --account 和 --repo 作为启动默认，调用时显式字段仍优先。

2026-11-08：Codex CLI 0.167.0 的实际 app-server 在临时工作区完成 32 工具发现、联合 Schema 加载、诊断／身份读取、prepare／commit／status 及一次业务请求的重复提交保护。该验证通过 app-server MCP 转发完成，没有模型回合；不证明自然语言参数生成、真人审批 UI 或取消 UI 通过。

## 4. Claude Code

项目级 .mcp.json 使用 mcpServers 包装；下面保持只读。可以先通过 claude mcp add-json 或当前客户端支持的命令加入，实际项目批准与工作区信任仍按 Claude Code 处理。

~~~json
{
  "mcpServers": {
    "sotea": {
      "type": "stdio",
      "command": "/absolute/path/to/sotea",
      "args": ["--config", "/absolute/path/to/config.json", "--instance", "team", "mcp", "serve"]
    }
  }
}
~~~

2026-11-08：Claude Code 2.1.318 在临时 CLAUDE_CONFIG_DIR 与工作区中通过实际 mcp list 健康检查，显示 Connected；未修改用户配置、未使用实际 Gitea 凭证或调用模型。完整业务调用、联合 Schema 参数生成、逐次批准与取消／EOF UI 仍待实测。--mcp-config 是可变长全局参数，不能把后面的 mcp list 误吞为配置路径；本轮验证使用隔离目录内 add-json／list。

## 5. Cursor

项目配置 .cursor/mcp.json，或用户级 ~/.cursor/mcp.json，使用 mcpServers 包装。项目配置不保存凭证。示例：

~~~json
{
  "mcpServers": {
    "sotea": {
      "command": "/absolute/path/to/sotea",
      "args": ["--config", "/absolute/path/to/config.json", "--instance", "team", "mcp", "serve"]
    }
  }
}
~~~

在客户端 MCP 设置中确认连接、工具清单和批准策略；需要文件上传时明确添加 --workdir／--allow-write。已识别本机 Cursor 3.27.6，但本轮未完成其 UI 工具调用验收。注意本机 /usr/local/bin/code 实际指向 Cursor，不能据此把 Cursor 的结果记成 VS Code。

## 6. VS Code

VS Code 当前官方文档区分 portable 的 mcpServers 和 VS Code 格式的 servers。下例是已明确支持的 VS Code 工作区 .vscode/mcp.json 格式；当前官方向导可能将其标记为兼容格式并提供其他目标，按所用版本向导生成的位置维护，不直接复制其他客户端包装字段。

~~~json
{
  "servers": {
    "sotea": {
      "type": "stdio",
      "command": "/absolute/path/to/sotea",
      "args": ["--config", "/absolute/path/to/config.json", "--instance", "team", "mcp", "serve"]
    }
  }
}
~~~

通过当前版本的 MCP 管理界面检查服务器信任、启动、工具与批准。已通过真实应用路径识别 VS Code 1.143.0；完整接入和业务／审批／取消 UI 尚未实测。macOS 本机 CLI 的可靠路径是 /Applications/Visual Studio Code.app/Contents/Resources/app/bin/code，不能使用当前指向 Cursor 的 code 别名作为证据。

## 7. 使用与恢复

读取：context_get／account_repo_list／account_org_list → org_repo_list（明确组织名与 ID）→ repo_view（可传 expected_repository_id）→ Issue／PR／Release。选择返回的 repo／account 后显式传入下一次调用，服务不保存菜单式当前仓库。

写入：先查目标 → write_prepare → 展示完整预览 → 客户端批准 → write_commit → write_status。相同 request_id 的相同意图返回原记录；不同意图冲突。prepared 默认 5 分钟有效，执行接纳后不因该期限到达中断；原结果在进程内可重复查询。准备失败、过期或前置条件失效后使用新 request_id，不能复用旧 ID 悄悄生成另一计划。

| 情况 | 恢复动作 |
| --- | --- |
| CONTEXT_REQUIRED／CONTEXT_AMBIGUOUS | 显式传 instance／account／repo；本地目录由启动 --workdir 授权，不打开终端选择器。 |
| AUTH_REQUIRED／AUTH_IDENTITY_MISMATCH | 在正常终端验证并刷新正确账号，重连 MCP；不把 token 放进工具参数。 |
| PLAN_EXPIRED／PRECONDITION_CHANGED | 重查远端与本地身份，展示新意图后用新 ID 准备；旧计划不自动更新。 |
| executing | 查询原计划，不启动替代写入；等待者取消不取消其他调用持有的执行。 |
| partial／unknown／RESULT_UNKNOWN | 保留成功项、失败项、资源 ID 与未执行步骤；查远端确认，写入不自动重试。 |
| NOT_FOUND 且客户端重启过 | 记录只属于旧进程；按远端资源查证，不承诺跨进程幂等。 |
| RESOURCE_LIMIT／RESPONSE_TOO_LARGE | 查询既有记录；缩小列表 limit 后从 page=1 重读，或使用 CLI 处理完整大内容。不能为了清空未知结果直接重放写请求。 |
| 断网／token 在远端过期 | 同进程 status 不要求远端在线；本地身份／配置绑定变化时仅返回最小状态，避免旧身份数据外泄。 |

正常 EOF／关闭会取消活动请求，最多十秒收尾并删除本进程私有快照。崩溃／强制终止不能保证临时文件清理，也没有持久化计划；客户端不应把杀进程理解成远端回滚。

## 8. 文档来源与验收边界

2026-11-08 核对官方页面及本机 CLI 帮助：

- Codex：developers.openai.com/codex/mcp/（当前重定向至 learn.chatgpt.com/docs/extend/mcp?surface=cli）；配置及 app-server Schema 另由 0.167.0 实际工具核对。
- Claude Code：code.claude.com/docs/en/mcp；2.1.318 mcp help／add-json／list。
- Cursor：cursor.com/docs/mcp；3.27.6。
- VS Code：code.visualstudio.com/docs/agent-customization/mcp-servers；1.143.0。

四类真实客户端完整通过条件仍见 [开发计划 P6](development-plan.md)；本说明不把配置示例、产品版本、SDK 消息或单次健康检查称为完整客户端验收。
