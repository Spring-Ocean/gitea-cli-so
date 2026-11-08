# P6 客户端项目工作区跟随

> 日期：2026-11-08；追加任务 P6-33 至 P6-35。Go 1.27.3，未升级依赖或协议。

## 1. 用户行为

Home → MCP integrations → 客户端 → User／Project → Set up integration／Update integration。在实例、账号之后选择本地访问模式：

| 选项 | 作用 |
| --- | --- |
| Follow client workspace | 同一客户端配置在不同项目启动时使用当前会话目录；首次安装焦点位于此项，仍须预览确认。 |
| Choose a fixed directory | 只授权明确指定的目录，仍可配置默认 owner/repo。 |
| Remote tools only | 不访问本地 Git／文件，保留全部已授权远端能力。 |

跟随模式不写固定 --workdir／--repo／客户端 cwd。当前项目 Git remote 提供仓库上下文，工具仍可显式指定远端目标；非 Git 目录可用作文件根，无需克隆仓库，但本地 Git 工具会报告实际非仓库错误。实例／账号仍按已保存选择固定，不会为了匹配另一个项目而自动切换凭证。

更新既有接入时必须进入向导选择 Follow，原有固定模式不会无提示迁移。保存后在客户端重启该 MCP 服务；新项目新会话产生独立进程或独立会话授权。Codex 进程启动目录和 Claude 项目环境是本次进程的固定来源，同一已运行进程不会因 shell cd 自动迁移目录。

## 2. 模块与授权

internal/mcp/workspace.go 在工具入口建立会话视图，与固定根复用 fileinput.Scope。MCP roots/list 提供唯一可用本地根时优先采用；Codex 根列表为空的行为在本机实际验证，因此向导为 Codex 写入 --workspace-source codex，以客户端 thread cwd 启动 MCP 的实际边界为依据。Claude Code 写入 --workspace-source claude，通过官方提供的 CLAUDE_PROJECT_DIR 在进程启动时记录项目。普通 --follow-workspace 没有这两个显式适配器时必须依赖 roots。

客户端配置只记录模式与适配器，不记录当前项目绝对路径。明确 --workdir 与跟随模式互斥；本地根固定后，工具的 workdir 参数只能缩小到根内。Cursor／VS Code 使用相同 roots 逻辑，真实客户端传递行为尚未验收，无法获取唯一根时远端继续、本地明确报错。

多根不合并父目录、不自动选首项；远程 URI host、非目录、根目录／主目录、UNC、查询／fragment 或不可解析路径不自动授权。不存在、根变化、根通知、目录实体替换和会话差异均进入不同随机 workspace_binding，写计划不能跨边界提交。会话一旦采用 roots，其根撤销后不会退回旧启动适配器。撤销对后续调用生效，在途调用保持开始时快照；旧根句柄直到 Host.Close 统一释放，最多保留八个根以界定资源。既有单根文件／快照预算继续适用。

MCP 工具清单不扩充：跟随模式注册 git_context，所以只读跟随为 28 工具，写＋raw＋跟随为 32。目录未解析时保留该工具并返回具体授权错误，避免启动时联网或需要客户端已初始化。菜单 Check connection 只发现工具，不能替代实际会话目录调用。

本轮未新增“全部工具”权限预设或敏感操作分类，它们属于单独的权限设计；写／raw 仍独立选择，不改变现有批准契约。

## 3. 验证范围

| 检查 | 结果 |
| --- | --- |
| Go test／vet、三平台构建 | 通过；产物 dist/p6-workspace，build-info.json 记录源和产物摘要。 |
| 受影响包 race | 通过；internal/mcp、mcpintegration、cli。 |
| SDK 两会话真实 Git 查询／越界／旧计划 | 通过；不同项目、根变化恢复、缺失根／多根远端读写仍可用、Claude adapter 与 roots 撤销不回退。 |
| 跟随配置四格式持久化 | 通过；无固定项目参数，不扩大写／raw，互斥固定目录与固定 repo。 |
| 真实 Codex CLI 0.167.0 | 同一用户级配置、服务启动 cwd 与项目不同；两个 thread 分别使用项目 A／B，git_context 与 context_get 的目录／仓库匹配，跨项目访问拒绝。无模型回合、无 UI 批准验证。 |
| 真实 Claude Code 2.1.318 | 同一隔离 User 配置，从两个项目 mcp list 均 Connected，实际子进程收到对应 CLAUDE_PROJECT_DIR；未调用模型或业务工具，不代表工具 UI 验收。 |
| POSIX PTY | Codex 项目级及四客户端用户级共五场景通过：跟随安装／取消／检查／更新固定目录／移除；只读跟随 28 工具、完整权限 32 工具。 |
| Cursor／VS Code 实际工作区调用 | 待实测；配置序列化或 SDK roots 测试不能代替实际客户端。 |
| iTerm2／Terminal GUI 与 Windows 10／11 | 待实测；交叉编译及 POSIX PTY 不等同实际目标宿主。 |
| 七版本 Gitea | 未重跑；本轮不改 API 路径、字段、认证协议或版本兼容规则，保留 P6 原版本证据。 |

本轮总证据见 [p6-workspace-result.json](validation/p6-workspace-result.json)。真实客户端证据见 [p6-workspace-clients-result.json](validation/p6-workspace-clients-result.json)，用户级向导证据见 [p6-workspace-menu-result.json](validation/p6-workspace-menu-result.json)。

## 4. 依据与执行

已使用 openai-docs 技能核对官方 [Codex MCP 文档](https://developers.openai.com/codex/mcp/)；cwd 字段本身不能证明默认 thread 启动目录，因此以实际 app-server 双项目验证补齐依据。没有使用把 cwd 设为固定绝对路径的配置。

Claude 项目环境与 roots 来源依据为 [Claude Code MCP 官方文档](https://code.claude.com/docs/en/mcp)，读取日期 2026-11-08。协议仍为原来的 2025-11-25／2025-06-18，未扩展到其他 MCP 协议版本；roots 在更新协议中的弃用由后续单独适配。

验证命令：

~~~sh
python3 scripts/check.py --phase p6-workspace
go test -race ./internal/mcp ./internal/mcpintegration ./internal/cli
python3 scripts/p6-workspace-client-smoke.py
python3 scripts/p6-integration-setup-smoke.py --binary dist/p6-workspace/darwin-arm64/sotea --scopes user --follow --result docs/validation/p6-workspace-menu-result.json
~~~

测试只使用临时 HOME／CODEX_HOME／CLAUDE_CONFIG_DIR、临时 Git 仓库和不可连接的回环 API 地址，不读取或改写开发者客户端配置、不使用真实 Gitea 凭证。
