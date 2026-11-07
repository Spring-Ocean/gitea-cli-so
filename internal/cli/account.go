package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

// accountSnapshotCurrent 在向导读取凭证前核对最初展示的配置快照。
// 外部切换或退出立即使本轮向导失效；最终保存仍由Store的原子比较保护并发写入窗口。
func accountSnapshotCurrent(ctx context.Context, service auth.Service, snapshot config.Snapshot) error {
	current, err := service.Store.Load(ctx)
	if err != nil {
		return err
	}
	if current.RevisionKey() != snapshot.RevisionKey() {
		return app.Wrap(app.ConfigConflict, "Account settings changed during setup. Return and start the connection again.", nil)
	}
	return nil
}

// accountInput 将引导字段放进统一输入框，静态终端保留安全逐步输入。
// 初值只用于向导；Secret不回显，取消由调用者决定返回哪一步，不当成空输入。
func (r *runtime) accountInput(command *cobra.Command, options tui.InputOptions) (string, error) {
	if r.session != nil {
		return r.session.Input(command.Context(), options)
	}
	if _, err := fmt.Fprintln(command.ErrOrStderr(), output.SingleLine(options.Step+" · "+options.Description)); err != nil {
		return "", app.Wrap(app.IOError, "Unable to display account setup.", err)
	}
	if options.Secret {
		return r.secureToken(command)
	}
	label := options.Label
	if options.Value != "" {
		label += " [" + options.Value + "]"
	}
	value, err := r.askLine(command, label)
	if err == nil && value == "" {
		value = options.Value
	}
	return value, err
}

// accountNotice 在动态与静态终端呈现相同的状态和明确动作。
// 只返回动作ID，不把显示页面当作授权，更不隐式重试凭证保存。
func (r *runtime) accountNotice(command *cobra.Command, notice tui.NoticeOptions) (string, error) {
	if r.session != nil {
		return r.session.Notice(command.Context(), notice)
	}
	for _, value := range []string{notice.Title, notice.Code, notice.Message, notice.Hint} {
		if value != "" {
			if _, err := fmt.Fprintln(command.ErrOrStderr(), r.options.redactor.Text(value)); err != nil {
				return "", app.Wrap(app.IOError, "Unable to display account status.", err)
			}
		}
	}
	if len(notice.Choices) == 0 {
		_, err := r.askLine(command, "Enter to continue")
		return "", err
	}
	return r.choose(command, notice.Title, notice.Choices)
}

// accountMenu 提供账号生命周期的用户入口，不要求用户输入CLI命令或来源枚举。
// 账号切换、核验及退出仍由同一认证服务执行；浏览工作台须先返回Home再改变账号。
func (r *runtime) accountMenu(command *cobra.Command, version string) error {
	if r.options.browseScope != nil {
		_, err := r.accountNotice(command, tui.NoticeOptions{
			Title: "Account settings", Message: "This workspace is linked to the account used to open it.",
			Hint: "Return to Home to connect, switch or disconnect an account, then browse again.", Kind: output.StyleWarning,
		})
		return err
	}
	for {
		t, _, service, resolveErr := r.resolve(command, r.input(false), false)
		title := "Account"
		if resolveErr == nil {
			status := service.Status(t)
			if status.Account != "" {
				title += " · @" + status.Account
			}
		}
		choice, err := r.choose(command, title, []tui.Choice{
			{ID: "auth login", Label: "Connect account", Description: "Set up a Gitea instance and verify your identity"},
			{ID: "auth status", Label: "Connection status", Description: "Inspect and verify the current account"},
			{ID: "auth switch", Label: "Switch account", Description: "Choose a saved identity"},
			{ID: "auth refresh", Label: "Update credentials", Description: "Replace expired credentials for a saved identity"},
			{ID: "auth logout", Label: "Disconnect account", Description: "Remove the sotea connection for this identity"},
			{ID: "back", Label: "Back", Description: "Return to Home"},
		})
		if err != nil || choice == "back" {
			if err != nil && app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return nil
			}
			return err
		}
		switch choice {
		case "auth login":
			err = r.connectAccount(command, false)
		case "auth refresh":
			err = r.connectAccount(command, true)
		default:
			err = r.savedAccountAction(command, choice)
		}
		if err != nil && app.Failure(err).Code != app.Cancelled {
			notice := failureNotice(app.Failure(err))
			notice.Choices = []tui.Choice{{ID: "back", Label: "Back to account settings", Description: "Review the connection or choose another account action"}}
			if _, displayErr := r.accountNotice(command, notice); displayErr != nil && app.Failure(displayErr).Code != app.Cancelled {
				return displayErr
			}
		}
	}
}

