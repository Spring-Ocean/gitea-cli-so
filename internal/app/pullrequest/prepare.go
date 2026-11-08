package pullrequest

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Request 保存一个完整 PR 意图；只有 close／reopen 可以接受多个编号。
// Changes 保留未提供与明确清空，ReviewID 非零表示提交已存在的待提交审查。
type Request struct {
	Operation    string        `json:"operation"`
	Numbers      []int64       `json:"numbers,omitempty"`
	Changes      issue.Changes `json:"changes"`
	LabelRefs    []string      `json:"label_refs,omitempty"`
	MilestoneRef string        `json:"milestone_ref,omitempty"`
	Base         string        `json:"base,omitempty"`
	Head         string        `json:"head,omitempty"`
	Users        []string      `json:"reviewers,omitempty"`
	Teams        []string      `json:"team_reviewers,omitempty"`
	Event        string        `json:"event,omitempty"`
	Commit       string        `json:"commit,omitempty"`
	ReviewID     int64         `json:"review_id,omitempty"`
	Method       string        `json:"method,omitempty"`
	DeleteBranch bool          `json:"delete_branch"`
}

type prepared struct {
	number int64
	before PullRequest
	err    error
}

// Preview 是固定请求、源目标分支和操作步骤的无敏感展示副本。
// 修改该副本不会改变计划；失败目标也显示，不在确认后偷偷补选目标。
type Preview struct {
	Context     app.Context `json:"context"`
	Request     Request     `json:"request"`
	Targets     []app.Item  `json:"targets"`
	Steps       []string    `json:"steps"`
	Source      Branch      `json:"source,omitempty"`
	Destination Branch      `json:"destination,omitempty"`
}

// Plan 保存一次准备后的输入、远端快照和配置修订。
// 入口确认后方可 Commit，计划只消费一次，不承诺跨进程幂等。
type Plan struct {
	request     Request
	context     app.Context
	revision    string
	targets     []prepared
	source      Branch
	destination Branch
	steps       []string
	consumed    atomic.Bool
}

// Preview 返回深拷贝的明确请求与步骤，供 CLI、菜单及 MCP 展示。
// 任何内部快照或错误 cause 均不外泄为可修改的计划引用。
func (plan *Plan) Preview() (Preview, error) {
	value := Preview{
		Context:     plan.context,
		Request:     plan.request,
		Steps:       plan.steps,
		Source:      plan.source,
		Destination: plan.destination,
		Targets:     []app.Item{},
	}
	for _, target := range plan.targets {
		status := app.Success
		if target.err != nil {
			status = app.Failed
		}
		value.Targets = append(value.Targets, app.Item{
			Target: strconv.FormatInt(target.number, 10),
			Status: status,
			Data:   target.before,
			Error:  app.Failure(target.err),
		})
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return Preview{}, app.Wrap(app.IOError, "Unable to encode the PR preview.", err)
	}
	var copy Preview
	if err := json.Unmarshal(encoded, &copy); err != nil {
		return Preview{}, app.Wrap(app.IOError, "Unable to copy the PR preview.", err)
	}
	return copy, nil
}

