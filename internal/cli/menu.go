package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

var menuBack = errors.New("Return to the parent menu.")

// menuState 保存当前会话的显示历史，不保存凭证或可复用写计划。
// 配置每轮重读；last 保留逐项 unknown／partial，返回菜单不会改写业务结果。
type menuState struct {
	revision       string
	loaded         bool
	last           *app.Result
	outcome        string
	resultOutcome  string
	ready          bool
	availability   string
	pendingOutcome string
	home           tui.HomeInfo
}

// menu 在具备 VT 能力时保持唯一终端会话，结果和提示都在屏内展示。
// 静态降级保留编号交互；原始流操作显式交接终端，不解析 CLI 文本。
func (r *runtime) menu(command *cobra.Command, version string) error {
	if !output.CapableTerminal(os.Getenv("TERM")) {
		return r.menuLoop(command, version)
	}
	input, out, diagnostic := command.InOrStdin(), command.OutOrStdout(), command.ErrOrStderr()
	defer func() {
		command.SetIn(input)
		command.SetOut(out)
		command.SetErr(diagnostic)
		r.session = nil
	}()
	return tui.RunSession(command.Context(), input, diagnostic, r.options.color, r.options.redactor.Text, func(ctx context.Context, session *tui.Session) error {
		r.session = session
		command.SetContext(ctx)
		command.SetOut(session)
		command.SetErr(session)
		r.rawOut, r.rawErr = out, diagnostic
		return r.menuLoop(command, version)
	})
}

// menuLoop 持续加载上下文并保持根导航，不将动作结果刷入 shell。
// 不可验证的平台或仓库仅保留修复入口；业务每次仍重新验证身份。
func (r *runtime) menuLoop(command *cobra.Command, version string) error {
	return r.menuWorkspace(command, version, false)
}

