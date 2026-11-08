package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	integration "github.com/Spring-Ocean/gitea-cli-so/internal/mcpintegration"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

// mcpIntegrations 提供独立于当前仓库可用性的客户端配置入口。
// 不从菜单启动长驻服务，不更改会话目标；错误在向导内展示后可以继续选择。
func (r *runtime) mcpIntegrations(command *cobra.Command) error {
	for {
		client, err := r.choose(command, "MCP integrations · Choose a client", []tui.Choice{
			{ID: string(integration.Codex), Label: "Codex", Description: "Configure the shared Codex TOML settings"},
			{ID: string(integration.ClaudeCode), Label: "Claude Code", Description: "Configure a project or user MCP server"},
			{ID: string(integration.Cursor), Label: "Cursor", Description: "Configure project or global MCP tools"},
			{ID: string(integration.VSCode), Label: "VS Code", Description: "Configure the standard profile or workspace MCP file"},
			{ID: "back", Label: "Back to Home", Description: "Return without changing the current repository or account"},
		})
		if err != nil || client == "back" {
			if err != nil && app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return nil
			}
			return err
		}
		if err := r.mcpClientScopes(command, integration.Client(client)); err != nil {
			if err := r.integrationFailure(command, err); err != nil {
				return err
			}
		}
	}
}

// mcpClientScopes 分别处理项目与用户范围，预览前允许修改具体配置位置。
// 标准 VS Code 配置档以外的位置须由用户明确输入，不猜测当前活动 profile。
func (r *runtime) mcpClientScopes(command *cobra.Command, client integration.Client) error {
	for {
		scope, err := r.choose(command, "MCP integrations · "+string(client)+" · Scope", []tui.Choice{
			{ID: "user", Label: "User", Description: "Make this connection available across projects; uses the standard profile"},
			{ID: "project", Label: "Project", Description: "Use one project directory; client trust and approval may still be required"},
			{ID: "back", Label: "Back to clients", Description: "Choose another AI client"},
		})
		if err != nil || scope == "back" {
			if err != nil && app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return nil
			}
			return err
		}
		path, err := r.mcpDestination(command, client, scope)
		if err == nil {
			err = r.mcpManage(command, client, path)
		}
		if err != nil {
			if err := r.integrationFailure(command, err); err != nil {
				return err
			}
		}
	}
}

// mcpDestination 逐步选择项目目录和可预览的目标配置文件。
// 空输入保留初值，取消返回范围选择；不会创建目录或读取客户端凭证。
func (r *runtime) mcpDestination(command *cobra.Command, client integration.Client, scope string) (string, error) {
	project := ""
	if scope == "project" {
		initial := r.options.workdir
		if initial == "" {
			var err error
			initial, err = os.Getwd()
			if err != nil {
				return "", app.Wrap(app.IOError, "Unable to locate the current directory.", err)
			}
		}
		value, err := r.accountInput(command, tui.InputOptions{
			Title: "MCP project", Step: string(client) + " · Project scope",
			Label: "Project directory", Value: initial,
			Description: "Choose the project that should receive this client configuration.",
		})
		if err != nil {
			return "", err
		}
		project, err = filepath.Abs(strings.TrimSpace(value))
		if err != nil {
			return "", app.Wrap(app.InvalidArgument, "Invalid project directory.", err)
		}
		info, err := os.Stat(project)
		if err != nil || !info.IsDir() {
			return "", app.Wrap(app.InvalidArgument, "Choose an existing project directory.", err)
		}
	}
	path, err := integration.Destination(client, project)
	if err != nil {
		return "", err
	}
	value, err := r.accountInput(command, tui.InputOptions{
		Title: "MCP configuration location", Step: string(client) + " · " + scope,
		Label: "Client configuration file", Value: path,
		Description: "Review the exact destination. Use another path only for a known client profile or custom configuration.",
	})
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) {
		return "", app.Wrap(app.InvalidArgument, "The client configuration file must be an absolute path.", nil)
	}
	return filepath.Clean(value), nil
}