// Prepare 校验意图、身份、共同引用与远端状态，只做只读请求。
// 创建解析已推送分支，合并检查仓库方法，旧版补充步骤明确进入预览。
func (s Service) Prepare(ctx context.Context, input Request) (*Plan, error) {
	if !s.Version.ExactMatch {
		return nil, app.Wrap(app.CompatibilityError, "PR writes require an exact researched server version.", nil)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, app.Wrap(app.InvalidArgument, "Unable to isolate PR input.", err)
	}
	var request Request
	if err := json.Unmarshal(encoded, &request); err != nil {
		return nil, err
	}
	if err := validate(&request); err != nil {
		return nil, err
	}
	connection, err := s.Open(ctx, false)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	request.Changes, err = issue.ResolveReferences(ctx, connection.Client, connection.Base, request.Changes, request.LabelRefs, request.MilestoneRef)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		request:  request,
		context:  connection.Target.Context(),
		revision: s.Revision,
		targets:  []prepared{},
		steps:    []string{request.Operation},
	}
	if request.Operation == "pr.create" {
		plan.source, plan.destination, err = creationBranches(ctx, connection, request)
		if err != nil {
			return nil, err
		}
		plan.targets = append(plan.targets, prepared{})
		if (len(request.Users) > 0 || len(request.Teams) > 0) && !s.Version.CreateReviewers() {
			plan.steps = append(plan.steps, "request_reviewers_after_creation")
		}
		return plan, nil
	}
	if request.Changes.Milestone != nil && *request.Changes.Milestone == 0 {
		plan.steps = append(plan.steps, "clear_milestone_via_shared_issue_resource")
	}
	for _, number := range request.Numbers {
		value, err := get(ctx, connection, number)
		if write.Systemic(err) {
			return nil, err
		}
		if err == nil && request.Operation == "pr.merge" {
			if err := checkMerge(ctx, connection, value, request.Method); err != nil {
				return nil, err
			}
		}
		if err == nil && request.Operation == "pr.review.create" {
			if request.ReviewID > 0 {
				var review Review
				path := connection.Base + "/pulls/" + strconv.FormatInt(number, 10) + "/reviews/" + strconv.FormatInt(request.ReviewID, 10)
				if err := write.Read(ctx, connection.Client, path, &review); err != nil {
					return nil, err
				}
				if review.State != "PENDING" || !strings.EqualFold(review.User.Login, connection.Target.Account) {
					return nil, app.Wrap(app.Conflict, "Only the selected account's pending review can be submitted.", nil)
				}
				if request.Commit != "" && request.Commit != review.CommitID {
					return nil, app.Wrap(app.InvalidArgument, "The explicit commit does not match the pending review.", nil)
				}
				if review.CommitID != value.Head.SHA {
					return nil, app.Wrap(app.PreconditionChanged, "The pending review targets an older PR head. Inspect the review before submitting it.", nil)
				}
				request.Commit = review.CommitID
			}
			if request.Commit == "" {
				request.Commit = value.Head.SHA
			}
			plan.request.Commit = request.Commit
			var commit struct {
				SHA string `json:"sha"`
			}
			if err := write.Read(ctx, connection.Client, connection.Base+"/git/commits/"+url.PathEscape(request.Commit), &commit); err != nil {
				return nil, err
			}
			if commit.SHA != request.Commit {
				return nil, app.Wrap(app.InvalidArgument, "--commit must be a full remote commit SHA.", nil)
			}
		}
		plan.targets = append(plan.targets, prepared{
			number: number,
			before: value,
			err:    err,
		})
	}
	return plan, nil
}

