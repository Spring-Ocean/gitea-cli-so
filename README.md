# sotea

面向自部署 Gitea 的命令行工具，已实现上下文、认证、Issue／PR／Release 完整链路和通用 API；目标平台实测与后续能力按阶段推进。

## 开发环境

- Go 1.27.3。仓库的 go.mod 规定了最低工具链版本；开发和发布请使用指定的 1.27.3，避免将最低版本理解为精确版本锁定。
- 拉取 Go 模块和自动下载工具链时需要访问相应的模块代理。

检查版本并运行：

~~~sh
go version
go run ./cmd/sotea --help
go run ./cmd/sotea version
go test ./...
go build -o bin/sotea ./cmd/sotea
~~~

Windows PowerShell 下构建可执行文件：

~~~powershell
go build -o bin/sotea.exe ./cmd/sotea
~~~

发布构建可通过 `go build -ldflags "-X main.version=v1.0.0" -o bin/sotea ./cmd/sotea` 注入版本；未指定时 `version` 命令显示 `dev`。

如本机有其他 Go 版本，可以在命令执行环境中将 GOTOOLCHAIN 设置为 go1.27.3，由 Go 使用或下载指定工具链。正式构建应先核对实际使用的工具链。

程序与命令名称为 `sotea`。Go 模块路径暂时保留现有远程仓库地址 `github.com/Spring-Ocean/gitea-cli-so`；如以后重命名远程仓库，应同步评估模块迁移。

## 当前范围

目前提供帮助、版本、补全、系统 Git、五类 auth 命令、instance info、仓库开发上下文只读命令、全部 Issue／PR／Release 命令及任意方法的原始 API（含上传／下载）。缺少资源编号／标签时可在交互终端搜索并选择，写入默认预览确认。MCP 已接入完整本地 stdio 工具，默认只读，完整模式 32 工具／21 种写 action；持续菜单、共享字段表单、表格和 --color 已接通；写入通过 prepare／commit／status 执行，原始 API 和本地文件根分别授权。首页通过 Gitea API 展示已验证用户资料；Browse account 可浏览账户仓库、组织和组织仓库，进入工作台后可逐层返回。业务详情统一使用可读字段和分组，正文支持安全 Markdown，菜单内的 JSON API 响应也可直接留屏查看。全局页面区分选项名称／说明与操作指引，底部显示当前位置；成功结果只保留资源标题，字段键值分层，分组使用背景标签；Account 使用输入框分步完成验证与明确保存。Release 仅对草稿显示高亮的 ◇ Draft。程序生成的提示和界面统一使用英文，开发文档使用中文。

~~~sh
sotea
sotea version --json
sotea --workdir /path/to/repository git -- status
sotea auth login --credential-source git
sotea repo view --json
sotea issue list --state open --json
sotea issue close 42
sotea issue close
sotea pr list --json
sotea pr merge 42
sotea release create v1.0.0 --target main
python3 scripts/check.py --phase p5
~~~

公共选项放在 git 之前，git 之后的参数原样交给 Git；Git 透传不支持业务 --json。三流均为终端时无参数进入持续菜单，启动探测当前仓库；↑↓ 移动、Enter 进入、Esc 返回，批量资源才用 Space。分组结果用 PgUp／PgDn 或 Tab／Shift+Tab 切组，长组才用 ↑↓、Home／End 组内滚动，左右键不切组；CI、重定向或 --no-interactive 时无参数显示帮助，--json 无子命令返回参数错误。基础能力见 [p1-foundation.md](docs/p1-foundation.md)，认证、只读命令和 MCP 原型见 [p2-readonly.md](docs/p2-readonly.md)，Issue、API 文件与验证结果见 [p3-issue.md](docs/p3-issue.md)，PR／Release 流程及版本结果见 [p4-workflows.md](docs/p4-workflows.md)。持续菜单、按键、字段与终端输出见 [p5-terminal-experience.md](docs/p5-terminal-experience.md)。完整 MCP 与七版本代表性流程见 [p6-mcp.md](docs/p6-mcp.md)，客户端配置见 [mcp-client-setup.md](docs/mcp-client-setup.md)。Windows／目标 macOS 和四客户端完整 UI 仍待验收。

在 Home 选择 **MCP integrations**，可为 Codex、Claude Code、Cursor、VS Code 配置用户或项目接入。逐步选择实例、账号、可选仓库与本地访问模式（跟随当前客户端项目、固定目录或仅远端），默认只读；预览确认后安装／更新，支持临时协议连接检查和仅移除自有条目。不会覆盖其他 MCP 服务器或写入 token，客户端仍负责启动服务及批准工具操作。向导与验证范围见 [p6-mcp-integrations.md](docs/p6-mcp-integrations.md)。

需求与技术方案分别见 [requirements.md](docs/requirements.md) 和 [technical-design.md](docs/technical-design.md)。

首版运行、认证、多选和确认规则见 [cli-contract.md](docs/cli-contract.md)，MCP 工具方案见 [mcp-design.md](docs/mcp-design.md)。契约中的后续能力仍待实施；当前可用命令以实际 --help 为准。

模块划分、阶段任务、依赖关系和验收标准见 [development-plan.md](docs/development-plan.md)。

P0 的七版本隔离环境、重复初始化及 helper／HTTP／TLS 演练见 [p0-readiness.md](docs/p0-readiness.md)，重建步骤见 [integration README](tests/integration/README.md)。该结果只验证测试基础设施，业务兼容性仍待后续实现与验收。

## 许可证

本项目采用 [MIT License](LICENSE)。