// connectAccount 以实例、凭证、身份预览及明确保存构成可返回的连接向导。
// 保存前不写账号或密钥；配置并发变化由认证服务拒绝，退出或失败恢复原会话上下文。
func (r *runtime) connectAccount(command *cobra.Command, refresh bool) error {
	if r.options.browseScope != nil {
		return app.Wrap(app.Conflict, "Return to Home before changing the account connection.", nil)
	}
	original := *r.options
	saved := false
	defer func() {
		if !saved {
			*r.options = original
		}
	}()
	current, _, _, initialErr := r.resolve(command, r.input(false), false)
	if initialErr != nil && app.Failure(initialErr).Code != app.ContextRequired && app.Failure(initialErr).Code != app.ContextAmbiguous {
		return initialErr
	}
	address := current.Instance.URL
	expected := current.Account
	step := 0
	var t target.Target
	var snapshot config.Snapshot
	var service auth.Service
	options := auth.LoginOptions{}
	var profile auth.Profile
	for {
		switch step {
		case 0:
			value, err := r.accountInput(command, tui.InputOptions{
				Title: "Connect account", Step: "Step 1 of 4 · Instance",
				Description: "Use the complete Gitea address, including any proxy subpath.",
				Label:       "Gitea instance URL", Placeholder: "https://gitea.example.com", Value: address,
			})
			if err != nil {
				return err
			}
			address, err = target.NormalizeURL(strings.TrimSpace(value))
			if err != nil {
				if retryErr := r.setupRetry(command, err); retryErr != nil {
					return retryErr
				}
				continue
			}
			*r.options = original
			r.options.instance = address
			if address != current.Instance.URL {
				r.options.account, r.options.repo, r.options.remote = "", "", ""
				r.options.noLocal = true
				expected = ""
			}
			t, snapshot, service, err = r.resolve(command, r.input(false), true)
			if err != nil {
				if retryErr := r.setupRetry(command, err); retryErr != nil {
					return retryErr
				}
				continue
			}
			t, err = r.confirmInstance(command, t, false)
			if err != nil {
				if app.Failure(err).Code == app.Cancelled {
					continue
				}
				return err
			}
			step = 1
		case 1:
			if refresh {
				choices := []tui.Choice{}
				names := []string{}
				for name := range t.Instance.Accounts {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					choices = append(choices, tui.Choice{ID: name, Label: "@" + name, Description: "Saved credential source: " + t.Instance.Accounts[name].Source})
				}
				if len(choices) == 0 {
					return app.Wrap(app.AuthRequired, "Connect an account before updating saved credentials.", nil)
				}
				value, err := r.choose(command, "Step 2 of 4 · Choose an account to update", choices)
				if err != nil {
					if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
						step = 0
						continue
					}
					return err
				}
				expected, t.Account, step = value, value, 2
				continue
			}
			value, err := r.accountInput(command, tui.InputOptions{
				Title: "Connect account", Step: "Step 2 of 4 · Identity",
				Description: "Enter the expected Gitea username, or clear this field to detect it from the credentials.",
				Label:       "Account username (optional)", Value: expected,
			})
			if err != nil {
				if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
					step = 0
					continue
				}
				return err
			}
			expected, t.Account = strings.TrimSpace(value), strings.TrimSpace(value)
			step = 2
		case 2:
			options.Secret = ""
			choices := []tui.Choice{}
			if t.Credential.Protocol != "" || t.Instance.Accounts[t.Account].Source == "git" {
				choices = append(choices,
					tui.Choice{ID: "git", Label: "Use Git credentials", Description: "Reuse the matching credential helper with Basic authentication"},
					tui.Choice{ID: "git-token", Label: "Use a token from Git", Description: "Reuse the matching helper with token authentication"})
			}
			if os.Getenv("SOTEA_TOKEN") != "" {
				choices = append(choices, tui.Choice{ID: "environment", Label: "Use environment token", Description: "Verify SOTEA_TOKEN without copying it into sotea storage"})
			}
			choices = append(choices, tui.Choice{ID: "token", Label: "Enter access token", Description: "Store the verified token in the system credential store"})
			source, err := r.choose(command, "Step 3 of 4 · Credentials", choices)
			if err != nil {
				if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
					step = 1
					continue
				}
				return err
			}
			options = auth.LoginOptions{Source: source}
			if source == "git" || source == "git-token" {
				options.Source, options.AuthType = "git", "basic"
				if source == "git-token" {
					options.AuthType = "token"
				}
			}
			if source == "token" {
				options.Secret, err = r.accountInput(command, tui.InputOptions{
					Title: "Connect account", Step: "Step 3 of 4 · Credentials",
					Description: "Paste your Gitea access token. Input is hidden. It is saved only after you verify and confirm the account.",
					Label:       "Access token", Secret: true,
				})
				if err != nil {
					options.Secret = ""
					if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
						continue
					}
					return err
				}
				r.options.redactor.Add(options.Secret)
			}
			ctx, cancel, err := r.browseLoading(command, "Verifying account...")
			if err != nil {
				return err
			}
			err = accountSnapshotCurrent(ctx, service, snapshot)
			if err == nil {
				profile, err = service.PreviewLogin(ctx, t, options)
			}
			cancel()
			if err != nil {
				options.Secret = ""
				if retryErr := r.setupRetry(command, err); retryErr != nil {
					return retryErr
				}
				if app.Failure(err).Code == app.ConfigConflict {
					step = 0
				}
				continue
			}
			step = 3
		case 3:
			source := options.Source
			message := fmt.Sprintf("Instance  %s\nAccount   @%s\nSource    %s", t.Instance.URL, profile.Login, source)
			if profile.FullName != "" {
				message += "\nName      " + profile.FullName
			}
			action, err := r.accountNotice(command, tui.NoticeOptions{
				Title: "Step 4 of 4 · Save connection", Message: message, Kind: output.StyleSuccess,
				Hint: "The identity was verified through Gitea. Save this connection to use it across repositories. Credentials are never written to ordinary configuration.",
				Choices: []tui.Choice{
					{ID: "back", Label: "Back", Description: "Review the credential source before saving"},
					{ID: "save", Label: "Save connection", Description: "Verify again and save this exact account"},
				},
			})
			if err != nil || action != "save" {
				if err != nil && (app.Failure(err).Code != app.Cancelled || command.Context().Err() != nil) {
					return err
				}
				step = 2
				continue
			}
			options.ExpectedAccount = profile.Login
			ctx, cancel, err := r.browseLoading(command, "Saving verified connection...")
			if err != nil {
				return err
			}
			err = accountSnapshotCurrent(ctx, service, snapshot)
			var status auth.Status
			if err == nil {
				status, err = service.Login(ctx, t, snapshot, options)
			}
			cancel()
			options.Secret = ""
			if err != nil {
				return err
			}
			r.options.instance, r.options.account = status.Instance, status.Account
			saved = true
			_, err = r.accountNotice(command, tui.NoticeOptions{
				Title: "Account connected", Kind: output.StyleSuccess,
				Message: "@" + status.Account + " is connected to " + status.URL + ".",
				Hint:    "Continue to browse repositories or work in the current repository.",
				Choices: []tui.Choice{{ID: "continue", Label: "Continue", Description: "Return to the previous screen with the current account settings"}},
			})
			if err != nil && app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return nil
			}
			return err
		}
	}
}

