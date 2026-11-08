package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/catalog"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/pullrequest"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/release"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/repository"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/session"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
)

// readInput 仅承接各工具经封闭 Schema 验证的字段。
// 公共上下文与筛选不被直接转成远端任意 query。
type readInput struct {
	Instance             string   `json:"instance"`
	Account              string   `json:"account"`
	Repo                 string   `json:"repo"`
	Workdir              string   `json:"workdir"`
	Refresh              bool     `json:"refresh"`
	Check                bool     `json:"check"`
	Index                int64    `json:"index"`
	ExpectedRepositoryID int64    `json:"expected_repository_id"`
	Organization         string   `json:"organization"`
	OrganizationID       int64    `json:"organization_id"`
	Page                 int      `json:"page"`
	Limit                int      `json:"limit"`
	State                string   `json:"state"`
	Search               string   `json:"q"`
	Labels               []string `json:"labels"`
	Assignee             string   `json:"assignee"`
	Milestone            string   `json:"milestone"`
	Base                 string   `json:"base"`
	Author               string   `json:"author"`
	Ref                  string   `json:"ref"`
	Tag                  string   `json:"tag"`
}

// registerReads 注册已确认的全部远端读取，文件根仅控制固定 Git 工具。
// 描述使用英文说明实际范围，不把账户目录当全站可访问仓库。
func (h *Host) registerReads() {
	tools := []struct {
		name        string
		description string
	}{
		{"context_get", "Resolve explicit instance, identity, repository and authorized local context without changing defaults."},
		{"instance_info", "Inspect Gitea version, cache and separate API-definition and runtime-test evidence."},
		{"auth_status", "Inspect the selected identity without exposing credentials. Use check to verify with GET /user."},
		{"account_repo_list", "List one page of repositories returned by /user/repos for the verified account."},
		{"account_org_list", "List one page of organizations for the verified account."},
		{"org_repo_list", "Verify an organization ID and list one page of its repositories without switching context."},
		{"repo_view", "Read repository details and permissions; optionally verify the expected stable repository ID."},
		{"repo_assignee_list", "List one page of repository assignee candidates for explicit issue or PR changes."},
		{"branch_list", "List one page of repository branches."},
		{"tag_list", "List one page of Git tags; tags are distinct from releases."},
		{"commit_list", "List one page of repository commits."},
		{"commit_status", "Read aggregate status for an explicit ref or a verified matching local HEAD."},
		{"label_list", "List one page of repository labels and stable IDs."},
		{"milestone_list", "List one page of repository milestones and stable IDs."},
		{"issue_list", "List a bounded issue page with filters; pull requests are excluded."},
		{"issue_view", "Read one verified issue; a pull request number is rejected."},
		{"issue_label_list", "Read labels attached to one issue, distinct from repository label candidates."},
		{"issue_comment_list", "Read one page of ordinary issue comments."},
		{"pr_list", "Read a bounded pull-request page with state, text, base and author filters."},
		{"pr_view", "Read one verified pull request and its remote head."},
		{"pr_files", "Read one page of changed files for a verified pull request."},
		{"pr_commits", "Read one page of commits for a verified pull request."},
		{"pr_comment_list", "Read one page of ordinary PR discussion, separate from reviews."},
		{"pr_review_list", "Read one page of PR reviews including pending review identities."},
		{"release_list", "Read a bounded release page; search is evaluated using shared version-compatible filters."},
		{"release_view", "Read a release by its exact tag, including numeric, slash and Unicode tags."},
		{"release_asset_list", "Read one page of release attachment metadata without downloading arbitrary URLs."},
	}
	if h.scope != nil || h.options.FollowWorkspace {
		tools = append(tools, struct {
			name        string
			description string
		}{"git_context", "Read fixed Git repository, branch, remote-name and status metadata inside the authorized root. No shell or Git writes."})
	}
	for _, tool := range tools {
		h.register(tool.name, tool.description, readSchema(tool.name), false, false, func(view *Host, ctx context.Context, arguments map[string]json.RawMessage) response {
			return view.read(ctx, tool.name, arguments)
		})
	}
}