// validate 约束不同操作可携带的字段，防止状态或合并夹带隐藏编辑。
// 审查正文按事件要求，创建 head 必须已明确，编号只在状态转换允许多选。
func validate(request *Request) error {
	changes := request.Changes
	if changes.Assignees != nil {
		values := []string{}
		seen := map[string]bool{}
		for _, name := range *changes.Assignees {
			if !validName(name) {
				return app.Wrap(app.InvalidArgument, "Assignee names must be explicit non-empty accounts.", nil)
			}
			key := strings.ToLower(name)
			if !seen[key] {
				seen[key] = true
				values = append(values, name)
			}
		}
		request.Changes.Assignees = &values
		changes = request.Changes
	}
	if changes.Title != nil && strings.TrimSpace(*changes.Title) == "" {
		return app.Wrap(app.InvalidArgument, "PR title must not be empty.", nil)
	}
	if changes.Body != nil && len(*changes.Body) > 256<<10 {
		return app.Wrap(app.InvalidArgument, "PR text exceeds the 256 KiB business limit.", nil)
	}
	hasFields := changes.Title != nil || changes.Body != nil || changes.Assignees != nil || changes.Labels != nil || changes.Milestone != nil || len(request.LabelRefs) > 0 || request.MilestoneRef != ""
	hasMeta := changes.Title != nil || changes.Assignees != nil || changes.Labels != nil || changes.Milestone != nil || len(request.LabelRefs) > 0 || request.MilestoneRef != ""
	if request.Operation != "pr.create" && request.Head != "" {
		return app.Wrap(app.InvalidArgument, "The PR head cannot be edited.", nil)
	}
	switch request.Operation {
	case "pr.create":
		if changes.Title == nil || request.Base == "" || request.Head == "" || len(request.Numbers) > 0 {
			return app.Wrap(app.InvalidArgument, "PR creation requires title, base, head, and no existing PR number.", nil)
		}
	case "pr.edit":
		if !hasFields && request.Base == "" {
			return app.Wrap(app.InvalidArgument, "Provide at least one PR edit or clear option.", nil)
		}
	case "pr.close", "pr.reopen", "pr.merge":
		if hasFields || request.Base != "" {
			return app.Wrap(app.InvalidArgument, "State and merge operations cannot include edit fields.", nil)
		}
	case "pr.comment.add", "pr.review.create":
		if hasMeta || request.Base != "" {
			return app.Wrap(app.InvalidArgument, "Comments and reviews cannot include unrelated edit fields.", nil)
		}
		if request.Operation == "pr.comment.add" || request.Event != "approve" {
			if changes.Body == nil || strings.TrimSpace(*changes.Body) == "" {
				return app.Wrap(app.InvalidArgument, "A non-empty discussion or review body is required.", nil)
			}
		}
		if request.Operation == "pr.review.create" && request.Event != "approve" && request.Event != "comment" && request.Event != "request-changes" {
			return app.Wrap(app.InvalidArgument, "Review event must be approve, comment, or request-changes.", nil)
		}
	case "pr.reviewer.add":
		if hasFields || request.Base != "" || len(request.Users)+len(request.Teams) == 0 {
			return app.Wrap(app.InvalidArgument, "Provide reviewers without unrelated edit fields.", nil)
		}
	default:
		return app.Wrap(app.InvalidArgument, "Unknown PR operation.", nil)
	}
	if request.Operation != "pr.create" && request.Operation != "pr.reviewer.add" && len(request.Users)+len(request.Teams) > 0 {
		return app.Wrap(app.InvalidArgument, "Reviewers belong to PR creation or reviewer add.", nil)
	}
	if request.Operation != "pr.review.create" && (request.Event != "" || request.Commit != "" || request.ReviewID != 0) {
		return app.Wrap(app.InvalidArgument, "Review options belong to review create.", nil)
	}
	if request.ReviewID < 0 || (request.Operation != "pr.merge" && (request.Method != "" || request.DeleteBranch)) {
		return app.Wrap(app.InvalidArgument, "Merge or review options do not match this operation.", nil)
	}
	for _, values := range [][]string{request.Users, request.Teams} {
		for _, value := range values {
			if !validName(value) {
				return app.Wrap(app.InvalidArgument, "Reviewer names must be explicit non-empty names without control characters.", nil)
			}
		}
	}
	if request.Operation == "pr.create" {
		return nil
	}
	numbers, err := issue.NormalizeNumbers(request.Numbers)
	if err != nil {
		return err
	}
	if request.Operation != "pr.close" && request.Operation != "pr.reopen" && len(numbers) != 1 {
		return app.Wrap(app.InvalidArgument, "This PR operation accepts exactly one number.", nil)
	}
	request.Numbers = numbers
	if request.Operation == "pr.merge" && request.Method == "" {
		request.Method = "merge"
	}
	return nil
}

