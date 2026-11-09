# 检查与安装更新

sotea 从公开 GitHub 仓库 `Spring-Ocean/gitea-cli-so` 的 Releases 检查和下载更新，不要求登录 GitHub；默认优先复用已有 GitHub 凭证，没有可用凭证时匿名访问。不会读取或转发 Gitea token，也不发送当前仓库信息。

## GitHub 认证

默认 `auto` 模式按以下顺序选择第一个可用凭证：`SOTEA_GITHUB_TOKEN` → `GH_TOKEN` → `GITHUB_TOKEN` → `gh auth token --hostname github.com` → Git credential helper 中匹配 `https://github.com/Spring-Ocean/gitea-cli-so` 的 HTTPS 凭证。无需安装 gh 或 Git；来源缺失、查询失败或超时后继续查找，全部不可用则匿名。Git 的 `credential.useHttpPath` 继续生效，查询在仓库外执行，不能读取当前项目的局部 helper；SSH 密钥和浏览器会话不参与。

凭证查询不弹登录框、不启动浏览器。每个外部查询最多五秒，更新检查总体仍受八秒限制；Home 可取消。token 只留内存，不写缓存、配置、日志或命令参数。认证仅发送给固定仓库的 `https://api.github.com` Releases API；附件和重定向请求不携带凭证。

若认证返回 `401`，同一读取请求只匿名重试一次，检查结果包含英文 `warning`；不会删除或更新共享凭证。`403`／`429` 不自动重试或轮换凭证，手动检查显示限流及可用的恢复时间，Home 继续遵守五小时冷却。认证提高 API 配额的机会，不能解决网络超时或附件 HTTP 504。

可关闭全部凭证查询：

```sh
sotea update check --github-auth anonymous
sotea update install --github-auth anonymous
```

设置 `SOTEA_GITHUB_AUTH=anonymous` 同时约束 Home、更新命令和安装脚本；`auto` 恢复自动查找，显式参数优先于环境变量。首次安装的 macOS 脚本支持 `--github-auth anonymous`，Windows 脚本支持 `-GitHubAuth anonymous`。显式版本首次安装无需查询 API，也不会读取凭证；未指定版本时，两脚本查询固定 API 的最新稳定版。

上述功能从 rc.5 开始提供；rc.4 及以前的二进制与脚本保持原样。使用旧程序升级到 rc.5 的首次请求仍遵循旧版匿名策略，升级并重启后才启用新逻辑。

## Home 提醒

进入 Home 时后台检查更新，不等待网络请求完成才允许导航。同一系统用户的多个 sotea 进程共用更新缓存，**五小时内最多自动检查一次**；网络失败、限流和取消也计入冷却。再次进入 Home 会复用缓存；持续留在 Home 不会开启定时轮询。

发现新版后，首页显示高亮的 `Update available · <version>`，**Update** 入口也会高亮。进入后选择 **Check for updates** 或 **Install update**。即使没有本地仓库、实例不可用或尚未登录，也可使用该入口。后台检查失败不弹出错误页面、不影响仓库业务；手动检查会展示具体错误。

自动检查仅在工作台入口启动；普通业务命令、MCP 服务和 CI 不启动自动检查。设置环境变量 `SOTEA_UPDATE_CHECK=never` 可关闭自动检查，仍可手动执行更新命令。

## 命令行

```sh
sotea update check
sotea update check --json
sotea update install
```

`check` 立即查询 GitHub，绕过自动检查的五小时间隔。`install` 也会先查询，再展示版本、来源和安装说明，默认拒绝；输入 `y` 确认后自动下载、校验并替换当前可执行程序。脚本必须显式使用：

```sh
sotea update install --yes --no-interactive
```

稳定版本默认只提示后续稳定版。当前版本包含预发布标识（例如 `v0.1.0-rc.1`）时，自动包含后续 RC、其他预发布及稳定版。稳定版用户可以主动选择：

```sh
sotea update check --prerelease
sotea update install --prerelease
```

