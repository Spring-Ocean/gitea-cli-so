package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/Spring-Ocean/gitea-cli-so/internal/update"
	"github.com/spf13/cobra"
)

// addUpdateCommands 注册独立于仓库和认证的本机更新入口。
// check 显式联网；install 采用默认拒绝确认，非交互必须明确传入 --yes。
func addUpdateCommands(root *cobra.Command, r *runtime, version string) {
	command := &cobra.Command{
		Use:   "update",
		Short: "Check GitHub Releases and install verified sotea updates",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return command.Help()
		},
	}
	var prerelease bool
	command.PersistentFlags().BoolVar(&prerelease, "prerelease", false, "Include prereleases; RC installations include them by default")
	command.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Check GitHub Releases now, bypassing the automatic five-hour interval",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			service, err := update.New()
			if err != nil {
				return err
			}
			checked, err := service.Check(command.Context(), version, prerelease, true)
			if err != nil {
				return err
			}
			return r.printUpdateResult(command, checked)
		},
	}, &cobra.Command{
		Use:   "install",
		Short: "Download, verify and install a newer sotea release after confirmation",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return r.installUpdate(command, version, prerelease)
		},
	})
	root.AddCommand(command)
}

// printUpdateResult 共用现有业务 envelope 输出检查或安装结果。
// 数据无需 Gitea 上下文，JSON 保持可解析，文本与菜单按既有格式化组件展示。
func (r *runtime) printUpdateResult(command *cobra.Command, data any) error {
	return r.printResult(command, app.NewResult(app.Context{}, []app.Item{{
		Target: "sotea update",
		Status: app.Success,
		Data:   data,
	}}, nil))
}

// installUpdate 明确检查、预览确认、执行下载校验和安装。
// 菜单用可取消加载提示；安装成功不自动重启或重新执行任何仓库操作。
func (r *runtime) installUpdate(command *cobra.Command, version string, prerelease bool) error {
	r.updateMu.Lock()
	alreadyInstalled := r.updateInstalled
	r.updateMu.Unlock()
	if alreadyInstalled {
		return app.Wrap(app.PreconditionChanged, "An update is already installed. Restart sotea and active MCP processes before installing another update.", nil)
	}
	if !r.options.yes && !r.interactive(command) {
		return app.Wrap(app.InvalidArgument, "Installing an update requires confirmation. Use --yes in non-interactive mode, or run sotea update check first.", nil)
	}
	service, err := update.New()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(command.Context())
	defer cancel()
	if r.session != nil {
		if err := r.session.Busy(ctx, "Checking GitHub Releases...", cancel); err != nil {
			return err
		}
	}
	checked, err := service.Check(ctx, version, prerelease, true)
	if err != nil {
		return err
	}
	if !checked.Available {
		return r.printUpdateResult(command, checked)
	}
	if !checked.Installable {
		return app.Wrap(app.NotFound, checked.Message+" "+checked.ReleaseURL, nil)
	}
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "Update sotea: %s → %s\nSource: %s\nThe current executable will be replaced after checksum and version verification. A backup will be retained. Restart sotea and active MCP processes afterwards.\n", version, checked.Latest, checked.ReleaseURL); err != nil {
		return app.Wrap(app.IOError, "Unable to display the update preview.", err)
	}
	if err := r.confirm(command, "Install this sotea update?", r.options.yes); err != nil {
		return err
	}
	if r.session != nil {
		if err := r.session.Busy(ctx, "Downloading, verifying and installing sotea...", cancel); err != nil {
			return err
		}
	}
	installed, err := service.Install(ctx, checked)
	if err != nil && installed.Version == "" {
		return err
	}
	r.updateMu.Lock()
	r.updateInstalled = true
	r.updateMu.Unlock()
	if r.session != nil {
		r.session.UpdateAvailable("")
	}
	if err != nil {
		return r.printResult(command, app.NewResult(app.Context{}, []app.Item{{
			Target: "sotea update",
			Status: app.Partial,
			Data:   installed,
		}}, err))
	}
	return r.printUpdateResult(command, installed)
}