// mcpManage 每轮重读实际配置，按托管状态提供安装、检查、更新和移除。
// 同名未托管或外部编辑的条目不提供可执行写操作，避免覆盖用户已有设置。
func (r *runtime) mcpManage(command *cobra.Command, client integration.Client, path string) error {
	for {
		snapshot, err := integration.Load(command.Context(), client, path)
		if err != nil {
			return err
		}
		choices := []tui.Choice{}
		switch snapshot.State {
		case "absent":
			choices = append(choices, tui.Choice{ID: "install", Label: "Set up integration", Description: "Choose context and permissions, then preview the exact entry"})
		case "managed":
			choices = append(choices,
				tui.Choice{ID: "check", Label: "Check connection", Description: "Initialize a temporary MCP process and discover tools; no business requests"},
				tui.Choice{ID: "update", Label: "Update integration", Description: "Review the context, executable and permissions before replacing this entry"},
				tui.Choice{ID: "remove", Label: "Remove integration", Description: "Remove only this managed sotea entry after preview and confirmation"},
			)
		default:
			choices = append(choices, tui.Choice{ID: "conflict", Label: "Existing entry needs attention", Description: "The entry is unmanaged or changed externally; sotea will preserve it"})
		}
		choices = append(choices, tui.Choice{ID: "back", Label: "Back to scope", Description: "Choose another configuration scope or location"})
		action, err := r.choose(command, "MCP integrations · "+string(client)+" · "+snapshot.State+"\n"+path, choices)
		if err != nil || action == "back" {
			if err != nil && app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return nil
			}
			return err
		}
		switch action {
		case "conflict":
			err = app.Wrap(app.ConfigConflict, "This sotea entry was configured elsewhere or changed after installation. Keep it unchanged; restore the installed entry and matching ownership record, or choose a different scope.", nil)
		case "check":
			err = r.mcpCheck(command, snapshot)
		case "remove":
			var change integration.Change
			change, err = snapshot.Prepare(integration.Launch{}, true)
			if err == nil {
				err = r.applyMCPChange(command, change, nil)
			}
		default:
			err = r.mcpSetup(command, snapshot)
		}
		if err != nil {
			if err := r.integrationFailure(command, err); err != nil {
				return err
			}
		}
	}
}

