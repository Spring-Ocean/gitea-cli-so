package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/catalog"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

// browseScope 固定一次账户浏览的实例、身份与配置修订，不持有密钥或 HTTP 客户端。
// 配置被另一进程修改后中止浏览，要求从主菜单重新读取，以免跨身份继续旧选择。
type browseScope struct {
	instance string
	account  string
	revision string
}

// check 在读取凭证之前核对浏览所绑定的目标与配置。
// 空账号仍由后续身份请求核对；任何明确身份、根地址或配置变化均不能静默切换。
func (scope browseScope) check(t target.Target, snapshot config.Snapshot) error {
	if t.Instance.URL != scope.instance || snapshot.RevisionKey() != scope.revision ||
		(t.Account != "" && !strings.EqualFold(t.Account, scope.account)) {
		return app.Wrap(app.Conflict, "The account context changed. Return to Home and open Browse account again.", nil)
	}
	return nil
}

// browseAccount 从当前已登记实例打开只读账户目录，不要求本地存在目标仓库。
// 未登录通过 Account 完成显式认证；浏览不保存默认配置、不克隆仓库或修改 Git remote。
func (r *runtime) browseAccount(command *cobra.Command, version string) error {
	var scope browseScope
	for {
		var err error
		scope, err = r.browseIdentity(command)
		if err == nil {
			break
		}
		if r.session == nil || app.Failure(err).Code != app.AuthRequired {
			return err
		}
		action, noticeErr := r.session.Notice(command.Context(), tui.NoticeOptions{
			Title:   "Connect an account to browse",
			Message: "Connect your Gitea identity so sotea can browse repositories beyond the current checkout.",
			Hint:    "Choose an instance and credential source, verify your identity, then save the connection. Your repositories will open when setup is complete.",
			Kind:    output.StyleWarning,
			Choices: []tui.Choice{
				{ID: "connect", Label: "Connect account", Description: "Continue with guided setup"},
				{ID: "back", Label: "Back", Description: "Return without changing the account"},
			},
		})
		if noticeErr != nil {
			return noticeErr
		}
		if action != "connect" {
			return nil
		}
		if err := r.connectAccount(command, false); err != nil {
			return err
		}
	}
	for {
		r.browseHeader(scope)
		choice, err := r.choose(command, "Browse account · @"+scope.account, []tui.Choice{{
			ID:    "repositories",
			Label: "Account repositories · Repositories returned for this account",
		}, {
			ID:    "organizations",
			Label: "Organizations · Browse your organization memberships",
		}, {
			ID:          "@back",
			Label:       "Back to Home",
			Description: "Return to your original repository context",
		}})
		if err != nil {
			if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return nil
			}
			return err
		}
		switch choice {
		case "@back":
			return nil
		case "repositories":
			err = r.browsePages(command, version, scope, catalog.Repositories, "", 0)
		case "organizations":
			err = r.browsePages(command, version, scope, catalog.Organizations, "", 0)
		}
		if err != nil && app.Failure(err).Code != app.Cancelled {
			return err
		}
	}
}

// browseIdentity 验证目录浏览的完整实例与账号绑定，不扩大临时Git凭证的作用范围。
// 失败由上层提供连接向导；成功只返回非敏感作用域，每次目录查询仍重新验证身份。
func (r *runtime) browseIdentity(command *cobra.Command) (browseScope, error) {
	t, snapshot, authentication, err := r.resolve(command, r.input(false), true)
	if err != nil {
		return browseScope{}, err
	}
	if !t.Registered {
		return browseScope{}, app.Wrap(app.AuthRequired, "Connect this instance before browsing account repositories.", nil)
	}
	ctx, cancel, err := r.browseLoading(command, "Verifying account...")
	if err != nil {
		return browseScope{}, err
	}
	defer cancel()
	client, verified, err := authentication.Open(ctx, t, false)
	if err != nil {
		return browseScope{}, err
	}
	client.Close()
	if _, saved := t.Instance.Accounts[verified.Account]; !saved && os.Getenv("SOTEA_TOKEN") == "" {
		return browseScope{}, app.Wrap(app.AuthRequired, "This verified identity has not been connected to sotea yet.", nil)
	}
	return browseScope{
		instance: t.Instance.URL,
		account:  verified.Account,
		revision: snapshot.RevisionKey(),
	}, nil
}