// resolveInput 固定单次输入并限制本地目录，不读取宿主启动 cwd。
// 跟随模式的根由工具入口视图提供；缺少根时只解析远端，本地 Git 必须留在该次授权范围。
func (h *Host) resolveInput(ctx context.Context, input readInput, requireRepo bool) (session.State, error) {
	options := h.options.Defaults
	options.RequireRepo = requireRepo
	if input.Instance != "" {
		options.Instance = input.Instance
	}
	if input.Account != "" {
		options.Account = input.Account
	}
	if input.Repo != "" {
		options.Repo = input.Repo
	}
	options.NoLocal = h.scope == nil
	if input.Workdir != "" && h.scope == nil {
		return session.State{}, h.localAccessError()
	}
	if h.scope != nil {
		directory := input.Workdir
		if directory == "" {
			directory = options.Workdir
		}
		actual, err := h.scope.Directory(directory)
		if err != nil {
			return session.State{}, err
		}
		options.Workdir = actual
		if options.Instance != "" && options.Repo != "" && input.Workdir == "" {
			options.NoLocal = true
		}
	}
	state, err := h.environment.Resolve(ctx, options)
	if err == nil && state.Target.Local != nil {
		_, err = h.scope.Directory(state.Target.Local.Root)
	}
	return state, err
}

// read 将严格工具输入映射到共享业务读取，不经过 CLI 或终端渲染。
// 分页默认一页；远端过滤与旧版完整有界扫描完全由应用服务处理。
func (h *Host) read(ctx context.Context, name string, arguments map[string]json.RawMessage) response {
	input := readInput{
		Page:  1,
		Limit: 30,
		State: "open",
	}
	if err := decode(arguments, &input); err != nil {
		return failed(err)
	}
	if name == "git_context" {
		return h.gitContext(ctx, input.Workdir)
	}
	if (name == "context_get" || name == "commit_status") && h.scope != nil && input.Workdir == "" {
		input.Workdir = "."
	}
	requireRepo := true
	switch name {
	case "context_get", "instance_info", "auth_status", "account_repo_list", "account_org_list", "org_repo_list":
		requireRepo = false
	}
	state, err := h.resolveInput(ctx, input, requireRepo)
	if err != nil {
		return failed(err)
	}
	switch name {
	case "context_get":
		visible := state.Target
		if h.scope == nil {
			visible.Workdir = ""
		}
		if h.options.FollowWorkspace {
			visible.Sources["local_access"] = "client workspace"
			if h.workspaceError != nil {
				visible.Sources["local_access"] = app.Failure(h.workspaceError).Message
			}
		}
		return dataResponse(state, visible, nil)
	case "instance_info":
		value, err := h.environment.Version(ctx, state, input.Refresh)
		return dataResponse(state, value, err)
	case "auth_status":
		value := state.Auth.Status(state.Target)
		if input.Check {
			value, err = state.Auth.Check(ctx, state.Target, state.Snapshot)
		}
		return dataResponse(state, value, err)
	case "account_repo_list", "account_org_list", "org_repo_list":
		kind := catalog.Repositories
		if name == "account_org_list" {
			kind = catalog.Organizations
		}
		if name == "org_repo_list" {
			kind = catalog.OrganizationRepositories
		}
		value, resolved, err := catalog.List(ctx, state.Auth, state.Target, catalog.Query{
			Kind:           kind,
			Organization:   input.Organization,
			OrganizationID: input.OrganizationID,
			Page:           input.Page,
			Limit:          input.Limit,
		})
		state.Target = resolved
		return dataResponse(state, value, err)
	case "repo_view":
		if input.ExpectedRepositoryID > 0 {
			_, resolved, err := catalog.GetRepository(ctx, state.Auth, state.Target, input.ExpectedRepositoryID, state.Target.Repository)
			if err != nil {
				return failed(err)
			}
			state.Target = resolved
		}
	}
	operation := strings.ReplaceAll(name, "_", ".")
	switch name {
	case "repo_view", "branch_list", "tag_list", "commit_list", "commit_status", "label_list", "milestone_list", "repo_assignee_list":
		if name == "repo_assignee_list" {
			operation = "assignee.list"
		}
		ref := input.Ref
		if name == "commit_status" && ref == "" && state.Target.LocalMatches && state.Target.Local != nil {
			ref = state.Target.Local.Head
		}
		result, err := repository.Read(ctx, state.Auth, state.Target, repository.Query{
			Operation: operation,
			Page:      input.Page,
			Limit:     input.Limit,
			Ref:       ref,
		})
		return businessResponse(state, result, err)
	}
	version, err := h.environment.Version(ctx, state, false)
	if err != nil && ctx.Err() != nil {
		return failed(err)
	}
	shared := write.Service{
		Auth:     state.Auth,
		Target:   state.Target,
		Version:  version,
		Revision: state.Snapshot.RevisionKey(),
		Locks:    h.environment.Locks,
	}
	var result app.Result
	switch {
	case strings.HasPrefix(name, "issue_"):
		service := issue.Service{
			Auth:     state.Auth,
			Target:   state.Target,
			Version:  version,
			Revision: shared.Revision,
			Locks:    shared.Locks,
		}
		switch name {
		case "issue_list":
			result, err = service.List(ctx, issue.Filter{
				State:     input.State,
				Search:    input.Search,
				Labels:    input.Labels,
				Assignee:  input.Assignee,
				Milestone: input.Milestone,
				Page:      input.Page,
				Limit:     input.Limit,
			})
		case "issue_view":
			result, err = service.View(ctx, input.Index)
		default:
			kind := "comments"
			if name == "issue_label_list" {
				kind = "labels"
			}
			result, err = service.Related(ctx, input.Index, kind, input.Page, input.Limit, false)
		}
	case strings.HasPrefix(name, "pr_"):
		service := pullrequest.Service{Service: shared}
		switch name {
		case "pr_list":
			result, err = service.List(ctx, pullrequest.Filter{
				State:  input.State,
				Search: input.Search,
				Base:   input.Base,
				Author: input.Author,
				Page:   input.Page,
				Limit:  input.Limit,
			})
		case "pr_view":
			result, err = service.View(ctx, input.Index)
		default:
			kind := strings.TrimPrefix(name, "pr_")
			if name == "pr_comment_list" {
				kind = "comments"
			}
			if name == "pr_review_list" {
				kind = "reviews"
			}
			result, err = service.Related(ctx, input.Index, kind, input.Page, input.Limit, false)
		}
	case strings.HasPrefix(name, "release_"):
		service := release.Service{Service: shared}
		switch name {
		case "release_list":
			result, err = service.List(ctx, release.Filter{
				Search: input.Search,
				Page:   input.Page,
				Limit:  input.Limit,
			})
		case "release_view":
			result, err = service.View(ctx, input.Tag)
		case "release_asset_list":
			result, err = service.Assets(ctx, input.Tag, input.Page, input.Limit, false)
		}
	}
	return businessResponse(state, result, err)
}

