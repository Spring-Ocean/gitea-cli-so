# GitHub Release 发布

[Release 工作流](../.github/workflows/release.yml) 在推送 `v*` 标签时运行；也可在 Actions 中手动选择已存在的标签。普通分支 push 仍只执行 [CI](ci.md)。本工作流新增不代表已经发布版本，实际附件以仓库 Releases 为准。

## 发布前

1. 将本轮代码、脚本及公开文档提交并推送，确认 CI 通过。
2. 将 `cmd/sotea/VERSION` 设置为目标版本。标签必须与文件完全一致，例如 `v0.1.0-rc.2`；不接受带 `+build` 的发布标签。
3. 核对支持范围和已知限制，并按目标版本更新 [Release 说明模板](release-notes.md)；模板必须逐版更新，不能原样沿用其他版本的变更和验证说明。正式稳定版仍需完成相应平台、终端、客户端及七版本 Gitea 验收；工作流成功不能自动补齐这些证据。新标签会重新构建最终包，旧候选的 hash 和验收记录不能直接套用。
4. 仓库需启用 GitHub Actions，允许官方 Actions 和托管 runner，并允许发布任务的 `GITHUB_TOKEN` 使用 `contents: write`。无需配置个人访问 token 或第三方发布服务。

维护者确认准备发布后，在目标提交上操作：

```sh
git tag -a v0.1.0-rc.2 -m 'sotea v0.1.0-rc.2'
git push origin v0.1.0-rc.2
```

上述命令会触发真正发布，不是本地验证命令。已有标签无需重复创建，可在 Actions → Release → Run workflow 中填入它；工作流始终检出标签提交，不拿默认分支源码冒充标签内容。

## 自动流程

| 阶段 | 检查及产物 |
| --- | --- |
| 版本和源码 | 校验 SemVer、标签提交、VERSION、工具链 Go 1.27.3、干净工作区、格式、依赖及工作流。 |
| 回归 | `go vet`、全量竞态测试、安装脚本静态检查与隔离测试。 |
| 打包 | `CGO_ENABLED=0`、`-trimpath`、`-buildvcs=false`；三个目标的实际版本均为标签本身，无 `+ci` 后缀。 |
| 原生验证 | macOS 15 arm64／Intel、Windows Server 2025 amd64 执行完整 Go 测试，并运行最终归档中的程序；arm64 对最终程序执行 PTY／MCP 冒烟；验证 sh、Windows PowerShell 5.1 和 PowerShell 安装场景。 |
| 草稿上传 | 所有验证通过后创建草稿，上传完整附件，既有同名 Release 或附件不覆盖。 |
| 下载复核 | 从 GitHub 重新下载全部附件，与本地 SHA256SUMS 及来源记录逐一比较。 |
| 公开发布 | 复核成功才取消草稿。`-rc.N` 等标签自动标记为 prerelease，不更新 Latest；稳定标签设为 Latest。 |

只有最终发布任务拥有 `contents: write`。其他任务只读；checkout 不保留 Git 凭证。Actions 固定完整 SHA，上传和下载只在同一次 workflow run 中传递附件。生成的 Release 说明包含中英文安装、升级与验证边界，并追加 GitHub 自动生成的变更记录。每次说明必须包含 macOS／Windows 一键首次安装命令；工作流把模板中的 `{{RELEASE_TAG}}` 替换为实际标签，让脚本下载地址与版本参数一致，RC 也不会误装稳定版。

## 附件契约

以 `v0.1.0-rc.2` 为例，共七个附件：

```text
sotea-v0.1.0-rc.2-darwin-arm64.tar.gz
sotea-v0.1.0-rc.2-darwin-amd64.tar.gz
sotea-v0.1.0-rc.2-windows-amd64.zip
install.sh
install.ps1
release-manifest.json
SHA256SUMS
```

三个包各自只有一个同名顶层目录，包含程序、根 README、公开 docs、项目 MIT LICENSE、原始依赖许可及 Go 运行时声明、`THIRD-PARTY-NOTICES.json`、`BUILD-INFO.json`、`CONTENTS.sha256`。Windows 另附基础验收脚本。不会包含 `devdocs`、开发者配置或凭证。

`release-manifest.json` 记录标签、提交、Go 版本和平台；`SHA256SUMS` 包含其余六个附件的摘要。归档布局与 `sotea update install` 使用同一命名契约；重新打包不得覆盖同版本，改用新版本并重新验证。

## 失败处理

- 构建／原生验证失败：不创建 Release，修复代码后用新标签发布；若尚未发布且需要复跑同一提交，可手动触发工作流。
- 上传／下载复核失败：保留草稿用于排查，不自动公开，也不删除现场。确认问题后由维护者处理未发布草稿，再重跑；已有公开 Release 不能通过此流程覆盖。
- 标签移动、VERSION 不匹配、已有 Release：工作流停止，不继续追加或替换文件。
- 发布后发现问题：记录受影响版本，发布新的修复版本；不覆盖旧附件，不把失败文件的摘要改成通过。

## 本地验证

在干净检出目录，以 Go 1.27.3 执行：

```sh
GOTOOLCHAIN=go1.27.3 python3 scripts/release.py build \
  --tag v0.1.0-rc.2 --directory dist/release-local
python3 scripts/release.py verify --tag v0.1.0-rc.2 --directory dist/release-local
GOTOOLCHAIN=go1.27.3 python3 scripts/release.py native \
  --tag v0.1.0-rc.2 --target darwin-arm64 --directory dist/release-local
python3 scripts/test-installers.py --tag v0.1.0-rc.2 --directory dist/release-local
```

`native` 的目标必须等于本机 OS／架构；Windows 安装测试需在 Windows 使用 `--powershell powershell.exe` 或 `--powershell pwsh.exe`。本地构建不触发网络发布，也不代替目标系统验收。

[返回文档目录](README.md)