// setupRetry 显示验证失败原因并允许返回输入步骤，取消不继续网络动作。
// 所有恢复都是用户选择，不对保存配置或远端写入自动重试。
func (r *runtime) setupRetry(command *cobra.Command, err error) error {
	notice := failureNotice(app.Failure(err))
	notice.Choices = []tui.Choice{
		{ID: "retry", Label: "Review input", Description: "Return to the previous setup step"},
		{ID: "cancel", Label: "Cancel setup", Description: "Leave setup without saving a connection"},
	}
	action, displayErr := r.accountNotice(command, notice)
	if displayErr != nil {
		return displayErr
	}
	if action != "retry" {
		return app.Wrap(app.Cancelled, "Account setup cancelled.", nil)
	}
	return nil
}

// savedAccountAction 使用稳定账号候选执行核验、切换或退出。
// 退出仅移除sotea绑定，不删除共享Git凭证或外部环境token；所有动作保留认证服务的原子性。
func (r *runtime) savedAccountAction(command *cobra.Command, action string) error {
	t, snapshot, service, err := r.resolve(command, r.input(false), true)
	if err != nil {
		return err
	}
	if action == "auth switch" {
		names := []string{}
		for name := range t.Instance.Accounts {
			if !t.Instance.LoggedOut[name] {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		choices := []tui.Choice{}
		for _, name := range names {
			choices = append(choices, tui.Choice{ID: name, Label: "@" + name, Description: "Saved credential source: " + t.Instance.Accounts[name].Source})
		}
		if len(choices) == 0 {
			return app.Wrap(app.AuthRequired, "Connect an account before switching identities.", nil)
		}
		t.Account, err = r.choose(command, "Switch account", choices)
		if err != nil {
			return err
		}
	}
	if action == "auth logout" {
		id, err := r.accountNotice(command, tui.NoticeOptions{
			Title: "Disconnect account", Kind: output.StyleWarning,
			Message: "Disconnect @" + t.Account + " from " + t.Instance.URL + "?",
			Hint:    "Shared Git credentials and externally managed environment tokens are kept.",
			Choices: []tui.Choice{
				{ID: "back", Label: "Keep connected", Description: "Return without changing the saved connection"},
				{ID: "disconnect", Label: "Disconnect", Description: "Remove this sotea connection; shared Git credentials and environment tokens remain"},
			},
		})
		if err != nil || id != "disconnect" {
			return err
		}
	}
	ctx, cancel, err := r.browseLoading(command, "Checking account connection...")
	if err != nil {
		return err
	}
	defer cancel()
	var status auth.Status
	switch action {
	case "auth switch":
		status, err = service.Switch(ctx, t, snapshot)
	case "auth logout":
		status, err = service.Logout(ctx, t, snapshot)
	default:
		status, err = service.Check(ctx, t, snapshot)
	}
	if err != nil {
		return err
	}
	if action == "auth switch" {
		r.options.account = status.Account
	} else if action == "auth logout" {
		r.options.account = ""
	}
	title := "Connection verified"
	if action == "auth logout" {
		title = "Account disconnected"
	}
	_, err = r.accountNotice(command, tui.NoticeOptions{
		Title: title, Kind: output.StyleSuccess,
		Message: fmt.Sprintf("Instance  %s\nAccount   @%s\nSource    %s", status.URL, status.Account, status.Source),
		Choices: []tui.Choice{{ID: "continue", Label: "Continue", Description: "Return to the previous screen with the current account settings"}},
	})
	return err
}