// businessResponse 补齐应用服务尚未构造结果时的共同错误。
// 已有 partial／unknown 不被改成空失败，顶层错误仍留在同一 envelope。
func businessResponse(state session.State, result app.Result, err error) response {
	if result.SchemaVersion == 0 {
		result = app.NewResult(state.Target.Context(), nil, err)
	} else if err != nil {
		result = app.NewResult(result.Context, result.Items, err)
	}
	return response{Result: result}
}

// gitContext 只执行固定本地查询，不传递协议 stdin 或任意参数。
// 仓库根在授权范围内，fsmonitor／pager／外部 diff 被禁用，远端只返回名称。
func (h *Host) gitContext(ctx context.Context, directory string) response {
	if h.scope == nil {
		return failed(h.localAccessError())
	}
	actual, err := h.scope.Directory(directory)
	if err != nil {
		return failed(err)
	}
	runner := h.environment.Authentication.Git
	runner.Environment = append(append([]string{}, runner.Environment...),
		"GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "GIT_EXTERNAL_DIFF=", "GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=core.untrackedCache", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_2=", "GIT_CONFIG_PARAMETERS=",
	)
	local, err := runner.Discover(ctx, actual)
	if err != nil {
		return failed(err)
	}
	if _, err := h.scope.Directory(local.Root); err != nil {
		return failed(err)
	}
	names := []string{}
	for _, remote := range local.Remotes {
		names = append(names, remote.Name)
	}
	status := ""
	if !local.Bare {
		captured, err := runner.Capture(ctx, actual, []string{"--no-pager", "-c", "core.fsmonitor=false", "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=all"}, nil)
		if err != nil {
			return failed(err)
		}
		status = string(captured.Stdout)
	}
	state := session.State{Target: target.Target{
		Repository: "",
		Workdir:    actual,
	}}
	return dataResponse(state, map[string]any{
		"root": local.Root, "branch": local.Branch, "head": local.Head,
		"bare": local.Bare, "remotes": names, "status_porcelain_v1_z": status,
	}, nil)
}
