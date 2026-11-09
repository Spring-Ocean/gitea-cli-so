# GitHub 持续集成

[CI 工作流](../.github/workflows/ci.yml) 在 push、Pull Request 和手动触发时执行。分支不设过滤，同一分支或 PR 的新运行会取消尚未完成的旧运行。

## 检查范围

| 检查 | 环境 | 内容 |
| --- | --- | --- |
| 变更范围 | Ubuntu 24.04 | 检查 diff 空白错误，判断是否只有公开文档变化。 |
| 格式与依赖 | Ubuntu 24.04 | `gofmt`、下载并校验锁定模块，检查 `go.mod`／`go.sum` 未被改写。 |
| 工作流与静态检查 | Ubuntu 24.04 | 固定版本 actionlint 检查工作流，执行 `go vet ./...`。 |
| 安装脚本 | Ubuntu 24.04／Windows Server 2025 | sh 语法、ShellCheck、隔离安装／失败保持；Windows PowerShell 5.1 解析 ps1，PS5.1／PowerShell 分别执行公开文档命令的 BOM／无 BOM 文本下载与参数绑定回归。 |
| 竞态回归 | Ubuntu 24.04 | 执行 `go test -race -count=1 -timeout=5m ./...`，覆盖已有并发、取消、配置及 MCP 测试。 |
| 原生测试与构建 | macOS 15 arm64、macOS 15 Intel、Windows Server 2025 amd64 | 各环境执行 Go 测试，再以 `CGO_ENABLED=0` 构建对应程序，运行版本 JSON 和帮助冒烟。 |
| 交互与 MCP 冒烟 | macOS 15 arm64 | 复用 POSIX PTY 菜单脚本，以及 Codex 项目范围／工作区跟随配置向导和真实 MCP stdio 检查。 |
| 结果汇总 | Ubuntu 24.04 | `CI result` 检查所有适用任务的结果；失败、意外跳过或取消不会被当作通过。 |

Go 版本来自 `go.mod`，当前锁定为 **1.27.3**。`GOTOOLCHAIN=local` 禁止检查期间自动切换到另一个工具链，`GOFLAGS=-mod=readonly` 防止普通 Go 命令修改模块声明。交互脚本使用 Python 3.13 和标准库，不安装真实 AI 客户端。

仅修改根 `README.md`、`LICENSE` 或 `docs/` 中的 Markdown／图片时，运行变更检查和结果汇总，跳过 Go 检查与构建。图片扩展名包括 PNG、JPG／JPEG、SVG、WebP 和 GIF；其他文件保守执行全部检查。PR 比较 GitHub 提供的合并提交与其第一父提交；push 比较本次推送前后提交。新分支、无法读取旧提交或手动触发时执行全部检查。更改源码、依赖、脚本、测试或工作流都会执行全部检查。

工作流始终产生统一的 **CI result** 检查，可在 GitHub 分支保护或规则集中将其设为必需检查。首次运行前，仓库管理员需确保 GitHub Actions 已启用，并允许本工作流使用的官方 Actions 和托管 runner。

## 验证与发布边界

- 全部自动测试使用临时目录、合成凭证及回环 API；工作流不需要仓库 Secrets，不运行真实系统凭证库测试。
- Actions 固定到完整提交 SHA，工作流只授予 `contents: read`；checkout 不保留 Git 凭证，PR 使用 `pull_request` 事件。
- 普通 push 不启动七版本 Gitea 矩阵。API 兼容变化按影响版本执行隔离集成验证，发布前仍需完成七版本验收，入口见 [集成验证说明](../tests/integration/README.md)。
- Windows Server runner 的测试不能替代 Windows 10／11 实机验收；macOS 托管 runner 也不能替代 Terminal／iTerm2、系统凭证及四类 AI 客户端完整 UI 验收。
- 此 CI 工作流执行持续集成和开发构建验证；版本取自 [VERSION](../cmd/sotea/VERSION)，当前 CI 构建显示 `v0.1.0-rc.3+ci.<commit>`，不创建 GitHub Release。新增的独立 [Release 工作流](releases.md) 仅在版本标签 push 或手动选择标签时打包发布，原生验证通过后才公开附件；正式交付仍按[兼容性与发布状态](compatibility.md)中的验收边界执行。

## 本地复现

使用 Go 1.27.3，在仓库根目录执行：

```sh
gofmt -l cmd internal
go mod download
go mod verify
git diff --exit-code -- go.mod go.sum
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
go vet ./...
go test -count=1 -timeout=5m ./...
go test -race -count=1 -timeout=5m ./...
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o bin/sotea ./cmd/sotea
```

`gofmt -l` 有输出时，需要先格式化列出的文件。`-race` 需要目标平台支持和可用的 C 编译器。Windows PowerShell 下设置 `$env:CGO_ENABLED = '0'` 后执行构建，并使用 `bin/sotea.exe` 作为输出文件名。

在 macOS 上复现交互与 MCP 冒烟：

```sh
python3 scripts/p5-terminal-smoke.py --binary bin/sotea
python3 scripts/p6-integration-setup-smoke.py \
  --binary bin/sotea \
  --clients Codex --scopes project --follow \
  --result dist/ci/mcp-integration-result.json
```

这些脚本使用独立配置，不修改本机真实客户端接入项。托管 CI 是否通过，以该提交在 GitHub Actions 中的实际运行结果为准。

生成当前候选的三个目标构建及来源摘要时，指定独立目录保留旧候选：

```sh
python3 scripts/check.py --phase p7 --output dist/v0.1.0-rc.3
```

P7 使用 VERSION 中的完整版本，其他开发阶段附加 `+dev.<commit>`。`--build-only` 只构建，并将测试和 vet 记录为未执行。来源摘要包含 VERSION，打包时也会核对，避免版本变更后沿用旧构建证据。
