# P6 MCP 客户端配置向导

> 日期：2026-11-08；追加任务 P6-29 至 P6-32。真实客户端 UI、iTerm2／Terminal GUI 与 Windows 验收独立记录。

> 后续增量：本地访问的三模式和会话工作区跟随见 [p6-workspace-following.md](p6-workspace-following.md)；本文保留初次向导交付证据。

## 1. 范围与模块

Home → MCP integrations → 客户端 → User／Project → 配置路径 → 管理页。提供 Codex、Claude Code、Cursor、VS Code，安装、更新、仅移除自有项和 Check connection。无需当前目录是 Git 仓库，也不要求 Gitea 在线；已有保存的实例和账号是安装前置，未连接时指引 Home > Account。

| 模块 | 职责 |
| --- | --- |
| internal/cli/mcp_integrations.go | 分步输入、Esc 返回、独立权限选择、预览及明确确认、成功／错误和取消处理。 |
| internal/mcpintegration/client.go | 四客户端路径、非敏感启动参数；无工作目录不授权本地根。 |
| internal/mcpintegration/format.go | 完整 TOML 验证及托管块、JSON／JSONC 字节范围编辑；保留其他设置与注释。 |
| internal/mcpintegration/store.go | 托管状态、预览快照、协作锁、私有备份、日志和原子替换，拒绝同名非托管项与外部修改。 |
| internal/mcpintegration/check.go | 重验快照、临时 stdio 进程、20 秒上限、工具发现、关闭回收；不调用业务 API。 |

新增直接依赖 github.com/pelletier/go-toml/v2 v2.2.4，用于验证 Codex 完整 TOML，避免以局部文本匹配掩盖原配置损坏。版本已锁定，没有升级现有库、模块路径或 Go 1.27.3。

## 2. 行为与边界

- 初次安装写入与 raw 都关闭，默认不授权本地目录；更新展示原条目和新条目，不添加自动批准配置。仓库选择来自已有绑定或明确 owner/repo，不在安装时猜测可访问仓库。
- 使用稳定绝对二进制路径及显式 --config／--instance／--account，可选 --repo／--workdir；参数为数组，不拼 shell，不持久化 token。环境来源账号需要客户端自己的安全环境。
- 只处理本向导托管的 sotea 项；existing／changed 不接管。最终文件拒绝符号链接，配置最大 4 MiB，JSON 目标容器重复键和无效语法拒绝修改。
- 原配置存在时先在用户配置目录 sotea/mcp-backups 保存私有恢复备份，不把原文件中的潜在密钥副本放进项目仓库；托管日志记旧／新条目摘要后替换配置，异常中断可识别两份已知版本。取消或冲突不覆盖客户端文件；外部编辑器不参与锁，最后一次比较与替换间仍有竞争窗口。
- 删除后保留客户端文件、其他项、空服务器容器和日志，不擅自停止客户端正在使用的服务；需要客户端重新加载／停止。更改配置同样需要客户端重载，项目信任与批准不由向导代办。
- Check connection 验证当前托管启动参数和实际工具数量，成功不代表远端账号、客户端 UI 或人工批准已验收。临时子进程不继承 SOTEA_TOKEN／SOTEA_TOKEN_INSTANCE／SOTEA_ACCOUNT。

## 3. 验证

风险回归保护四格式安装／更新／移除、原设置与注释、JSONC 尾随逗号和首尾成员、私有备份、同名非托管冲突、预览后的外部配置／托管记录编辑、锁、取消、条目外部变更、日志恢复及最终路径链接。POSIX 子进程取消与凭证环境隔离单独验证。

真实 POSIX PTY 脚本 scripts/p6-integration-setup-smoke.py 使用临时 HOME／CODEX_HOME／CLAUDE_CONFIG_DIR 和合成账号，对四客户端的项目及用户配置执行完整导航：预览／取消、安装、27 工具只读检查、权限更新、32 工具检查、移除及返回 Home。Gitea 地址为不可连接的回环端口，未使用真实凭证或修改开发者客户端设置。

| 检查 | 当前状态 |
| --- | --- |
| 四格式及配置风险单元测试 | 通过；POSIX 子进程取消／回收、凭证环境隔离及受影响包 race 通过。 |
| 四客户端项目及用户范围 POSIX PTY／stdio | 通过；同一最终产物八场景完整闭环，均发现只读 27／完整授权 32 工具，见 [验证记录](validation/p6-integrations-result.json)。 |
| 全量 Go test／vet、三平台构建 | 通过；dist/p6-integrations/build-info.json 记录最终产物及源文件摘要，macOS arm64／amd64 与 Windows amd64 均编译通过。 |
| 四真实客户端重载、信任、工具调用与审批 UI | 待实测；本轮没有改写真实用户配置。 |
| iTerm2／Terminal GUI、Windows 10／11 | 待实测；POSIX PTY 与交叉编译不代替。 |
| 七版本 API 矩阵 | 未重跑；本轮未改变 API、认证协议、版本策略或 MCP 工具业务语义，保留原 P6 证据。 |

验证中修复：成功保存后的 Esc 曾被当作提交前取消并退回表单，已改为只关闭成功页；向导字段用显式默认项覆盖导航历史，重复确认永远默认 Back；TOML 标记块之外追加但仍归属同表的键会使托管状态失效。权限开关更新时终端复用未变化的标题，PTY 驱动按实际可见变化继续操作，不断言全屏重绘。

## 4. 官方配置依据

2026-11-08 读取以下官方页面，生成配置与其相应包装格式匹配；实际产品验收仍独立记录。

- [Codex MCP](https://developers.openai.com/codex/mcp/)：mcp_servers、用户／受信任项目配置、独立 command／args。
- [Claude Code MCP](https://code.claude.com/docs/en/mcp)：项目 .mcp.json、用户 .claude.json 及 mcpServers。
- [Cursor MCP](https://cursor.com/docs/context/mcp)：项目／全局 .cursor/mcp.json 及 mcpServers。
- [VS Code MCP](https://code.visualstudio.com/docs/copilot/customization/mcp-servers)：工作区／用户 profile 的兼容 servers 格式；不混用 portable mcpServers。

操作和恢复步骤见 [mcp-client-setup.md](mcp-client-setup.md)。构建使用 python3 scripts/check.py --phase p6-integrations，产物单独位于 dist/p6-integrations，保留原 dist/p6 与七版本证据的对应关系。