// validName 拒绝空名称和控制字符，不通过用户名猜测身份。
// 用户或团队是否可审查由服务器最终裁决，不循环尝试全站候选。
func validName(value string) bool {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

// creationBranches 解析明确的 head 和 base，跨仓库只接受 owner:branch 的 fork 形式。
// 返回远端 SHA 供提交前重核，不自动 push，也不以本地 HEAD 取代远端证据。
func creationBranches(ctx context.Context, connection *write.Connection, request Request) (Branch, Branch, error) {
	headRepository := connection.Target.Repository
	headRef := request.Head
	if owner, branch, cross := strings.Cut(request.Head, ":"); cross {
		if !validName(owner) || branch == "" {
			return Branch{}, Branch{}, app.Wrap(app.InvalidArgument, "Cross-repository head must use owner:branch or owner/repo:branch.", nil)
		}
		resolved, err := resolveHeadRepository(ctx, connection, owner)
		if err != nil {
			return Branch{}, Branch{}, err
		}
		headRepository = resolved
		headRef = branch
	}
	if !target.ValidRepository(headRepository) || (headRepository == connection.Target.Repository && headRef == request.Base) {
		return Branch{}, Branch{}, app.Wrap(app.InvalidArgument, "PR head and base must identify distinct remote branches.", nil)
	}
	head, err := readBranch(ctx, connection, headRepository, headRef)
	if err != nil {
		return Branch{}, Branch{}, err
	}
	base, err := readBranch(ctx, connection, connection.Target.Repository, request.Base)
	return head, base, err
}

// resolveHeadRepository 只在目标仓库和其可见 fork 集合中匹配明确 owner 或 owner/repo。
// 不按同名仓库猜测 fork；重名、不可见或非关联仓库均在创建前拒绝。
func resolveHeadRepository(ctx context.Context, connection *write.Connection, reference string) (string, error) {
	owner := strings.Split(connection.Target.Repository, "/")[0]
	if reference == connection.Target.Repository || reference == owner {
		return connection.Target.Repository, nil
	}
	forks, err := gitea.Pages[Repository](ctx, connection.Client, connection.Base+"/forks", nil, 1, 100, true)
	if err != nil {
		return "", err
	}
	matches := []string{}
	for _, fork := range forks.Items {
		if fork.FullName == reference || (!strings.Contains(reference, "/") && strings.HasPrefix(fork.FullName, reference+"/")) {
			matches = append(matches, fork.FullName)
		}
	}
	if len(matches) != 1 {
		return "", app.Wrap(app.ContextAmbiguous, "The explicit head does not identify one visible fork of the selected repository. Use owner/repo:branch.", nil)
	}
	return matches[0], nil
}

// readBranch 仅查询指定仓库的分支并保留完整仓库信息。
// 不存在分支直接失败，不能自动创建、推送或切换到其他仓库。
func readBranch(ctx context.Context, connection *write.Connection, repository, ref string) (Branch, error) {
	parts := strings.Split(repository, "/")
	base := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	var repo Repository
	if err := write.Read(ctx, connection.Client, base, &repo); err != nil {
		return Branch{}, err
	}
	var branch struct {
		Name   string `json:"name"`
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := write.Read(ctx, connection.Client, base+"/branches/"+url.PathEscape(ref), &branch); err != nil {
		return Branch{}, err
	}
	if branch.Commit.ID == "" || repo.FullName != repository {
		return Branch{}, app.Wrap(app.ServerError, "Remote branch evidence is incomplete.", nil)
	}
	return Branch{
		Ref:  ref,
		SHA:  branch.Commit.ID,
		Repo: repo,
	}, nil
}

// checkMerge 只接受共同合并方式并检查当前仓库允许的策略。
// 冲突、保护规则和权限由服务器最终裁决，不把 mergeable 当作授权。
func checkMerge(ctx context.Context, connection *write.Connection, value PullRequest, method string) error {
	if value.Merged || value.State != "open" {
		return app.Wrap(app.Conflict, "Only an open unmerged PR can be merged.", nil)
	}
	var repo Repository
	if err := write.Read(ctx, connection.Client, connection.Base, &repo); err != nil {
		return err
	}
	if !repo.Permissions.Push && !repo.Permissions.Admin {
		return app.Wrap(app.PermissionDenied, "The selected account has no repository write permission for merging.", nil)
	}
	allowed := map[string]bool{"merge": repo.AllowMerge, "squash": repo.AllowSquash, "rebase": repo.AllowRebase, "rebase-merge": repo.AllowRebaseMerge, "fast-forward-only": repo.AllowFastForward}
	if !allowed[method] {
		return app.Wrap(app.Conflict, "This merge method is not allowed by the repository. Select an explicitly allowed method.", nil)
	}
	return nil
}

// MemoryBytes 估算计划持有的序列化意图与完整前置快照大小，供入口限制缓存。
// 不返回快照内容或凭证；编码失败应拒绝注册，额外预留结构体和索引开销。
func (plan *Plan) MemoryBytes() (int, error) {
	before := []PullRequest{}
	for _, item := range plan.targets {
		before = append(before, item.before)
	}
	encoded, err := json.Marshal(struct {
		Request Request
		Before  []PullRequest
	}{Request: plan.request, Before: before})
	if err != nil {
		return 0, app.Wrap(app.IOError, "Unable to measure the prepared plan.", err)
	}
	return len(encoded)*2 + 4096, nil
}
