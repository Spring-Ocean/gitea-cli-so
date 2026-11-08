# sotea

[![Status: Pre-release](https://img.shields.io/badge/Status-Pre--release-orange?style=flat-square)](docs/compatibility.md#发布状态与问题反馈)
[![Go: 1.27.3](https://img.shields.io/badge/Go-1.27.3-00ADD8?style=flat-square&logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![CLI and TUI](https://img.shields.io/badge/Interface-CLI%20%2B%20TUI-4B5563?style=flat-square)](docs/usage.md)
[![MCP: stdio](https://img.shields.io/badge/MCP-stdio-8B5CF6?style=flat-square)](docs/mcp.md)

[![Target: macOS 15+](https://img.shields.io/badge/Target-macOS%2015%2B-555555?style=flat-square&logo=apple&logoColor=white)](docs/compatibility.md#平台)
[![Target: Windows 10 / 11](https://img.shields.io/badge/Target-Windows%2010%20%2F%2011-0078D4?style=flat-square)](docs/compatibility.md#平台)
[![Git: 2.43+](https://img.shields.io/badge/Git-2.43%2B-F05032?style=flat-square&logo=git&logoColor=white)](docs/configuration.md#git-凭证的匹配范围)
[![Gitea: 7 versions tested](https://img.shields.io/badge/Gitea-7%20versions%20tested-609926?style=flat-square&logo=gitea&logoColor=white)](docs/compatibility.md#gitea)
[![Docs: 简体中文](https://img.shields.io/badge/Docs-%E7%AE%80%E4%BD%93%E4%B8%AD%E6%96%87-blue?style=flat-square)](docs/README.md)

面向自部署 Gitea 的命令行工具。用同一个 Go 可执行程序，通过命令行、交互菜单或本地 MCP 服务处理仓库、Issue、Pull Request 和 Release。

A CLI for self-hosted Gitea. Manage repositories, issues, pull requests, and releases through commands, interactive menus, or a local MCP server, all from a single Go executable.

## 能做什么 / Features

| 能力 / Feature | 使用方式 / How it works |
| --- | --- |
| 交互工作台 / Interactive workspace | 在项目目录运行 `sotea`，自动识别仓库；浏览账户、组织和仓库，查看详情并逐步执行操作。<br>Run `sotea` in a project directory to detect the repository, browse accounts, organizations, and repositories, view details, and follow guided operations. |
| 开发流程 / Development workflows | 创建和管理 Issue、讨论与审查 PR、合并代码、准备和发布 Release、上传附件。<br>Create and manage issues, discuss and review pull requests, merge changes, prepare and publish releases, and upload assets. |
| Git 与 API / Git and API | 调用系统 Git，复用经过身份验证的 HTTPS Git 凭证；通过 `sotea api` 调用当前账号有权访问的 Gitea API。<br>Use system Git and verified HTTPS Git credentials. Call Gitea APIs available to the selected account through `sotea api`. |
| AI 客户端接入 / AI client integrations | 内置 stdio MCP，提供 Codex、Claude Code、Cursor、VS Code 配置向导；支持跟随客户端项目。<br>Built-in stdio MCP with setup wizards for Codex, Claude Code, Cursor, and VS Code, including support for following the client's project. |
| 自动化 / Automation | 业务命令支持 JSON、明确的退出码和非交互运行；写操作默认预览确认。<br>Business commands provide JSON output, defined exit codes, and non-interactive execution. Writes require a preview and confirmation by default. |

程序界面与提示使用英文；本 README 中英对照，详细文档目前使用中文。仓库中的标题、正文和评论保留原文。

The interface and prompts are in English. This README is bilingual; detailed documentation is currently in Chinese. Repository titles, body text, and comments retain their original language.

## 快速开始 / Quick start

安装对应平台的预编译程序，放入 PATH；运行时无需 Go。安装、升级和卸载步骤见 [安装说明](docs/installation.md)。

Install the prebuilt executable for your platform and add it to PATH. Go is not required at runtime. See the [installation guide](docs/installation.md) for installation, upgrades, and removal.

```sh
sotea version
cd /path/to/project
sotea
```

首次使用，在 **Account** 中按提示绑定 Gitea 实例和账号。已经配置 HTTPS Git 凭证的仓库也可以执行：

On first use, open **Account** and follow the prompts to connect your Gitea instance and account. If the repository already has HTTPS Git credentials configured, you can also run:

```sh
sotea auth login --credential-source git
sotea auth status --check
sotea repo view
sotea issue list --state open
```

没有本地仓库时，先通过菜单连接实例，再使用 **Browse account**；也可显式指定目标：

Without a local repository, connect an instance through the menu and use **Browse account**, or specify the target explicitly:

```sh
sotea --instance team --repo owner/repository issue list
```

`team` 是保存的实例名。token、私有 CA、多账号和 SSH remote 的处理见 [配置与认证](docs/configuration.md)。

`team` is the saved instance name. See [configuration and authentication](docs/configuration.md) for tokens, private CAs, multiple accounts, and SSH remotes.

## 常用操作 / Common operations

```sh
sotea issue view 42
sotea issue close
sotea pr list
sotea pr review create 42 --event approve
sotea release create v1.0.0 --target main --notes-file release-notes.md
sotea --workdir /path/to/project git -- status
```

在可交互终端中，`issue close` 不带编号会先列出候选供选择；直接传编号也会预览确认。Release 默认创建为草稿。菜单内 Enter 进入、Esc 返回，批量资源使用 Space 勾选。

In an interactive terminal, `issue close` without numbers opens a selector. Explicit numbers still require a preview and confirmation. Releases are created as drafts by default. In menus, Enter opens an item, Esc goes back, and Space selects items for batch operations.

完整操作示例、分页、JSON、脚本与退出码见 [使用指南](docs/usage.md)。

See the [usage guide](docs/usage.md) for more examples, pagination, JSON output, scripting, and exit codes.

## 连接 AI 客户端 / Connect AI clients

运行 `sotea`，进入 **MCP integrations**，选择客户端与用户／项目范围，再选择实例、账号、本地访问模式和权限。预览配置后确认安装，客户端会自动管理 MCP 子进程。

Run `sotea`, open **MCP integrations**, and select the client and user or project scope. Then choose the instance, account, local access mode, and permissions. Review and confirm the configuration; the client will manage the MCP subprocess automatically.

- **Follow client workspace**：同一接入配置跟随不同项目，不固定当前目录。<br>Reuse one integration across projects without pinning the current directory.
- **Choose a fixed directory**：只授权指定目录内的本地能力。<br>Authorize local capabilities only within the selected directory.
- **Remote tools only**：只使用远端工具。<br>Use remote tools only.

默认只读，业务写入和原始 API 独立开启。接入、更新、移除及故障处理见 [MCP 接入指南](docs/mcp.md)。

Access is read-only by default. Business writes and raw API access are enabled separately. See the [MCP integration guide](docs/mcp.md) for setup, updates, removal, and troubleshooting.

## 平台与当前状态 / Platforms and current status

目标平台为 macOS 15 起的 arm64／amd64，以及 Windows 10 22H2／Windows 11 的 amd64。Git 集成功能基线为 Git 2.43。

Target platforms are macOS 15 and later on arm64 and amd64, plus Windows 10 22H2 and Windows 11 on amd64. The Git integration baseline is Git 2.43.

项目目前处于发布前验收阶段。七个指定 Gitea 版本的共同业务及 MCP 回归已通过；Windows、macOS 最低版本／Intel、目标终端和四客户端完整 UI 仍有待实测项。正式下载渠道与签名条件尚未落实。具体版本和验证边界见 [兼容性说明](docs/compatibility.md)，Windows 实测入口见 [Windows 验证指南](docs/windows-validation.md)。

The project is undergoing pre-release acceptance testing. Shared business workflows and MCP regression checks have passed against seven specified Gitea versions. Testing remains pending for Windows, the minimum macOS version and Intel Macs, target terminals, and the complete UI flows of all four clients. Official distribution and signing arrangements are not yet finalized. See [compatibility and validation status](docs/compatibility.md) for exact versions and coverage, and the [Windows validation guide](docs/windows-validation.md) for Windows testing.

## 从源码构建 / Build from source

使用 **Go 1.27.3**，保持现有模块路径。以下命令可在仓库根目录执行：

Use **Go 1.27.3** and keep the existing module path. Run the following commands from the repository root:

```sh
go version
go test ./...
go vet ./...
go build -o bin/sotea ./cmd/sotea
./bin/sotea version
```

Windows PowerShell 下使用以下命令：

For Windows PowerShell, use:

```powershell
go build -o bin/sotea.exe ./cmd/sotea
.\bin\sotea.exe version
```

未注入版本时显示 `dev`。开发文档、设计与验收记录保存在本地 `devdocs/`，该目录已加入 Git 忽略规则；对外使用文档统一放在 [docs/](docs/README.md)。隔离测试环境的执行说明保留在 [tests/integration](tests/integration/README.md)。

Builds without an injected version display `dev`. Development documentation, designs, and acceptance records are kept locally in `devdocs/`, which is ignored by Git. User documentation lives in [docs/](docs/README.md). Instructions for the isolated test environment are in [tests/integration](tests/integration/README.md).

## 许可证 / License

本项目采用 [MIT License](LICENSE)。

This project is licensed under the [MIT License](LICENSE).

Copyright (c) 2026 springocean.
