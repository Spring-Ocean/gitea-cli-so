# Windows 验证指南

Windows 10 22H2 amd64 和 Windows 11 使用同一候选 exe，分别执行并留存结果。当前尚无这两个系统的完整实测记录；本指南用于完成发布前验证。

## 基础检查

先按 [安装说明](installation.md)核验并解压候选包，在 64 位 PowerShell 中运行：

```powershell
.\sotea.exe version --json
.\sotea.exe --help
```

维护者提供的验收包可以包含 `windows-p7-smoke.ps1` 和 `BUILD-INFO.json`。在同时包含脚本、manifest 和 exe 的解压目录执行：

```powershell
.\windows-p7-smoke.ps1 -Result .\windows10-powershell51.json
```

Windows 11 用 PowerShell 5.1 和 7.6 分别执行，使用不同结果名。源码目录内的脚本也可以显式指向解压文件：

```powershell
.\scripts\windows-p7-smoke.ps1 -Binary 'C:\Test\sotea\sotea.exe' -Manifest 'C:\Test\sotea\BUILD-INFO.json' -Result '.\windows11-powershell51.json'
```

脚本拒绝覆盖已有记录。它核对 exe 摘要，记录系统／PowerShell／Git，在临时目录验证配置、中文空格路径、Git 退出码、File.Replace 文件占用保护、默认只读 MCP 工具发现与 EOF。不会读取实际账号或发送业务请求；没有 Git 时保留 Git 项待测。

该脚本目前尚未在 Windows 执行，基础检查成功也只标为 `smoke_passed_full_acceptance_pending`。若执行策略阻止运行，按组织策略处理，不要求关闭系统保护。

## 手工检查

以下远端操作使用隔离 Gitea 实例和专用测试账号／仓库。不要将开发者或团队生产资源作为写入验收数据。

| 范围 | 需要实际验证的内容 |
| --- | --- |
| 安装 | 用户 PATH、新窗口启动、版本与候选摘要一致。 |
| 认证 | Account 引导、token 保存／刷新／切换／退出、Windows Credential Manager 清理。 |
| Git | Git for Windows／helper、中文空格路径、worktree、多 remote、退出抑制。 |
| API 与文件 | 仓库读取、分页、私有 CA、权限错误、上传下载字节及已有目标不覆盖。 |
| 菜单与输出 | Enter／Esc、搜索、跨页多选、确认前取消无写入、Markdown、中文宽度、缩放、Ctrl+C 恢复。 |
| 终端组合 | Windows Terminal + PowerShell 5.1；Windows 11 另测 7.6；传统控制台纯文本降级。 |
| Issue／PR／Release | 完整业务、失败／部分完成／未知结果；按本轮明确资源 ID 清理。 |
| 四类 AI 客户端 | 工具发现、读取、写计划、批准／拒绝、取消、错误后继续与退出清理。 |
| 多项目 | 同一用户配置切换两个项目，工作区不串用，根外路径拒绝，旧计划失效。 |
| 升级和卸载 | 进程占用／失败保护、配置保留、备份恢复、自有接入项与凭证清理。 |

## 保留结果

每个系统／shell 组合单独保留 JSON，手工项目另记 passed、failed 或 not_tested。提供实际 exe SHA-256、系统 build、CPU 架构、Git／helper、终端及客户端版本，以及脱敏的操作和错误。

取消和结果未知不代表远端回滚。先查询已有资源再处理，避免重复写入；基础脚本不会替你清理手工创建的远端资源。

[返回文档目录](README.md)