按完整语义版本比较，`rc.10` 高于 `rc.2`，相同版本的 `+ci.<commit>`／`+dev.<commit>` 不计为新版本；不会降级。检查最近一百个发布，忽略草稿和非法版本标签。没有公开 Release 时，返回暂无发布；发现新版但当前平台附件不齐时，只提示访问发布页。

## 安装过程和恢复

安装只处理当前运行的 sotea，支持 macOS arm64／amd64 和 Windows amd64：

1. 确认后重新读取选定标签，核对发布仍可安装，避免使用缓存中的过期附件地址。
2. 下载精确平台压缩包及同一 Release 的 `SHA256SUMS`；校验字节数、SHA-256，以及 GitHub 提供时的附件 digest。
3. 在安装目录的私有临时目录中，只提取指定 `sotea`／`sotea.exe`，拒绝越界路径、链接、特殊文件、重复可执行文件及超限归档。
4. 检查原生文件格式和架构，运行隔离的 `version --json` 核对标签；再检查原安装文件身份和摘要未发生变化。
5. 在同目录保留名为 `.sotea-update-<random>.previous` 的旧程序备份，替换安装路径。macOS 使用同卷原子替换；Windows 先改名保留旧 exe，安装失败时尝试恢复，恢复也失败会明确显示备份和恢复位置。

校验失败、下载中断或确认前取消都不会替换原程序。替换阶段已开始时，取消不能当作已经回滚，需以返回结果为准。程序不自动提权；目录不可写时，需要换到用户可写的安装目录或手动安装。遗留安装锁不会自动接管，确认没有更新进程后可手动移除安装路径旁的 `.update.lock`。

成功后显示实际安装位置、备份位置及 `restart_required=true`。**退出并重新启动 sotea，同时重启 AI 客户端中已经运行的 sotea MCP 进程**；当前会话继续运行旧版本，不会自动重启或重放操作。同一工作台会话不重复安装。

需要回退时，先退出所有 sotea／MCP 进程，把成功结果中的旧程序备份复制回原安装路径，并恢复执行权限。备份不会自动删除。账号、配置与凭证均保存在程序之外，自动更新不修改这些数据。

Homebrew 的 `Cellar`／`Caskroom` 和 npm 的 `node_modules` 安装路径会拒绝自替换，提示使用对应包管理器升级。临时 `go run` 可执行文件也不能自更新。当前源代码构建安装在固定可写目录时，可使用相同更新入口。

## 缓存与隐私

缓存文件为 Go 系统用户缓存目录下的 `sotea/updates.json`：macOS 通常位于 `~/Library/Caches/sotea/`，Windows 通常位于 `%LocalAppData%\sotea\`。它只保存检查时间、通道、公开发布附件和错误摘要，不保存账号或仓库信息，与 `--config` 无关。不同用户各自冷却，不依赖启动目录。

检查使用八秒上限，安装总时限五分钟；网络失败不自动重放。缓存损坏时保守停止自动检查并可手动删除 `updates.json` 恢复。若检查进程崩溃留下 `updates.json.lock`，确认没有活动检查后移除锁文件。

## 发布附件约定

发布标签应为 `v<major>.<minor>.<patch>` 或带预发布后缀，例如 `v0.1.0-rc.2`。自动安装需要以下精确附件名：

```text
sotea-v0.1.0-rc.2-darwin-arm64.tar.gz
sotea-v0.1.0-rc.2-darwin-amd64.tar.gz
sotea-v0.1.0-rc.2-windows-amd64.zip
SHA256SUMS
```

兼容现有 `sotea-<version>-pkgN-<os>-<arch>` 包名，但同一发布、同一平台只能有一个候选包；多个匹配项会拒绝自动安装。压缩包中程序位于包根或与附件名对应的单层目录中。`SHA256SUMS` 每项格式为 `sha256  完整附件名`，每个附件只能有一项。

发布源与下载重定向限定为固定 GitHub HTTPS 来源及其官方附件存储。摘要用于校验发布文件一致性，不替代平台代码签名或公证；实际签名与验收状态仍见 [兼容性说明](compatibility.md)。此功能不自动创建或发布 GitHub Release，尚无附件时需要维护者先上传完整产物。

[返回文档目录](README.md)
