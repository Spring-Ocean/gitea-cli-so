# sotea 使用文档

从安装、账号绑定开始，也可以直接查看所需的使用场景。

| 文档 | 内容 |
| --- | --- |
| [安装、升级与卸载](installation.md) | GitHub Release、sh／PowerShell 首次安装、校验压缩包、PATH、升级与配置保留。 |
| [检查与安装更新](updates.md) | Home 五小时提醒、GitHub Releases、自动下载校验安装及备份恢复。 |
| [配置与认证](configuration.md) | 实例、账号、Git 凭证、token、私有 CA、代理及配置位置。 |
| [使用指南](usage.md) | 菜单、Issue／PR／Release、Git、通用 API、JSON 与退出码。 |
| [MCP 接入](mcp.md) | 四客户端配置向导、工作区跟随、权限、连接检查和恢复。 |
| [MCP 模型调用指南](mcp-agent-guide.md) | sotea 与 Gitea 字段区别、action 参数表、Release 示例及错误恢复。 |
| [兼容性与验证状态](compatibility.md) | 目标系统、Gitea 版本及已验证范围。 |
| [Windows 验证](windows-validation.md) | Windows 10／11 基础检查、随候选包提供的脚本及手工验收。 |
| [GitHub 持续集成](ci.md) | push／PR 检查、目标构建、文档变更跳过规则及本地复现。 |
| [GitHub Release 发布](releases.md) | 标签触发、三平台打包、原生校验、草稿下载复核及安装附件契约。 |

文中的 `https://gitea.example.com`、`team`、`owner/repository` 和编号均为示例，请替换为自己的实例、账号和目标。写操作会修改远端资源，默认要求预览确认。

程序生成的帮助和界面为英文。可以用 `sotea --help` 或 `sotea <command> --help` 查看当前安装版本的参数。