// menuWorkspace 在根入口或账户浏览下展示同一仓库工作台。
// 浏览进入的工作台固定远端目标，Esc 返回列表；不将原目录的 Git 作为远端仓库操作。
func (r *runtime) menuWorkspace(command *cobra.Command, version string, browsed bool) error {
	state := &menuState{}
	for {
		if browsed {
			if _, _, _, err := r.resolve(command, r.input(false), false); err != nil {
				return err
			}
		}
		if err := command.Context().Err(); err != nil {
			return app.Failure(err)
		}
		header, err := r.menuContext(command, state)
		if err != nil {
			return err
		}
		if r.session != nil {
			r.session.Header(header)
			ctx, cancel := context.WithCancel(command.Context())
			if err := r.session.Busy(command.Context(), "Checking Gitea and repository...", cancel); err != nil {
				cancel()
				return err
			}
			r.checkMenuTarget(command, ctx, state)
			cancel()
			r.session.Header(header + " · " + state.availability)
			if notice := r.session.Drain(); strings.TrimSpace(notice) != "" {
				if err := r.session.Display(command.Context(), "Context update", notice); err != nil {
					return err
				}
			}
		}
		choices := menuChoices(command.Root())
		if browsed {
			filtered := []tui.Choice{}
			for _, choice := range choices {
				if choice.ID != "git" {
					filtered = append(filtered, choice)
				}
			}
			choices = filtered
		}
		if r.session != nil && !state.ready {
			filtered := []tui.Choice{}
			for _, choice := range choices {
				if choice.ID == "auth" || choice.ID == "instance" || choice.ID == "git" || choice.ID == "version" {
					filtered = append(filtered, choice)
				}
			}
			choices = filtered
			choices = append(choices, tui.Choice{
				ID:          "@availability",
				Label:       "Repository unavailable",
				Description: "Inspect the connection problem and available recovery steps",
			})
		}
		if !browsed {
			choices = append([]tui.Choice{{
				ID:          "@browse",
				Label:       "Browse account",
				Description: "Browse your repositories and organizations, then open a workspace",
			}}, choices...)
			choices = append(choices, tui.Choice{
				ID:          "@context",
				Label:       "Context",
				Description: "Switch the instance, repository, account or remote for this session",
			}, tui.Choice{
				ID:          "@save",
				Label:       "Save defaults",
				Description: "Review and save the current context for future sessions",
			})
		}
		if state.last != nil {
			choices = append(choices, tui.Choice{
				ID:          "@last",
				Label:       "Last business result",
				Description: "Review the latest result: " + state.resultOutcome,
			})
		}
		choices = append(choices, tui.Choice{
			ID:          "@retry",
			Label:       "Refresh repository and identity",
			Description: "Check the connection, signed-in account and repository again",
		}, tui.Choice{
			ID:          "@quit",
			Label:       "Quit",
			Description: "Close sotea and return to your terminal",
		})
		if browsed {
			choices[len(choices)-1].Label = "Back to repository list"
			choices[len(choices)-1].Description = "Leave this workspace and resume browsing repositories"
		}
		title := "Home · " + header
		if r.session != nil {
			title = "Home · " + state.availability
		}
		var choice string
		if r.session != nil {
			state.home.Version = version
			state.home.LastResult = state.resultOutcome
			choice, err = r.session.Home(command.Context(), state.home, choices)
		} else {
			choice, err = r.choose(command, title, choices)
		}
		if err != nil {
			if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return nil
			}
			return err
		}
		switch choice {
		case "auth":
			err = r.accountMenu(command, version)
			if err == nil {
				state.loaded = false
			}
		case "@quit":
			return nil
		case "@browse":
			err = r.browseAccount(command, version)
			if err == nil {
				state.loaded = false
			}
		case "@retry":
			continue
		case "@availability":
			if r.session != nil {
				err = r.session.Display(command.Context(), "Repository unavailable", state.availability+"\nUse Context to select a Gitea instance and repository, Account to sign in, or Refresh to retry.\nDetection errors do not establish that the server is not Gitea.")
			}
		case "@context":
			err = r.switchContext(command)
		case "@save":
			err = r.saveMenuContext(command)
		case "@last":
			state.pendingOutcome = state.resultOutcome
			err = r.printResult(command, *state.last)
		default:
			action, _, findErr := command.Root().Find(strings.Split(choice, " "))
			if findErr != nil || action == command.Root() {
				return app.Wrap(app.InvalidArgument, "The menu action is unavailable.", findErr)
			}
			if action.RunE == nil {
				err = r.menuGroup(command, version, action, state)
			} else {
				err = r.menuAction(command, version, choice, state)
			}
		}
		if errors.Is(err, menuBack) {
			continue
		}
		if err := r.finishMenuAction(command, err, state); err != nil {
			return err
		}
	}
}

// finishMenuAction 展示本次结果或错误，已执行结果不会因返回而丢失。
// 取消导航不制造失败页；类型化失败已经渲染，不重复错误摘要。
func (r *runtime) finishMenuAction(command *cobra.Command, err error, state *menuState) error {
	if err != nil {
		var printed *printedResultError
		var process *git.ProcessError
		if !errors.As(err, &printed) && !errors.As(err, &process) && app.Failure(err).Code != app.Cancelled {
			if r.session != nil {
				r.session.Drain()
				state.outcome = string(app.Failure(err).Code)
				state.pendingOutcome = ""
				return r.showMenuFailure(command, app.Failure(err))
			}
			if writeErr := r.menuError(command, err, state); writeErr != nil {
				return writeErr
			}
		}
	}
	if r.session != nil {
		body := r.session.Drain()
		title := "Result"
		if state.pendingOutcome != "" {
			title = state.pendingOutcome
			if strings.TrimSpace(body) == "" {
				body = "The command has finished."
				if err != nil && app.Failure(err).Code == app.Cancelled {
					body = "The action was cancelled."
				}
			}
			state.pendingOutcome = ""
		}
		if strings.TrimSpace(body) != "" {
			return r.session.Display(command.Context(), title, body)
		}
	}
	return nil
}

