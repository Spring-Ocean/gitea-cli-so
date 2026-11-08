# 配置与认证

sotea 将 Gitea 实例、API 账号和仓库上下文分别管理。程序从当前目录发现 Git 仓库，也允许通过参数明确选择远端目标。

## 连接实例

运行 `sotea`，在 **Account** 中依次选择或输入实例、账号和凭证来源，验证后保存。实例地址填写完整根地址，保留反向代理子路径，例如 `https://gitea.example.com/code`，不要填写仓库网页地址或 `/api/v1`。

在交互终端使用 token 登录：

```sh
sotea --instance https://gitea.example.com auth login --credential-source token --name team
sotea --instance team auth status --check
```

首次连接会确认实例来源，token 使用隐藏输入。`--name team` 给新实例设置别名；它不能重命名已有实例。非交互首次连接还需要显式的 `--confirm-instance`；普通 `--yes` 不代替来源确认。

没有本地仓库时，可从菜单连接实例、浏览账户仓库，或在命令中提供仓库：

```sh
sotea --instance team --repo owner/repository repo view
```

## 凭证来源

| 来源 | 使用方式 | 保存位置与边界 |
| --- | --- | --- |
| token | `auth login --credential-source token` | 验证后保存在 macOS Keychain／Windows Credential Manager，普通配置只保存引用。 |
| Git helper | 在目标仓库运行 `auth login --credential-source git` | 调用 Git 的 credential helper，保存查询上下文及已验证 API 身份。 |
| 环境变量 | 外部注入 `SOTEA_TOKEN`，可用 `auth login --credential-source environment` 验证保存引用 | token 仍由启动 shell、CI 或 AI 客户端管理，不复制到普通配置。 |

显式环境 token 优先于保存的凭证；建议同时设置 `SOTEA_TOKEN_INSTANCE` 为实例名或对应根地址，限定使用目标。环境值由密钥管理工具注入，不写入项目配置、客户端 JSON 或命令参数。需要从管道传入 token 时，使用 `auth login --with-token` 的 stdin。

系统安全存储失败时不会静默回退到明文文件。Git commit 作者信息不代表 Gitea 登录账号；API 返回的身份与显式选择账号不一致时会停止请求。

### Git 凭证的匹配范围

Git HTTPS 凭证遵循协议、主机／端口、用户名和 helper 配置。HTTP(S) 默认通常按主机等上下文查询；启用 `credential.useHttpPath=true` 后，仓库路径也参与匹配。sotea 使用原仓库 URL 上下文，不把 API 路径当凭证路径，也不轮流尝试该主机上的所有账号。

sotea 保存的 API 凭证引用绑定完整实例根地址和已验证账号；同一实例内多个仓库可以使用同一账号。不同根地址即使位于同一域名，也不会自动共享 API 身份。

SSH key、SSH agent 和 Git 的 SSH 登录不直接提供 API 身份。SSH remote 无法确定 HTTP 实例时，显式指定 `--instance`／`--repo`，完成 token 登录；不要把 SSH 私钥输入为 token。

## 账号维护

```sh
sotea --instance team auth status
sotea --instance team auth status --check
sotea --instance team auth switch another-user
sotea --instance team --account username auth refresh
sotea --instance team --account username auth logout
```

`auth status` 展示状态，`--check` 请求 Gitea 核验身份。切换账号会验证所选账号；更新凭证成功后替换旧引用。退出后保存抑制标记，避免下一次通过 Git helper 隐式重登。

退出默认不删除共享 Git 凭证，也不撤销服务端 token。需要拒绝共享 helper 凭证时，使用 `auth logout --forget-git` 并确认实际作用域；这可能影响同主机的其他仓库。环境 token 需要在外部移除，继承了旧环境的 MCP 进程需要重启。

## 上下文参数

| 参数 | 含义 |
| --- | --- |
| `--instance team` | 已保存实例名，或明确的 HTTP(S) 根地址。 |
| `--account username` | 明确选择 API 账号，不修改 Git 作者。 |
| `--repo owner/repository` | 明确选择远端仓库。 |
| `--workdir /absolute/path` | 指定本次本地工作目录。 |
| `--remote origin` | 多个 Git fetch remote 时指定一个。 |
| `--config /absolute/path/config.json` | 使用独立的 sotea 配置。 |

显式参数优先于相应默认值；未指定时结合环境、保存的绑定和 Git remote 解析。多个来源有歧义时需要明确选择，不自动猜测。MCP 的工作目录授权规则见 [MCP 接入](mcp.md)。

## 配置位置

| 系统 | 默认位置 |
| --- | --- |
| macOS | `~/Library/Application Support/sotea/config.json` |
| Windows | `%AppData%\sotea\config.json` |

配置路径优先级为 `--config`、`SOTEA_CONFIG`、系统默认位置。当前格式为 version=1，包含实例、账号引用、仓库绑定和退出抑制信息。未知字段、未知格式或损坏 JSON 会返回错误，不会重置已有文件。

优先通过菜单和认证命令维护配置。跨设备复制配置不会自动迁移系统凭证或本地绝对路径，通常需要重新登录，并更新 CA、仓库和 MCP 程序路径。

## 私有 CA 与代理

```sh
sotea --instance https://gitea.example.com --ca-file /absolute/path/team-ca.pem auth login --credential-source token --name team
sotea --instance team --proxy direct repo view
```

CA 为 PEM 文件，作为相应请求的额外信任来源，不修改系统信任库。Windows 可写为 `--ca-file 'C:\Certificates\team-ca.pem'`；保存的 CA 路径也须能被 AI 客户端启动的进程读取。

`--proxy direct` 禁用环境代理；也可显式指定不带用户名／密码的 HTTP(S) 代理地址。默认验证 TLS，不通过忽略证书错误解决连接问题。

## 常见问题

| 提示／现象 | 处理方式 |
| --- | --- |
| `AUTH_REQUIRED` | 检查所选账号、凭证来源和 token 是否失效；运行登录或 refresh。 |
| `AUTH_IDENTITY_MISMATCH` | 核对 token 对应的 API 账号，明确选择正确账号后重新验证。 |
| `PERMISSION_DENIED` | 检查账号对目标仓库的权限及 token scope；不要反复替换凭证猜测。 |
| `CONTEXT_REQUIRED`／`CONTEXT_AMBIGUOUS` | 明确实例、仓库或 remote；非仓库目录可使用显式远端上下文。 |
| TLS 失败 | 核对根地址、代理子路径、域名、时间和 CA 链。 |
| `CONFIG_CONFLICT` | 重新读取配置后再操作；存在 `.lock` 时先确认没有其他进程写入。 |
| 终端能用，AI 客户端不能用 | 核对客户端进程的程序路径、配置路径、CA、账号及环境；图形客户端未必继承当前 shell 环境。 |

[返回文档目录](README.md)