// mcpSetup 以可返回步骤补全启动上下文，所有字段只存在于本轮向导。
// 初次安装默认只读且无工作目录；更新使用已托管参数，不继承 Home 的隐式授权。
func (r *runtime) mcpSetup(command *cobra.Command, snapshot integration.Snapshot) error {
	launch := snapshot.Launch
	if snapshot.State == "absent" {
		launch.FollowWorkspace = true
		binary, err := os.Executable()
		if err != nil {
			return app.Wrap(app.IOError, "Unable to locate the running sotea executable.", err)
		}
		launch.Command = binary
		launch.ConfigPath, err = config.ResolvePath(r.options.configPath, nil)
		if err != nil {
			return err
		}
	}
	store := config.Store{Path: launch.ConfigPath}
	saved, err := store.Load(command.Context())
	if err != nil {
		return err
	}
	if len(saved.Config.Instances) == 0 {
		return app.Wrap(app.AuthRequired, "Connect and save a Gitea account from Home > Account before setting up MCP.", nil)
	}
	for step := 0; step <= 6; {
		var err error
		var value string
		switch step {
		case 0:
			choices := []tui.Choice{}
			names := sortedInstanceNames(saved.Config)
			for _, name := range names {
				choices = append(choices, tui.Choice{ID: name, Label: name, Description: saved.Config.Instances[name].URL})
			}
			var instance string
			instance, err = r.mcpChoose(command, "MCP setup · Instance", choices, launch.Instance)
			if err == nil && instance != launch.Instance {
				launch.Instance, launch.Account, launch.Repository = instance, "", ""
			}
		case 1:
			choices := []tui.Choice{}
			instance := saved.Config.Instances[launch.Instance]
			names := []string{}
			for name, account := range instance.Accounts {
				if !account.Invalid && !instance.LoggedOut[name] {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			for _, name := range names {
				source := instance.Accounts[name].Source
				description := "Saved credential source: " + source
				if source == "environment" {
					description += "; the client must receive the token securely in its own environment"
				}
				choices = append(choices, tui.Choice{ID: name, Label: "@" + name, Description: description})
			}
			if len(choices) == 0 {
				return app.Wrap(app.AuthRequired, "This instance has no available saved account. Connect or refresh an account from Home > Account first.", nil)
			}
			preferred := launch.Account
			if preferred == "" {
				preferred = instance.ActiveAccount
			}
			value, err = r.mcpChoose(command, "MCP setup · Account", choices, preferred)
			if err == nil {
				launch.Account = value
			}
		case 2:
			err = r.mcpLocalAccess(command, &launch)
		case 3:
			if launch.FollowWorkspace {
				launch.Repository = ""
				break
			}
			value, err = r.mcpRepository(command, saved.Config, launch)
			if err == nil {
				launch.Repository = value
			}
		case 4:
			err = r.mcpPermissions(command, &launch)
		case 5:
			value, err = r.accountInput(command, tui.InputOptions{
				Title: "MCP setup · Executable", Label: "sotea executable", Value: launch.Command,
				Description: "Use a stable absolute installation path. The client starts this binary automatically; do not move it afterward.",
			})
			if err == nil {
				launch.Command = value
			}
		case 6:
			verify := func() error {
				current, err := store.Load(command.Context())
				if err != nil {
					return err
				}
				if current.RevisionKey() != saved.RevisionKey() {
					return app.Wrap(app.ConfigConflict, "Gitea account settings changed during setup. Start the integration setup again.", nil)
				}
				return nil
			}
			if err := verify(); err != nil {
				return err
			}
			var change integration.Change
			change, err = snapshot.Prepare(launch, false)
			if err == nil {
				err = r.applyMCPChange(command, change, verify)
			}
		}
		if err != nil {
			if step == 6 && app.Failure(err).Code == app.ConfigConflict {
				return err
			}
			if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				if step == 0 {
					return nil
				}
				if step == 4 && launch.FollowWorkspace {
					step = 2
				} else {
					step--
				}
				continue
			}
			if err := r.integrationFailure(command, err); err != nil {
				return err
			}
			continue
		}
		step++
	}
	return nil
}

// sortedInstanceNames 按稳定名称顺序列出已登记实例。
// 排序只影响选择器展示，不改变配置默认项或账号顺序。
func sortedInstanceNames(saved config.Config) []string {
	names := []string{}
	for name := range saved.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// preferMCPChoice 将已有的明确选择作为首个焦点，保留其余选项的稳定次序。
// 更新或返回上一步时不以第一个字母顺序候选覆盖旧账号、仓库或本地授权根。
func preferMCPChoice(choices []tui.Choice, selected string) []tui.Choice {
	sort.SliceStable(choices, func(i, j int) bool {
		return choices[i].ID == selected && choices[j].ID != selected
	})
	return choices
}

// mcpChoose 以当前配置值而非导航历史确定向导字段的默认选择。
// 动态终端显式覆盖历史焦点，静态终端将当前项移到首位，两者均须 Enter 确认。
func (r *runtime) mcpChoose(command *cobra.Command, title string, choices []tui.Choice, selected string) (string, error) {
	if r.session != nil {
		return r.session.NavigateWithDefault(command.Context(), title, choices, selected)
	}
	return r.choose(command, title, preferMCPChoice(choices, selected))
}

// mcpRepository 提供无默认仓库、已有绑定及直接输入三种明确选择。
// 不将客户端启动 cwd 推断成仓库；留空意味着工具每次显式提供 repo。
func (r *runtime) mcpRepository(command *cobra.Command, saved config.Config, launch integration.Launch) (string, error) {
	choices := []tui.Choice{
		{ID: "none", Label: "No default repository", Description: "Choose a repository in each tool call; account browsing remains available"},
		{ID: "enter", Label: "Enter owner/repo", Description: "Set an explicit default repository without requiring a local checkout"},
	}
	seen := map[string]bool{}
	add := func(name string) {
		if target.ValidRepository(name) && !seen[name] {
			seen[name] = true
			choices = append(choices, tui.Choice{ID: "repo:" + name, Label: name, Description: "Use this explicit repository as the MCP default"})
		}
	}
	add(launch.Repository)
	for _, binding := range saved.Repositories {
		if binding.Instance == launch.Instance && (binding.Account == "" || binding.Account == launch.Account) {
			add(binding.Repository)
		}
	}
	choice, err := r.mcpChoose(command, "MCP setup · Optional repository", choices, "repo:"+launch.Repository)
	if err != nil || choice == "none" {
		return "", err
	}
	if strings.HasPrefix(choice, "repo:") {
		return strings.TrimPrefix(choice, "repo:"), nil
	}
	value, err := r.accountInput(command, tui.InputOptions{
		Title: "MCP repository", Label: "owner/repo", Value: launch.Repository,
		Description: "Enter the repository's full name. Access is checked when a tool uses it.",
	})
	if err == nil && !target.ValidRepository(value) {
		err = app.Wrap(app.InvalidArgument, "Use a valid owner/repo, or choose No default repository.", nil)
	}
	return value, err
}

// mcpLocalAccess 在跟随会话工作区、固定目录及仅远端之间选择授权方式。
// 跟随模式清除固定仓库与目录，防止用户在另一个项目启动客户端时仍操作旧项目。
func (r *runtime) mcpLocalAccess(command *cobra.Command, launch *integration.Launch) error {
	choices := []tui.Choice{
		{ID: "follow", Label: "Follow client workspace", Description: "Use each AI session's reported project; derive its repository from Git, with no fixed project path"},
		{ID: "none", Label: "Remote tools only", Description: "Do not authorize local Git queries or file access"},
		{ID: "enter", Label: "Choose a fixed directory", Description: "Always authorize this specific directory for fixed Git reads and eligible files"},
	}
	if launch.Workdir != "" {
		choices = append(choices, tui.Choice{ID: "keep", Label: "Keep " + launch.Workdir, Description: "Retain the previously authorized local root"})
	}
	preferred := "none"
	if launch.FollowWorkspace {
		preferred = "follow"
	} else if launch.Workdir != "" {
		preferred = "keep"
	}
	choice, err := r.mcpChoose(command, "MCP setup · Local access", choices, preferred)
	if err != nil {
		return err
	}
	if choice == "follow" {
		launch.FollowWorkspace, launch.Workdir, launch.Repository = true, "", ""
		return nil
	}
	if choice == "none" {
		launch.FollowWorkspace, launch.Workdir = false, ""
		return nil
	}
	if choice == "keep" {
		launch.FollowWorkspace = false
		return nil
	}
	value, err := r.accountInput(command, tui.InputOptions{
		Title: "MCP working directory", Label: "Absolute directory", Value: launch.Workdir,
		Description: "Only this directory and its real descendants are authorized. This does not grant arbitrary shell or Git writes.",
	})
	if err != nil {
		return err
	}
	info, err := os.Stat(value)
	if err != nil || !filepath.IsAbs(value) || !info.IsDir() {
		return app.Wrap(app.InvalidArgument, "Choose an existing absolute working directory.", err)
	}
	launch.Workdir, launch.FollowWorkspace = filepath.Clean(value), false
	return nil
}

// mcpPermissions 以独立开关管理业务写入与原始 API，不修改客户端批准策略。
// 初次安装两个开关都关闭；启用写入只授予服务能力，不等同用户批准每次操作。
func (r *runtime) mcpPermissions(command *cobra.Command, launch *integration.Launch) error {
	for {
		write, raw := "Off", "Off"
		if launch.AllowWrite {
			write = "On"
		}
		if launch.AllowRawAPI {
			raw = "On"
		}
		choice, err := r.choose(command, "MCP setup · Permissions", []tui.Choice{
			{ID: "continue", Label: "Continue with these permissions", Description: "Read tools are always enabled; client approval rules remain unchanged"},
			{ID: "write", Label: "Business writes · " + write, Description: "Toggle prepare, commit and status tools for Issue, PR and Release changes"},
			{ID: "raw", Label: "Raw API · " + raw, Description: "Toggle raw API reads; raw writes additionally require Business writes"},
		})
		if err != nil || choice == "continue" {
			return err
		}
		if choice == "write" {
			launch.AllowWrite = !launch.AllowWrite
		} else {
			launch.AllowRawAPI = !launch.AllowRawAPI
		}
	}
}

// applyMCPChange 展示目标与完整条目后要求独立的明确安装／移除选择。
// 不使用 --yes 绕过确认；verify 在确认后重核身份配置，成功报告备份位置和重载要求。
func (r *runtime) applyMCPChange(command *cobra.Command, change integration.Change, verify func() error) error {
	body := "Client: " + string(change.Before.Client) + "\nConfiguration: " + change.Before.Path + "\nAction: " + change.Action + " only the sotea entry\n\n" + change.Preview
	if r.session != nil {
		if err := r.session.Display(command.Context(), "Review MCP configuration", body); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(command.ErrOrStderr(), r.options.redactor.Text(body)); err != nil {
		return app.Wrap(app.IOError, "Unable to display the MCP configuration preview.", err)
	}
	choice, err := r.accountNotice(command, tui.NoticeOptions{
		Title:         change.Action + " MCP integration?",
		Message:       string(change.Before.Client) + "\n" + change.Before.Path,
		Hint:          "Only the reviewed sotea entry will change. Existing files receive a private backup; tokens and automatic approval rules are never added.",
		Kind:          output.StyleWarning,
		DefaultChoice: "back",
		Choices: []tui.Choice{
			{ID: "back", Label: "Back", Description: "Return without writing this configuration"},
			{ID: "apply", Label: change.Action + " integration", Description: "Apply the reviewed change to this exact configuration file"},
		},
	})
	if err != nil {
		return err
	}
	if choice != "apply" {
		return app.Wrap(app.Cancelled, "Integration setup cancelled. No configuration was written.", nil)
	}
	if verify != nil {
		if err := verify(); err != nil {
			return err
		}
	}
	backup, err := change.Apply(command.Context())
	if err != nil {
		return err
	}
	message := "The sotea integration was " + strings.ToLower(change.Action) + "d."
	if change.Action == "Install" {
		message = "The sotea integration was installed."
	}
	if backup != "" {
		message += "\nPrevious configuration backup: " + backup
	}
	_, err = r.accountNotice(command, tui.NoticeOptions{
		Title: "MCP configuration saved", Message: message, Kind: output.StyleSuccess,
		Hint: "Reload the client's MCP configuration or restart its server. Project trust and client approvals still apply. Removing an entry does not stop a server already running in the client.",
	})
	// 保存后的 Esc 仅关闭成功页，不再被外层当成提交前取消而退回编辑。
	if err != nil && app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
		return nil
	}
	return err
}

// mcpCheck 在可取消忙碌页执行临时协议连接检查，不向远端调用业务 API。
// 成功只证明启动及工具发现；客户端 UI、账号权限与审批需要在对应客户端另行确认。
func (r *runtime) mcpCheck(command *cobra.Command, snapshot integration.Snapshot) error {
	ctx, cancel := context.WithCancel(command.Context())
	defer cancel()
	if r.session != nil {
		if err := r.session.Busy(ctx, "Checking MCP initialization and tools...", cancel); err != nil {
			return err
		}
	}
	result, err := snapshot.Check(ctx)
	if err != nil {
		return err
	}
	_, err = r.accountNotice(command, tui.NoticeOptions{
		Title: "MCP connection ready", Kind: output.StyleSuccess,
		Message: fmt.Sprintf("%s %s · %d tools discovered", result.Server, result.Version, result.Tools),
		Hint:    "The temporary process has stopped. No business tools were called. Check account access and approval behavior inside the client separately.",
	})
	return err
}

// integrationFailure 统一处理向导错误和取消，保留用户继续修复的入口。
// 取消只返回上级导航；真正的上下文取消向上传递以结束会话。
func (r *runtime) integrationFailure(command *cobra.Command, err error) error {
	if command.Context().Err() != nil {
		return app.Failure(command.Context().Err())
	}
	if app.Failure(err).Code == app.Cancelled {
		return nil
	}
	_, noticeErr := r.accountNotice(command, failureNotice(app.Failure(err)))
	return noticeErr
}
