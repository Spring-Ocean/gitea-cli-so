# 使用指南

在项目目录运行 `sotea` 进入交互工作台，或直接执行子命令。以下写入示例会先展示预览并要求确认，请替换为实际目标。

## 交互菜单

首页展示实例、仓库及经过 API 验证的用户信息。**Account** 维护账号，**Browse account** 浏览账户仓库、所属组织及组织仓库，**MCP integrations** 配置 AI 客户端。

| 操作 | 按键 |
| --- | --- |
| 移动、进入、返回 | ↑↓、Enter、Esc |
| 搜索候选 | `/` |
| 批量选择 | Space 勾选，再 Enter 继续 |
| 资源候选翻页 | 以页面提示为准，通常为 n／p |
| 详情分组切换 | PgUp／PgDn，或 Tab／Shift+Tab |
| 长分组内滚动 | ↑↓、Home／End |

左右键留给当前项的备选值，不用于切换详情组。结果留在界面中，Esc 返回上一级。终端太窄时布局会压缩；能力不足时使用可读的纯文本模式。

## 常用命令

| 命令组 | 主要能力 |
| --- | --- |
| `repo view` | 仓库详情和权限。 |
| `branch list`、`tag list`、`commit list`、`commit status` | 开发上下文与提交状态。 |
| `label list`、`milestone list` | 仓库标签与里程碑。 |
| `issue` | 列表、详情、创建、编辑、关闭／重开、评论、负责人及标签。 |
| `pr` | 列表、详情、文件／提交、创建／编辑、讨论、审查、请求审查、关闭／重开和合并。 |
| `release` | 草稿创建、列表、查看、编辑／发布、附件列表及上传。 |
| `git` | 将参数和三流交给系统 Git，保留 Git 退出码。 |
| `api` | 对已选实例发送明确的原始 API 请求。 |

用 `sotea issue --help` 或 `sotea pr merge --help` 查看当前版本参数。

## Issue

```sh
sotea issue list --state open --search bug --limit 20
sotea issue view 42
sotea issue create --title "Fix startup error" --body "Steps to reproduce..."
sotea issue comment add 42 --body "Verified in the test environment."
sotea issue assignee add 42 --user alice
sotea issue label add 42 --label bug
sotea issue close 42 43
sotea issue reopen 42
```

`issue close`／`reopen` 支持 1–100 个目标，批内去重。缺少编号时查询对应状态的候选供多选；查看、编辑、评论等是单资源操作。列表只包含 Issue，不把 PR 当 Issue 修改。

## Pull Request

```sh
sotea pr list --state open
sotea pr view 42
sotea pr files 42
sotea pr commits 42
sotea pr create --head feature/login --base main --title "Add login" --body "Implementation notes..."
sotea pr comment add 42 --body "Please check the error handling."
sotea pr review create 42 --event approve
sotea pr reviewer add 42 --user alice
sotea pr merge 42 --method squash
```

head 必须已推送，sotea 不自动 push 本地分支。跨仓 head 可明确写为 `owner/repository:branch`，由服务核对 fork 关系。

合并方式须是仓库允许的方式；不允许时不会自动换一种方法。合并前绑定已读取的 head SHA，变化后需要重新确认。删除源分支默认关闭，只有明确传 `--delete-branch` 才请求删除。已合并 PR 不能重新打开。

## Release

```sh
sotea release list
sotea release view v1.0.0
sotea release create v1.0.0 --target main --notes-file release-notes.md
sotea release asset upload v1.0.0 ./app.zip
sotea release asset list v1.0.0
sotea release edit v1.0.0 --publish
```

默认创建草稿，`--publish` 才明确发布；新标签需明确目标分支／提交。Release 以标签定位，不要求用户查内部 ID。附件一次上传一个文件，同名冲突不会自动覆盖或删除。上传失败后保留已有草稿及成功附件，先检查远端状态再补传。

## 分页与筛选

列表使用 `--page`、`--limit`；支持的列表可用 `--all` 读取后续页。共同上限为每页 100 项、有界扫描 10000 项，超限需缩小筛选范围。某些版本会通过共同兼容逻辑完成筛选，不能从单页数量推断总数。

```sh
sotea issue list --state open --label bug --page 2 --limit 20
sotea pr list --base main --author alice
sotea release list --search beta
```

## Git 与通用 API

```sh
sotea --workdir /path/to/project git -- status
sotea git -- log --oneline -5
sotea --instance team api /version
sotea --instance team api /repos/owner/repository/issues --query state=open --query limit=20
```

sotea 的公共选项放在 `git` 之前；`git` 后的参数原样传给系统 Git，不能在前面加业务 `--json` 期待转换 Git 输出。

`api` 的路径相对 `/api/v1`，使用 `--method` 指定方法；请求体可用 `--data`、`--input`，multipart 用 `--upload FIELD=FILE` 和 `--form NAME=VALUE`。它保留原始响应，不包裹业务 JSON；`--include` 增加状态和响应头。鉴权与 Host 头由上下文管理。

```sh
sotea --instance team api /repos/owner/repository/issues --method POST --input issue.json
```

非读取请求同样需要确认。下载到文件时使用 `--output`，目标已存在会失败，避免覆盖原文件。

## 自动化、JSON 与退出码

```sh
sotea --instance team --repo owner/repository issue list --json --no-interactive
sotea --instance team --repo owner/repository issue close 42 --json --no-interactive --yes
```

CI、管道、`--json` 或 `--no-interactive` 不会等待补参。写操作必须参数完整且明确 `--yes`；它只跳过普通写入确认，不替用户选择目标，也不替代首次实例来源或共享凭证范围确认。

业务 JSON 的顶层字段为 `schema_version`、`status`、`context`、`items`、`error`；当前 schema_version=1。结果可能为 success、partial、failed、cancelled、unknown；逐项还可能是 skipped 或 not_executed。诊断写入 stderr。`api`、`git` 和 MCP 分别保留自己的流语义。

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功，或目标已经处于期望状态。 |
| 1 | 认证、权限、网络、服务端或其他操作失败。 |
| 2 | 参数、配置、上下文、确认或交互条件不满足。 |
| 3 | 部分完成，仍有失败、未知或未执行项。 |
| 4 | 没有部分成功且写入结果未知。 |
| 130 | 用户取消／中断；已完成项仍在结果中保留。 |

取消不表示远端回滚。遇到 partial／unknown，保留成功项和资源 ID，先查询远端，不直接重放整条写命令。输出颜色可用 `--color auto|always|never` 控制；`NO_COLOR` 禁止自动着色，显式 `--color always` 优先。

[返回文档目录](README.md)
