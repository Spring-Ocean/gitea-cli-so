# sotea

面向自部署 Gitea 的命令行工具，已实现上下文、认证、Issue 完整链路和通用 API；目标平台实测与后续能力按阶段推进。

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

目前提供帮助、版本、补全、系统 Git、五类 auth 命令、instance info、仓库开发上下文只读命令、全部 Issue 命令及任意方法的原始 API（含上传／下载）。缺少 Issue 编号时可在交互终端搜索并选择，写入默认预览确认。MCP 已有仅含 sotea_instance_info 的 stdio 原型；PR／Release、完整 MCP、持续菜单和终端样式按后续阶段接入。程序生成的提示和界面统一使用英文，开发文档使用中文。

~~~sh
sotea version --json
sotea --workdir /path/to/repository git -- status
sotea auth login --credential-source git
sotea repo view --json
sotea issue list --state open --json
sotea issue close 42
sotea issue close
python3 scripts/check.py --phase p3
~~~

公共选项放在 git 之前，git 之后的参数原样交给 Git；Git 透传不支持业务 --json。当前无参数显示帮助，持续菜单按 P5 实施。基础能力见 [p1-foundation.md](docs/p1-foundation.md)，认证、只读命令和 MCP 原型见 [p2-readonly.md](docs/p2-readonly.md)，Issue、API 文件与验证结果见 [p3-issue.md](docs/p3-issue.md)。Windows 实机与真实 MCP 客户端仍待验收。

需求与技术方案分别见 [requirements.md](docs/requirements.md) 和 [technical-design.md](docs/technical-design.md)。

首版运行、认证、多选和确认规则见 [cli-contract.md](docs/cli-contract.md)，MCP 工具方案见 [mcp-design.md](docs/mcp-design.md)。契约中的后续能力仍待实施；当前可用命令以实际 --help 为准。

模块划分、阶段任务、依赖关系和验收标准见 [development-plan.md](docs/development-plan.md)。

P0 的七版本隔离环境、重复初始化及 helper／HTTP／TLS 演练见 [p0-readiness.md](docs/p0-readiness.md)，重建步骤见 [integration README](tests/integration/README.md)。该结果只验证测试基础设施，业务兼容性仍待后续实现与验收。

## 许可证

本项目采用 [MIT License](LICENSE)。