// startHomeUpdateCheck 在每次进入 Home 时尝试启动非阻塞检查。
// 进程内只允许一个工作任务；跨进程缓存负责五小时冷却，CI 和显式禁用不访问网络。
func (r *runtime) startHomeUpdateCheck(ctx context.Context, version string) {
	if os.Getenv("SOTEA_UPDATE_CHECK") == "never" || os.Getenv("CI") != "" {
		return
	}
	r.updateMu.Lock()
	if r.updateChecking || r.updateInstalled {
		r.updateMu.Unlock()
		return
	}
	r.updateChecking = true
	r.updateWorkers.Add(1)
	r.updateMu.Unlock()
	session := r.session
	go func() {
		defer r.updateWorkers.Done()
		service, err := update.New()
		var checked update.Result
		if err == nil {
			checked, err = service.Check(ctx, version, false, false)
		}
		r.updateMu.Lock()
		r.updateChecking = false
		installed := r.updateInstalled
		if err == nil && !installed {
			r.updateResult = checked
		}
		if err == nil && session != nil && !installed && ctx.Err() == nil {
			latest := ""
			if checked.Available {
				latest = checked.Latest
			}
			session.UpdateAvailable(latest)
		}
		r.updateMu.Unlock()
	}()
}

// homeUpdateChoice 对静态 Home 应用已完成的检查结果，高亮可更新入口。
// VT 页面的新结果通过消息即时刷新；未知或失败不伪装成已是最新版。
func (r *runtime) homeUpdateChoice(choice tui.Choice) tui.Choice {
	r.updateMu.Lock()
	defer r.updateMu.Unlock()
	if choice.ID == "update" && r.updateResult.Available && !r.updateInstalled {
		choice.Label = "Update available · " + r.updateResult.Latest
		choice.Description = "Review and install the verified GitHub release"
		choice.Role = output.StyleWarning
	}
	return choice
}

// updateMenu 提供与仓库状态无关的检查及安装向导。
// 普通 Esc 返回 Home；检查与安装均走共享服务，不调用子 CLI。
func (r *runtime) updateMenu(command *cobra.Command, version string) error {
	for {
		action, err := r.choose(command, "sotea update · GitHub Releases", []tui.Choice{
			{ID: "check", Label: "Check for updates", Description: "Check the official GitHub Releases now"},
			{ID: "install", Label: "Install update", Description: "Preview, download, verify and replace sotea; keep a backup"},
			{ID: "back", Label: "Back to Home", Description: "Return to the workspace"},
		})
		if err != nil || action == "back" {
			if err != nil && app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return menuBack
			}
			return err
		}
		if action == "install" {
			err = r.installUpdate(command, version, false)
		} else {
			service, createErr := update.New()
			err = createErr
			if err == nil {
				ctx, cancel := context.WithCancel(command.Context())
				if r.session != nil {
					err = r.session.Busy(ctx, "Checking GitHub Releases...", cancel)
				}
				var checked update.Result
				if err == nil {
					checked, err = service.Check(ctx, version, false, true)
				}
				cancel()
				if err == nil {
					r.updateMu.Lock()
					r.updateResult = checked
					r.updateMu.Unlock()
					if r.session != nil {
						latest := ""
						if checked.Available {
							latest = checked.Latest
						}
						r.session.UpdateAvailable(latest)
					}
					err = r.printUpdateResult(command, checked)
				}
			}
		}
		if err != nil && app.Failure(err).Code != app.Cancelled {
			if r.session == nil {
				return err
			}
			r.session.Drain()
			if err := r.showMenuFailure(command, app.Failure(err)); err != nil {
				return err
			}
		} else if r.session != nil {
			body := r.session.Drain()
			if strings.TrimSpace(body) != "" {
				if err := r.session.Display(command.Context(), "sotea update", body); err != nil {
					return err
				}
			}
		}
	}
}