// browseLoading 为目录网络动作建立独立取消上下文，渲染保持可响应。
// 调用者必须 cancel；静态终端沿用父上下文，无额外后台读取器。
func (r *runtime) browseLoading(command *cobra.Command, title string) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(command.Context())
	if r.session != nil {
		if err := r.session.Busy(command.Context(), title, cancel); err != nil {
			cancel()
			return nil, nil, err
		}
	}
	return ctx, cancel, nil
}

// browseTarget 每次目录查询重读配置，并在发送凭证前绑定原浏览身份。
// 不将已取得的客户端跨操作缓存，HTTP 服务仍重新验证真实 API 账号。
func (r *runtime) browseTarget(command *cobra.Command, ctx context.Context, scope browseScope) (target.Target, auth.Service, error) {
	query := &cobra.Command{}
	query.SetContext(ctx)
	t, snapshot, authentication, err := r.resolve(query, r.input(false), false)
	if err != nil {
		return t, authentication, err
	}
	if err := scope.check(t, snapshot); err != nil {
		return t, authentication, err
	}
	t.Account = scope.account
	return t, authentication, nil
}

// browseHeader 显示浏览的实例和账号，不把刚离开的仓库当成当前目录范围。
// 内容只进入已脱敏的会话头；静态降级由各页标题展示同一账号。
func (r *runtime) browseHeader(scope browseScope) {
	if r.session != nil {
		r.session.Header(scope.instance + " · @" + scope.account + " · Browse account")
	}
}

// browsePages 显式按页浏览仓库或组织，筛选仅作用于当前已读取页。
// 进入和返回保留页码、筛选及稳定焦点；不后台扫描全部仓库，空页和获取失败不冒充完整结果。
func (r *runtime) browsePages(command *cobra.Command, version string, scope browseScope, kind catalog.Kind, organization string, organizationID int64) error {
	pageNumber, filter := 1, ""
	for {
		r.browseHeader(scope)
		ctx, cancel, err := r.browseLoading(command, "Loading account directory...")
		if err != nil {
			return err
		}
		t, authentication, err := r.browseTarget(command, ctx, scope)
		var page catalog.Page
		if err == nil {
			page, _, err = catalog.List(ctx, authentication, t, catalog.Query{
				Kind:           kind,
				Organization:   organization,
				OrganizationID: organizationID,
				Page:           pageNumber,
				Limit:          30,
			})
		}
		cancel()
		if err != nil {
			return err
		}
		choices := []tui.Choice{}
		items := map[string]catalog.Item{}
		for _, item := range page.Items {
			label := catalogLabel(item, kind)
			if filter != "" && !strings.Contains(strings.ToLower(label), strings.ToLower(filter)) {
				continue
			}
			id := strconv.FormatInt(item.ID, 10)
			items[id] = item
			choices = append(choices, tui.Choice{
				ID:    id,
				Label: label,
			})
		}
		if page.HasMore {
			choices = append(choices, tui.Choice{
				ID:          "@next",
				Label:       "Next page",
				Description: "Load the next page from Gitea",
			})
		}
		if pageNumber > 1 {
			choices = append(choices, tui.Choice{
				ID:          "@previous",
				Label:       "Previous page",
				Description: "Load the previous page from Gitea",
			})
		}
		choices = append(choices, tui.Choice{
			ID:    "@filter",
			Label: "Filter this page · Empty clears the filter",
		}, tui.Choice{
			ID:          "@refresh",
			Label:       "Refresh this page",
			Description: "Reload this page while keeping the current filter",
		}, tui.Choice{
			ID:          "@back",
			Label:       "Back",
			Description: "Return to the parent directory menu",
		})
		title := fmt.Sprintf("%s · Page %d · %d shown · Filter: %s", catalogTitle(kind, organization), pageNumber, len(items), filter)
		if len(items) == 0 {
			title += " · No matching items on this page"
		}
		id, err := r.choose(command, title, choices)
		if err != nil {
			if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
				return nil
			}
			return err
		}
		switch id {
		case "@back":
			return nil
		case "@next":
			pageNumber++
		case "@previous":
			pageNumber--
		case "@refresh":
			continue
		case "@filter":
			value, err := r.askLine(command, "Filter this page by name or description (empty clears)")
			if err == nil {
				filter = strings.TrimSpace(value)
			} else if app.Failure(err).Code != app.Cancelled {
				return err
			}
		default:
			item, exists := items[id]
			if !exists {
				return app.Wrap(app.Conflict, "The selected directory item is no longer available. Refresh the page.", nil)
			}
			if kind == catalog.Organizations {
				err = r.browseOrganization(command, version, scope, item)
			} else {
				err = r.browseRepository(command, version, scope, item)
			}
			if err != nil && app.Failure(err).Code != app.Cancelled {
				return err
			}
		}
	}
}

