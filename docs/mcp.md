# MCP 接入指南

sotea 内置本地 stdio MCP 服务，由 AI 客户端启动并管理。配置完成后，无需另开终端运行菜单或维持后台守护进程。CLI、菜单和 MCP 使用同一套 Gitea 业务能力。

## 通过菜单配置

先在普通终端完成 [账号绑定](configuration.md)，并确认 `sotea auth status --check` 成功。然后运行 `sotea`，进入 **MCP integrations**：

1. 选择 **Codex**、**Claude Code**、**Cursor** 或 **VS Code**。
2. 选择 **User** 或 **Project** 范围，核对配置文件位置。User 供多个项目复用，Project 写入指定项目。
3. 选择 **Set up integration** 或 **Update integration**，依次选择实例、账号及本地访问模式。
4. 按需要开启 **Business writes**、**Raw API**；默认均为 Off。
5. 指定安装后可长期使用的 sotea 绝对路径，查看 **Review MCP configuration** 的目标文件和变更。
6. 明确确认安装／更新，在客户端中重载或重启该 MCP 服务。

向导保留其他服务器与设置，不修改客户端的自动批准策略，也不把 token 写入配置。已有同名但非向导托管的 sotea 项会报告冲突，不会强行接管。

Codex 向导只添加 `[mcp_servers.sotea]` 子表，保留已有的 `[mcp_servers]` 和其他服务器。已发布的 `v0.1.0-rc.1` 在配置含显式父表或相关点分键时可能错误报告 `CONFIG_CONFLICT`／“layout cannot be safely extended”；这是 sotea 的序列化缺陷，失败发生在保存之前。`v0.1.0-rc.2` 包含此修复；无需为此删除正常父表或现有服务器。

## 本地访问模式

| 选项 | 行为 | 适合的场景 |
| --- | --- | --- |
| Follow client workspace | 跟随客户端提供的项目根，不写死当前目录或仓库；每次调用仍可以明确远端仓库。 | 经常切换项目的用户级接入。 |
| Choose a fixed directory | 固定授权目录内的 Git 查询和允许的本地文件输入。 | 固定项目或明确的文件访问范围。 |
| Remote tools only | 不开放本地 Git 查询或文件读取。 | 只操作远端仓库、Issue、PR、Release。 |

这里的工作目录是本地访问边界，**不是 sotea 配置或缓存的存储目录**。固定目录不必本身就是 Git 仓库，但 Git 查询必须能在授权范围内定位仓库；目录内允许的文件可用于正文或附件。

跟随模式优先使用客户端提供的唯一有效项目根。Codex 和 Claude Code 的向导配置还使用各自明确的项目来源适配；多个根、无效根或没有可靠来源时，本地调用会提示无法授权，远端功能仍可继续。不能用工具参数扩大到父目录或其他项目。

切换工作区、账号或配置后，旧的未提交写入计划可能失效，需要重新读取目标并准备。当前进程不会因为 AI 执行了一次 shell `cd` 就任意改变授权根；打开新项目时应由客户端建立对应的服务会话。

## 权限

| 设置 | 提供能力 |
| --- | --- |
| 默认 | 远端只读与诊断。 |
| Business writes | 通过 prepare／commit／status 执行 Issue、PR、Release 的既有业务写操作。 |
| Raw API | 原始 API 读取；同时开启 Business writes 才允许原始 API 写入。 |
| 有效的本地访问模式 | 授权根内固定 Git 读取与允许的文件输入。 |

全部现有 MCP 能力需要相应本地访问授权和两个独立权限开关。当前没有额外的“删除操作专用”权限开关；写入范围仍受 Gitea 账号权限、业务校验及客户端批准控制。MCP 不提供任意 shell 或任意 Git 写命令。

写入流程为：读取目标 → `sotea_write_prepare` → 展示预览 → 客户端批准 → `sotea_write_commit` → `sotea_write_status`。服务端无法仅凭一次工具调用证明人工已批准；应同时核对客户端的批准设置。

## 配置位置与维护

下面是向导生成的默认位置；自定义 profile 可以在向导中明确指定真实文件路径。

| 客户端 | Project | User |
| --- | --- | --- |
| Codex | `.codex/config.toml` | `CODEX_HOME/config.toml`，未设置时为 `~/.codex/config.toml` |
| Claude Code | `.mcp.json` | `~/.claude.json`；设置 CLAUDE_CONFIG_DIR 时使用该目录内 `.claude.json` |
| Cursor | `.cursor/mcp.json` | `~/.cursor/mcp.json` |
| VS Code | `.vscode/mcp.json` | 系统用户配置目录下的 `Code/User/mcp.json` |

**Check connection** 临时启动已托管的 sotea，仅检查 MCP 初始化和工具发现，不调用 Gitea 业务或读取实际凭证。连接成功后，仍需要在客户端尝试读取仓库来验证身份和上下文。

**Remove integration** 只移除向导托管的 sotea 条目，保留其他服务器；客户端中已经运行的服务还需停止或重载。更新配置前会保留本地私有备份，保存页显示位置。配置文件旁的 `.sotea-mcp.json` 是条目所有权记录；不要手改它来强行接管外部条目。

## 手动启动参数

需要自行维护客户端配置时，将 sotea 绝对路径填写为 `command`，把选项作为独立 `args` 元素。下面是对应的启动命令示例；stdio 用于协议通信，不作为普通菜单启动。

```sh
sotea --config /absolute/path/config.json --instance team --account username mcp serve
```

固定工作目录并开启全部现有 MCP 能力：

```sh
sotea --config /absolute/path/config.json --instance team --account username --workdir /absolute/path/project mcp serve --allow-write --allow-raw-api
```

跟随项目使用 `mcp serve --follow-workspace`，与固定 `--workdir` 互斥。通过向导生成客户端特定参数，避免把不受保证的宿主当前目录当作项目授权。

Windows 使用实际 exe 的绝对路径；JSON／TOML 中按语法正确转义反斜杠。不要把整条 shell 命令放到 command，也不要把 token 直接写进 command、args 或项目文件。

## 故障与恢复

| 情况 | 处理方式 |
| --- | --- |
| 客户端找不到程序 | 核对绝对路径和执行权限，避免指向已删除的解压目录。 |
| Codex 添加时提示 “layout cannot be safely extended” | `v0.1.0-rc.1` 可能重定义正常的 `[mcp_servers]` 父表；使用包含修复的版本。真正的不可扩展内联容器会被配置格式检查拒绝并保留原文，可改用支持的表结构或另一配置范围。 |
| connection 成功但读取失败 | 用普通终端检查账号、实例、仓库权限、CA 和代理；连接检查不验证这些远端条件。 |
| 终端有 token，客户端没有 | 图形客户端可能不继承 shell 环境；由宿主安全注入，或使用已验证的系统凭证。 |
| 本地目录不可用／根外路径拒绝 | 核对客户端项目和授权模式，不通过选择更大父目录掩盖错误。 |
| `PLAN_EXPIRED`／`PRECONDITION_CHANGED` | 重查目标，用新 request_id 准备并确认，不能复用旧计划。 |
| `executing` | 查询原计划状态，不发起替代写入。 |
| `partial`／`unknown`／`RESULT_UNKNOWN` | 保留已知资源 ID，先查远端；不要自动重放写请求。 |
| 重启后找不到计划 | 计划只属于原进程，按远端资源核查，不承诺跨进程幂等。 |

取消或结束进程不等于服务端回滚。四客户端的当前实际验证范围见 [兼容性说明](compatibility.md)。

[返回文档目录](README.md)
