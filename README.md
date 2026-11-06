# sotea

面向自部署 Gitea 的命令行工具，目前处于工程初始化阶段。

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

目前提供命令入口、帮助和版本命令。Git、Gitea API、MCP、认证和终端样式尚未实现；Cobra 用于命令解析，Lip Gloss 会在需要终端渲染时引入。目录按功能逐步创建，避免提交空占位目录。

需求与技术方案分别见 [requirements.md](docs/requirements.md) 和 [technical-design.md](docs/technical-design.md)。

首版运行、认证、多选和确认规则见 [cli-contract.md](docs/cli-contract.md)，MCP 工具方案见 [mcp-design.md](docs/mcp-design.md)。这些是实施契约，尚未实现；当前支持的命令仍以实际 --help 为准。

模块划分、阶段任务、依赖关系和验收标准见 [development-plan.md](docs/development-plan.md)。

P0 的七版本隔离环境、重复初始化及 helper／HTTP／TLS 演练见 [p0-readiness.md](docs/p0-readiness.md)，重建步骤见 [integration README](tests/integration/README.md)。该结果只验证测试基础设施，业务兼容性仍待后续实现与验收。

## 许可证

本项目采用 [MIT License](LICENSE)。