// catalogLabel 生成资源摘要，不用位置代替稳定 ID，也不推断未声明的权限。
// 组织显示 API 名称，仓库显示完整路径和可见性；终端边界继续清理远端控制字符。
func catalogLabel(item catalog.Item, kind catalog.Kind) string {
	name := item.FullName
	if kind == catalog.Organizations {
		name = item.Name
		if item.FullName != "" && item.FullName != item.Name {
			name += " · " + item.FullName
		}
	} else {
		visibility := "Public"
		if item.Private {
			visibility = "Private"
		}
		name += " · " + visibility
		if item.Archived {
			name += " · Archived"
		}
	}
	if item.Description != "" {
		name += " · " + item.Description
	}
	return name
}

// catalogTitle 为各目录层提供范围明确的英文标题。
// 不把用户仓库接口说成管理员视角的整个实例仓库清单。
func catalogTitle(kind catalog.Kind, organization string) string {
	switch kind {
	case catalog.Organizations:
		return "Organizations"
	case catalog.OrganizationRepositories:
		return "Organization repositories · " + organization
	default:
		return "Account repositories"
	}
}

// browseOrganization 进入前重新核对稳定组织 ID，避免名称被重建后选中另一组织。
// 验证和列表都沿用原账号范围，失败不继续查询该组织仓库。
func (r *runtime) browseOrganization(command *cobra.Command, version string, scope browseScope, item catalog.Item) error {
	ctx, cancel, err := r.browseLoading(command, "Checking organization...")
	if err != nil {
		return err
	}
	t, authentication, err := r.browseTarget(command, ctx, scope)
	if err == nil {
		_, _, err = catalog.GetOrganization(ctx, authentication, t, item.ID, item.Name)
	}
	cancel()
	if err != nil {
		return err
	}
	return r.browsePages(command, version, scope, catalog.OrganizationRepositories, item.Name, item.ID)
}

// browseRepository 重新核对仓库 ID 与完整名称后进入可返回的远端工作台。
// 仅覆盖本次会话的上下文；所有出口恢复原值，不保存默认项，不允许操作未克隆目标的本地 Git。
func (r *runtime) browseRepository(command *cobra.Command, version string, scope browseScope, item catalog.Item) error {
	ctx, cancel, err := r.browseLoading(command, "Checking selected repository...")
	if err != nil {
		return err
	}
	t, authentication, err := r.browseTarget(command, ctx, scope)
	if err == nil {
		_, t, err = catalog.GetRepository(ctx, authentication, t, item.ID, item.FullName)
	}
	cancel()
	if err != nil {
		return err
	}
	original := *r.options
	defer func() {
		*r.options = original
	}()
	r.options.instance = t.Name
	r.options.account = scope.account
	r.options.repo = t.Repository
	r.options.remote = ""
	r.options.noLocal = true
	r.options.browseScope = &scope
	r.options.lastResult = nil
	return r.menuWorkspace(command, version, true)
}