// checkMenuTarget 先匿名探测版本，再按已登记来源读取身份和仓库。
// 401、404、网络和代理失败只报告无法验证，不将检测失败断言为非 Gitea。
func (r *runtime) checkMenuTarget(command *cobra.Command, ctx context.Context, state *menuState) {
	state.ready = false
	state.home = tui.HomeInfo{}
	defer func() {
		state.home.Status = state.availability
		if state.ready && state.home.Login != "" {
			state.home.Status = "Connected · Gitea " + state.home.ServerVersion
		}
	}()
	t, _, service, err := r.resolve(command, r.input(false), false)
	if err != nil {
		state.availability = "Unavailable · " + app.Failure(err).Message
		return
	}
	state.home.Instance = t.Instance.URL
	state.home.Repository = t.Repository
	state.home.Directory = t.Workdir
	if r.options.browseScope != nil {
		state.home.Directory = "Remote workspace · No local checkout"
	}
	var profile *auth.Profile
	client, err := gitea.New(gitea.Options{
		BaseURL:  t.Instance.URL,
		CAFile:   t.Instance.CAPath,
		ProxyURL: t.Instance.Proxy,
	})
	if err != nil {
		state.availability = "Unable to verify · " + app.Failure(err).Message
		return
	}
	info, err := compat.Detect(ctx, client, t.Instance.URL)
	client.Close()
	if err != nil && t.Registered && (app.Failure(err).Code == app.AuthRequired || app.Failure(err).Code == app.PermissionDenied) {
		// 已登记的完整根地址可以重新验证已授权身份；未知来源绝不走此分支。
		client, t, profile, err = service.OpenProfile(ctx, t, true)
		if err == nil {
			info, err = compat.Detect(ctx, client, t.Instance.URL)
			client.Close()
		}
	}
	if err != nil {
		state.availability = "Unable to verify Gitea · " + string(app.Failure(err).Code) + " · " + app.Failure(err).Message
		return
	}
	if t.Registered {
		client, t, profile, err = service.OpenProfile(ctx, t, true)
	} else {
		client, err = gitea.New(gitea.Options{
			BaseURL:  t.Instance.URL,
			CAFile:   t.Instance.CAPath,
			ProxyURL: t.Instance.Proxy,
		})
	}
	state.home.ServerVersion = info.Raw
	if profile != nil {
		state.home.Login = profile.Login
		state.home.Name = profile.FullName
		state.home.Email = profile.Email
	}
	if err == nil {
		defer client.Close()
		if !target.ValidRepository(t.Repository) {
			state.availability = "Gitea " + info.Raw + " · No repository selected"
			return
		}
		parts := strings.Split(t.Repository, "/")
		var response *gitea.Response
		response, err = client.Do(ctx, gitea.Request{Path: "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])})
		if err == nil {
			var repository struct {
				FullName string `json:"full_name"`
			}
			err = gitea.Decode(response, &repository)
			if err == nil && !strings.EqualFold(repository.FullName, t.Repository) {
				err = app.Wrap(app.ContextRequired, "The server did not identify the selected repository.", nil)
			}
		}
	}
	if err != nil {
		state.availability = "Gitea " + info.Raw + " · Repository unavailable · " + string(app.Failure(err).Code) + " · " + app.Failure(err).Message
		return
	}
	state.ready = true
	state.availability = "Gitea " + info.Raw
	if profile != nil {
		state.availability += " · Signed in as " + t.Account
	} else {
		state.availability += " · Anonymous"
		if !t.Registered {
			state.availability += " · Source confirmation required"
		}
	}
}

// menuChoices 从实际注册命令生成完整入口，避免维护会漂移的平行命令表。
// MCP 是独立进程协议服务，不从持续终端菜单启动；隐藏命令和帮助不作为业务动作。
func menuChoices(parent *cobra.Command) []tui.Choice {
	choices := []tui.Choice{}
	for _, command := range parent.Commands() {
		if command.Hidden || command.Name() == "mcp" || command.Name() == "help" || command.Name() == "completion" {
			continue
		}
		path := strings.TrimPrefix(command.CommandPath(), parent.Root().Name()+" ")
		label, description, _ := strings.Cut(menuLabel(command), " · ")
		choices = append(choices, tui.Choice{
			ID:          path,
			Label:       label,
			Description: description,
		})
	}
	if parent == parent.Root() {
		priority := map[string]int{
			"repo":      0,
			"issue":     1,
			"pr":        2,
			"release":   3,
			"branch":    4,
			"tag":       5,
			"commit":    6,
			"label":     7,
			"milestone": 8,
			"auth":      9,
			"instance":  10,
			"git":       11,
			"api":       12,
			"version":   13,
		}
		sort.SliceStable(choices, func(i, j int) bool {
			left, leftKnown := priority[choices[i].ID]
			right, rightKnown := priority[choices[j].ID]
			if !leftKnown {
				left = len(priority)
			}
			if !rightKnown {
				right = len(priority)
			}
			return left < right
		})
	}
	return choices
}

// menuLabel 将命令名转换为面向用户的导航标题，稳定路径仍作为内部 ID。
// 描述沿用已注册命令，避免维护另一套业务命令范围。
func menuLabel(command *cobra.Command) string {
	names := map[string]string{
		"repo":      "Repository · Overview, access and clone URLs",
		"issue":     "Issues · Track and manage work",
		"pr":        "Pull requests · Review and merge changes",
		"release":   "Releases · Versions and release assets",
		"branch":    "Branches · Browse the repository branches",
		"tag":       "Tags · Browse version tags in the repository",
		"commit":    "Commits · Inspect commits and their checks",
		"label":     "Labels · Browse labels used to organize issues and pull requests",
		"milestone": "Milestones · Browse milestones and planned work",
		"auth":      "Account · Connect, verify, switch or disconnect a Gitea account",
		"instance":  "Instance · Inspect Gitea version and compatibility evidence",
		"git":       "Git · Run Git in the local repository using literal arguments",
		"api":       "API · Call a Gitea endpoint with explicit request options",
		"version":   "Version · Show the installed sotea version",
	}
	if command.Parent() == command.Root() {
		if label := names[command.Name()]; label != "" {
			return label
		}
	}
	path := strings.TrimPrefix(command.CommandPath(), command.Root().Name()+" ")
	descriptions := map[string]string{
		"repo view":            "Repository details, access and clone addresses",
		"issue list":           "Browse and filter issues",
		"issue view":           "Read the description and current status",
		"issue create":         "Describe a new issue",
		"issue edit":           "Update an issue's title, description and fields",
		"issue close":          "Close one or more selected issues",
		"issue reopen":         "Reopen one or more selected issues",
		"pr list":              "Browse and filter pull requests",
		"pr view":              "Read the proposal and current review status",
		"pr create":            "Open a pull request between branches",
		"pr edit":              "Update the title, description and fields",
		"pr merge":             "Review the merge plan before applying it",
		"release list":         "Browse published releases and drafts",
		"release view":         "Read release notes and version details",
		"release create":       "Prepare release notes and a new draft",
		"release edit":         "Update the release details and notes",
		"release publish":      "Publish a reviewed draft release",
		"release asset":        "Browse attachments or upload a file",
		"release asset list":   "Browse files attached to a release",
		"release asset upload": "Choose a file to attach to a release",
	}
	name := strings.ReplaceAll(command.Name(), "-", " ")
	description := command.Short
	if friendly, exists := descriptions[path]; exists {
		description = friendly
	}
	return strings.ToUpper(name[:1]) + name[1:] + " · " + description
}

// menuGroup 保持当前层级，动作结果查看结束后回到同一子菜单。
// Esc 返回直接父层；可选字段入口独立存在，只读动作 Enter 即执行默认查询。
func (r *runtime) menuGroup(command *cobra.Command, version string, group *cobra.Command, state *menuState) error {
	for {
		choices := menuChoices(group)
		choices = append(choices, tui.Choice{
			ID:          "@options",
			Label:       "Run with options",
			Description: "filters and advanced fields",
		})
		choice, err := r.choose(command, group.CommandPath(), choices)
		if err != nil {
			if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return menuBack
			}
			return err
		}
		withOptions := false
		if choice == "@options" {
			choice, err = r.choose(command, "Choose an action to configure", menuChoices(group))
			if err != nil {
				if app.Failure(err).Code == app.Cancelled {
					continue
				}
				return err
			}
			withOptions = true
		}
		action, _, err := command.Root().Find(strings.Split(choice, " "))
		if err != nil {
			return err
		}
		if action.RunE != nil {
			if withOptions {
				choice = "@options " + choice
			}
			err = r.menuAction(command, version, choice, state)
		} else {
			err = r.menuGroup(command, version, action, state)
		}
		if errors.Is(err, menuBack) {
			continue
		}
		if err := r.finishMenuAction(command, err, state); err != nil {
			return err
		}
	}
}

// menuAction 构造一次性参数状态并直接调用共享 RunE 适配器。
// 成功结果的持续页省略退出摘要，失败／unknown仍保留；实际状态通过lastResult保存，原始流不变。
func (r *runtime) menuAction(parent *cobra.Command, version, path string, state *menuState) error {
	withOptions := strings.HasPrefix(path, "@options ")
	path = strings.TrimPrefix(path, "@options ")
	root, actionRuntime := newRootCommand(version)
	actionRuntime.session = r.session
	actionRuntime.rawOut, actionRuntime.rawErr = r.rawOut, r.rawErr
	actionCtx, cancel := context.WithCancel(parent.Context())
	defer cancel()
	if r.session != nil {
		if err := r.session.Busy(parent.Context(), path, cancel); err != nil {
			return err
		}
	}
	initial := *r.options
	initial.lastResult = nil
	*actionRuntime.options = initial
	// 仅保留用户明确补齐的维度；配置和 remote 推导值继续在下一轮重新解析。
	actionRuntime.options.onResolve = func(input target.Input) {
		r.options.instance = input.Instance
		r.options.repo = input.Repo
		r.options.account = input.Account
		r.options.remote = input.Remote
	}
	root.SetIn(parent.InOrStdin())
	root.SetOut(parent.OutOrStdout())
	root.SetErr(parent.ErrOrStderr())
	root.SetContext(actionCtx)
	command, remaining, err := root.Find(strings.Split(path, " "))
	if err != nil || len(remaining) != 0 || command.RunE == nil {
		return app.Wrap(app.InvalidArgument, "The selected command is unavailable.", err)
	}
	command.SetContext(actionCtx)
	args := []string{}
	if path == "git" {
		args, err = actionRuntime.gitArguments(command)
	} else {
		if withOptions || path == "api" || menuNeedsFields(path) {
			err = actionRuntime.editFlags(command)
		}
		if err == nil && path == "api" {
			var endpoint string
			endpoint, err = actionRuntime.ask(command, "API path (relative to the selected instance API)")
			args = append(args, endpoint)
		}
	}
	if err != nil {
		return err
	}
	if err := command.ValidateArgs(args); err != nil {
		return app.Wrap(app.InvalidArgument, "Invalid action arguments.", err)
	}
	if err := root.PersistentPreRunE(command, args); err != nil {
		return err
	}
	if path == "git" && r.session != nil {
		command.SetOut(r.rawOut)
		command.SetErr(r.rawErr)
		err = r.session.External(parent.Context(), func() error {
			return command.RunE(command, args)
		})
	} else {
		if path == "api" && r.session != nil {
			command.SetOut(r.rawOut)
		}
		err = command.RunE(command, args)
	}
	result := actionRuntime.options.lastResult
	code := app.ExitCode(err)
	if result != nil {
		code = result.ExitCode()
	}
	var printed *printedResultError
	if errors.As(err, &printed) {
		code = printed.code
	}
	var process *git.ProcessError
	if errors.As(err, &process) {
		code = process.ExitCode()
	}
	state.outcome = fmt.Sprintf("%s · exit %d", path, code)
	if result != nil {
		state.outcome += " · " + string(result.Status)
		state.last = result
	}
	if r.session != nil && code == 0 && (result == nil || result.Status == app.Success) {
		state.outcome = path
	}
	if result != nil {
		state.resultOutcome = state.outcome
	}
	state.pendingOutcome = state.outcome
	if r.session == nil {
		if _, writeErr := fmt.Fprintln(parent.ErrOrStderr(), state.outcome); writeErr != nil {
			return app.Wrap(app.IOError, "Unable to report the menu action outcome.", writeErr)
		}
	}
	return err
}

// menuError 展示未输出的动作错误，保留本轮失败或取消摘要。
// 已有类型化结果不被替换为成功；原始 Git 的退出状态也保留在 outcome 中。
func (r *runtime) menuError(command *cobra.Command, err error, state *menuState) error {
	var process *git.ProcessError
	if errors.As(err, &process) {
		return nil
	}
	failure := app.Failure(err)
	state.outcome = string(failure.Code) + " · " + failure.Message
	state.pendingOutcome = state.outcome
	render := renderer(command)
	render.Redactor = r.options.redactor
	return render.Error(err)
}

// menuContext 每轮重新解析非敏感目标及配置来源，不缓存认证对象。
// 外部配置内容变化立即显示提示；后续动作仍会重新检查账号退出、身份及凭证有效性。
func (r *runtime) menuContext(command *cobra.Command, state *menuState) (string, error) {
	t, snapshot, _, err := r.resolve(command, r.input(false), false)
	if snapshot.Config.Version == 0 {
		return "", err
	}
	revision := snapshot.RevisionKey()
	if state.loaded && revision != state.revision {
		if _, writeErr := fmt.Fprintln(command.ErrOrStderr(), "Configuration changed. Context and identity will be checked again."); writeErr != nil {
			return "", app.Wrap(app.IOError, "Unable to report configuration changes.", writeErr)
		}
	}
	state.revision, state.loaded = revision, true
	if err != nil {
		return "Context unresolved · use Context to select an instance and repository", nil
	}
	account := "Anonymous"
	if t.Account != "" {
		account = t.Account + " (not yet verified)"
	} else if t.Sources["account"] == "environment" {
		account = "Environment credential (not yet verified)"
	}
	repository := t.Repository
	if repository == "" {
		repository = "No repository selected"
	}
	text := fmt.Sprintf("%s · %s · %s", t.Instance.URL, repository, account)
	if r.session != nil {
		text = fmt.Sprintf("%s · %s", t.Instance.URL, repository)
	}
	return r.options.redactor.Text(text), nil
}

// switchContext 只修改明确选择的会话维度，不自动保存默认项。
// 切换实例会清除旧的显式仓库和账号；所有业务动作仍按解析优先级重新验证来源。
func (r *runtime) switchContext(command *cobra.Command) error {
	field, err := r.choose(command, "Session context · changes are not saved", []tui.Choice{{
		ID:    "instance",
		Label: "Instance · require a new repository; stop local context inference",
	}, {
		ID:    "repo",
		Label: "Repository · owner/repo",
	}, {
		ID:    "account",
		Label: "Account · explicit account, verified by the next operation",
	}, {
		ID:    "remote",
		Label: "Git remote · explicit fetch remote",
	}, {
		ID:          "inspect",
		Label:       "Inspect resolved context and sources",
		Description: "Show the current instance, repository, account and where each value came from",
	}, {
		ID:    "reset",
		Label: "Reset session overrides · return to environment and configuration",
	}})
	if err != nil {
		return err
	}
	if field == "inspect" {
		t, _, _, err := r.resolve(command, r.input(false), false)
		if err != nil {
			return err
		}
		return r.businessPreview(command, t.Context(), map[string]any{
			"instance":   t.Name,
			"url":        t.Instance.URL,
			"repository": t.Repository,
			"account":    t.Account,
			"sources":    t.Sources,
			"workdir":    t.Workdir,
			"remote":     t.Remote,
		})
	}
	if field == "reset" {
		r.options.instance, r.options.repo, r.options.account, r.options.remote = "", "", "", ""
		r.options.noLocal = false
		return nil
	}
	value, err := r.ask(command, "New "+field)
	if err != nil {
		return err
	}
	switch field {
	case "instance":
		if _, err := target.NormalizeURL(value); err != nil && strings.Contains(value, "://") {
			return err
		}
		r.options.instance = value
		r.options.noLocal = true
		r.options.repo, r.options.account, r.options.remote = "", "", ""
	case "repo":
		if !target.ValidRepository(value) {
			return app.Wrap(app.InvalidArgument, "Use a valid owner/repo.", nil)
		}
		r.options.repo = value
	case "account":
		r.options.account = value
	case "remote":
		r.options.remote = value
	}
	return nil
}

// saveMenuContext 显式保存当前已注册实例、有效账号及匹配本地仓库的默认项。
// 保存前核对身份和快照，冲突不覆盖；来源尚未确认或本地不匹配时不能伪造仓库绑定。
func (r *runtime) saveMenuContext(command *cobra.Command) error {
	t, snapshot, service, err := r.resolve(command, r.input(false), false)
	if err != nil {
		return err
	}
	if !t.Registered {
		return app.Wrap(app.InvalidArgument, "Log in or confirm and read the instance before saving defaults.", nil)
	}
	if t.Account != "" {
		if _, exists := t.Instance.Accounts[t.Account]; !exists {
			return app.Wrap(app.InvalidArgument, "Save and verify the account with auth login first.", nil)
		}
		client, verified, err := service.Open(command.Context(), t, false)
		if err != nil {
			return err
		}
		client.Close()
		t = verified
	}
	if err := r.businessPreview(command, app.Context{
		Instance:   t.Name,
		Repository: t.Repository,
		Account:    t.Account,
	}, map[string]any{
		"default_instance": t.Name,
		"active_account":   t.Account,
		"local_binding":    t.Local != nil && t.LocalMatches,
		"sources":          t.Sources,
	}); err != nil {
		return err
	}
	if err := r.confirm(command, "Save these exact defaults", false); err != nil {
		return err
	}
	next := snapshot.Config
	next.DefaultInstance = t.Name
	instance := next.Instances[t.Name]
	if t.Account != "" {
		instance.ActiveAccount = t.Account
		next.Instances[t.Name] = instance
	}
	if t.Local != nil && t.LocalMatches {
		binding := config.Binding{
			Path:       t.Local.Root,
			Remote:     t.Remote,
			Instance:   t.Name,
			Repository: t.Repository,
			Account:    t.Account,
		}
		found := false
		for index := range next.Repositories {
			if next.Repositories[index].Path == binding.Path && next.Repositories[index].Remote == binding.Remote {
				next.Repositories[index] = binding
				found = true
			}
		}
		if !found {
			next.Repositories = append(next.Repositories, binding)
		}
	}
	_, err = service.Store.Save(command.Context(), snapshot, next)
	return err
}

// gitArguments 逐个收集 Git 参数并展示参数数组，不经 shell 或空白拆分。
// 每个参数可包含空格；明确 Done 后交给系统 Git，零参数也须用户明确接受。
func (r *runtime) gitArguments(command *cobra.Command) ([]string, error) {
	args := []string{}
	for {
		action, err := r.choose(command, fmt.Sprintf("Git arguments: %q", args), []tui.Choice{{
			ID:          "add",
			Label:       "Add one literal argument",
			Description: "Enter one Git argument; spaces are preserved without shell parsing",
		}, {
			ID:          "done",
			Label:       "Done",
			Description: "Run Git with the displayed arguments and hand over the terminal",
		}})
		if err != nil {
			return nil, err
		}
		if action == "done" {
			return args, nil
		}
		value, err := r.askLine(command, "One literal Git argument (empty is an explicit empty argument)")
		if err != nil {
			return nil, err
		}
		args = append(args, value)
	}
}

// menuNeedsFields 保留缺参无法覆盖的可配置写入入口，默认只读不先展示字段表。
// edit 命令在选择资源后由原适配器补齐字段，避免重复填写。
func menuNeedsFields(path string) bool {
	parts := strings.Fields(path)
	if len(parts) == 0 {
		return false
	}
	switch parts[len(parts)-1] {
	case "list", "view", "status", "info", "edit", "switch", "close", "reopen", "logout", "delete", "publish":
		return false
	default:
		return path != "version"
	}
}
