package pullrequest

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Review 保存 PR 级审查、请求审查的用户／团队和明确提交。
// 不是普通评论或代码行评论；待提交审查必须另行校验所有权与提交 ID。
type Review struct {
	ID       int64      `json:"id"`
	Body     string     `json:"body"`
	State    string     `json:"state"`
	CommitID string     `json:"commit_id"`
	User     issue.User `json:"user"`
	Team     struct {
		Name string `json:"name"`
	} `json:"team"`
}

// Commit 消费单次 PR 计划，重核身份／配置并串行执行全部明确目标。
// 已发送写入不重放；主动取消保留步骤和 unknown，以退出 130 优先。
func (s Service) Commit(ctx context.Context, plan *Plan) (app.Result, error) {
	if plan == nil || !plan.consumed.CompareAndSwap(false, true) {
		return app.Result{}, app.Wrap(app.Conflict, "This PR plan is missing or already consumed. Check the server and prepare again.", nil)
	}
	connection, err := s.Begin(ctx, plan.context, plan.revision)
	if err != nil {
		return app.Result{}, err
	}
	defer connection.Close()
	items := []app.Item{}
	var stop error
	for _, target := range plan.targets {
		name := strconv.FormatInt(target.number, 10)
		if target.number == 0 {
			name = "new_pr"
		}
		item := app.Item{Target: name}
		if ctx.Err() != nil {
			stop = app.Failure(ctx.Err())
		}
		if stop != nil {
			item.Status = app.NotExecuted
		} else if target.err != nil {
			item.Status, item.Error = app.Failed, app.Failure(target.err)
		} else {
			execution, status, runErr := s.execute(ctx, connection, plan, target)
			item.Data, item.Status, item.Error = execution, status, app.Failure(runErr)
			if write.Systemic(runErr) {
				stop = runErr
			}
		}
		items = append(items, item)
	}
	var cancelled error
	if errors.Is(ctx.Err(), context.Canceled) {
		cancelled = ctx.Err()
	}
	return app.NewResult(connection.Target.Context(), items, cancelled), nil
}

// execute 重读并核对单个 PR，状态、审查和合并均使用预览的明确目标。
// 多步骤失败只保留已完成操作；不自动重建、补偿、改合并方式或自动重试。
func (s Service) execute(ctx context.Context, connection *write.Connection, plan *Plan, target prepared) (write.Execution, app.Status, error) {
	execution := write.Execution{Steps: []app.Item{}}
	request := plan.request
	if request.Operation == "pr.create" {
		return s.create(ctx, connection, plan)
	}
	value, err := get(ctx, connection, target.number)
	if err != nil {
		return execution, app.Failed, err
	}
	execution.Resource = value
	desired := ""
	if request.Operation == "pr.close" {
		desired = "closed"
	} else if request.Operation == "pr.reopen" {
		desired = "open"
	}
	if desired == "open" && value.Merged {
		return write.Step(execution, "state", app.Wrap(app.Conflict, "A merged PR cannot be reopened.", nil))
	}
	if desired != "" && value.State == desired {
		return execution, app.Skipped, nil
	}
	if !samePreconditions(value, target.before) {
		return write.Step(execution, "preconditions", app.Wrap(app.PreconditionChanged, "The PR changed after preview. Read and confirm a new plan.", nil))
	}
	path := connection.Base + "/pulls/" + strconv.FormatInt(value.Number, 10)
	switch request.Operation {
	case "pr.close", "pr.reopen":
		err = write.Send(ctx, connection.Client, http.MethodPatch, path, map[string]any{"state": desired}, nil)
		if err == nil {
			execution, err = readback(ctx, connection, value.Number, execution, func(current PullRequest) bool {
				return !current.Merged && current.State == desired
			})
		}
		return write.Step(execution, "state", err)
	case "pr.edit":
		fields := changeFields(request.Changes)
		if request.Base != "" {
			fields["base"] = request.Base
		}
		clearMilestone := request.Changes.Milestone != nil && *request.Changes.Milestone == 0
		if clearMilestone {
			delete(fields, "milestone")
		}
		if len(fields) > 0 {
			err = write.Send(ctx, connection.Client, http.MethodPatch, path, fields, nil)
			if err == nil {
				checking := request.Changes
				if clearMilestone {
					checking.Milestone = nil
				}
				execution, err = readback(ctx, connection, value.Number, execution, func(current PullRequest) bool {
					return issue.MatchChanges(current.Issue, checking, true) && (request.Base == "" || current.Base.Ref == request.Base)
				})
			}
			updated, status, stepErr := write.Step(execution, "edit", err)
			execution = updated
			if stepErr != nil || !clearMilestone {
				return execution, status, stepErr
			}
		}
		if clearMilestone {
			fresh, freshErr := get(ctx, connection, value.Number)
			if freshErr != nil {
				return write.Step(execution, "clear_milestone", freshErr)
			}
			if !write.Same(fresh.Milestone, target.before.Milestone) {
				return write.Step(execution, "clear_milestone", app.Wrap(app.PreconditionChanged, "The milestone changed before the clear step. Inspect the PR and confirm again.", nil))
			}
			err = write.Send(ctx, connection.Client, http.MethodPatch, connection.Base+"/issues/"+strconv.FormatInt(value.Number, 10), map[string]any{"milestone": 0}, nil)
			if err == nil {
				execution, err = readback(ctx, connection, value.Number, execution, func(current PullRequest) bool {
					return current.Milestone == nil
				})
			}
			return write.Step(execution, "clear_milestone", err)
		}
	case "pr.comment.add":
		var comment issue.Comment
		err = write.Send(ctx, connection.Client, http.MethodPost, connection.Base+"/issues/"+strconv.FormatInt(value.Number, 10)+"/comments", map[string]any{"body": *request.Changes.Body}, &comment)
		if err == nil && (comment.ID < 1 || comment.Body != *request.Changes.Body) {
			err = app.Wrap(app.ResultUnknown, "The PR comment response could not be verified. Check comments before retrying.", nil)
		}
		if err == nil {
			execution.Resource = comment
		}
		return write.Step(execution, "comment", err)
	case "pr.reviewer.add":
		err = requestReviewers(ctx, connection, value.Number, request.Users, request.Teams)
		return write.Step(execution, "request_reviewers", err)
	case "pr.review.create":
		event := map[string]string{"approve": "APPROVED", "comment": "COMMENT", "request-changes": "REQUEST_CHANGES"}[request.Event]
		body := ""
		if request.Changes.Body != nil {
			body = *request.Changes.Body
		}
		payload := map[string]any{"event": event, "body": body, "commit_id": request.Commit}
		reviewPath := path + "/reviews"
		if request.ReviewID > 0 {
			reviewPath += "/" + strconv.FormatInt(request.ReviewID, 10)
			var pending Review
			if err := write.Read(ctx, connection.Client, reviewPath, &pending); err != nil {
				return write.Step(execution, "review", err)
			}
			if pending.State != "PENDING" || !strings.EqualFold(pending.User.Login, connection.Target.Account) || pending.CommitID != request.Commit {
				return write.Step(execution, "review", app.Wrap(app.PreconditionChanged, "The pending review owner, state, or commit changed. Prepare again.", nil))
			}
			delete(payload, "commit_id")
		}
		var review Review
		err = write.Send(ctx, connection.Client, http.MethodPost, reviewPath, payload, &review)
		if err == nil && (review.ID < 1 || review.State != event || review.CommitID != request.Commit || review.Body != body) {
			err = app.Wrap(app.ResultUnknown, "The submitted review could not be verified. Inspect reviews before retrying.", nil)
		}
		if err == nil {
			execution.Resource = review
		}
		return write.Step(execution, "review", err)
	case "pr.merge":
		return s.merge(ctx, connection, request, value, execution)
	}
	return write.Step(execution, "operation", app.Wrap(app.InvalidArgument, "No PR mutation was specified.", nil))
}

// create 校验预览分支仍指向同一 SHA，然后只创建一次 PR。
// 旧版审查请求独立执行，失败保留 PR 与未完成步骤，不会为了补字段重建。
func (s Service) create(ctx context.Context, connection *write.Connection, plan *Plan) (write.Execution, app.Status, error) {
	execution := write.Execution{Steps: []app.Item{}}
	source, destination, err := creationBranches(ctx, connection, plan.request)
	if err != nil {
		return write.Step(execution, "branches", err)
	}
	if source.SHA != plan.source.SHA || destination.SHA != plan.destination.SHA || source.Repo.ID != plan.source.Repo.ID {
		return write.Step(execution, "branches", app.Wrap(app.PreconditionChanged, "A PR source or destination branch changed after preview. Confirm again.", nil))
	}
	request := plan.request
	fields := changeFields(request.Changes)
	fields["base"], fields["head"] = request.Base, request.Head
	if _, branch, cross := strings.Cut(request.Head, ":"); cross {
		fields["head"] = strings.Split(source.Repo.FullName, "/")[0] + ":" + branch
	}
	if s.Version.CreateReviewers() {
		fields["reviewers"], fields["team_reviewers"] = request.Users, request.Teams
	}
	var value PullRequest
	err = write.Send(ctx, connection.Client, http.MethodPost, connection.Base+"/pulls", fields, &value)
	if value.Number > 0 {
		execution.Resource = value
	}
	if err == nil && (value.Number < 1 || value.Head.SHA != source.SHA || value.Base.Ref != destination.Ref || value.Head.Repo.ID != source.Repo.ID || value.Base.Repo.ID != destination.Repo.ID) {
		err = app.Wrap(app.ResultUnknown, "The created PR response could not be verified. Check the repository before retrying.", nil)
	}
	execution, status, err := write.Step(execution, "create", err)
	if err != nil {
		if !s.Version.CreateReviewers() && len(request.Users)+len(request.Teams) > 0 {
			execution.Steps = append(execution.Steps, app.Item{
				Target: "request_reviewers",
				Status: app.NotExecuted,
			})
		}
		return execution, status, err
	}
	execution, err = readback(ctx, connection, value.Number, execution, func(current PullRequest) bool {
		return issue.MatchChanges(current.Issue, request.Changes, true) && current.Head.SHA == source.SHA && current.Base.Ref == destination.Ref && current.Head.Repo.ID == source.Repo.ID && current.Base.Repo.ID == destination.Repo.ID
	})
	execution, status, err = write.Step(execution, "verify_fields", err)
	if err != nil {
		if len(request.Users)+len(request.Teams) > 0 {
			execution.Steps = append(execution.Steps, app.Item{
				Target: "verify_reviewers",
				Status: app.NotExecuted,
			})
		}
		return execution, status, err
	}
	if len(request.Users)+len(request.Teams) > 0 {
		if s.Version.CreateReviewers() {
			err = verifyReviewers(ctx, connection, value.Number, request.Users, request.Teams)
		} else {
			err = requestReviewers(ctx, connection, value.Number, request.Users, request.Teams)
		}
		return write.Step(execution, "request_reviewers", err)
	}
	return execution, app.Success, nil
}

// readback 写后读取并核对明确字段，不因失败擦除最后已知资源。
// 不匹配或读取失败保留 unknown；已成功的前置步骤由聚合结果报告 partial。
func readback(ctx context.Context, connection *write.Connection, number int64, execution write.Execution, matches func(PullRequest) bool) (write.Execution, error) {
	value, err := get(ctx, connection, number)
	if err == nil {
		execution.Resource = value
	}
	if err != nil || !matches(value) {
		return execution, app.Wrap(app.ResultUnknown, "The PR write could not be confirmed by a read-back. Inspect the returned PR before retrying.", err)
	}
	return execution, nil
}

// changeFields 只序列化明确共同编辑字段，不包含未提供值。
// PR 的标签可使用 PATCH，里程碑清空由上层以独立共享资源步骤适配。
func changeFields(changes issue.Changes) map[string]any {
	fields := map[string]any{}
	if changes.Title != nil {
		fields["title"] = *changes.Title
	}
	if changes.Body != nil {
		fields["body"] = *changes.Body
	}
	if changes.Assignees != nil {
		fields["assignees"] = *changes.Assignees
	}
	if changes.Labels != nil {
		fields["labels"] = *changes.Labels
	}
	if changes.Milestone != nil {
		fields["milestone"] = *changes.Milestone
	}
	return fields
}

// requestReviewers 发送明确的用户／团队集合并读回审查请求核对。
// 不枚举全站账号，不在权限失败后改用其他用户，也不重放 POST。
func requestReviewers(ctx context.Context, connection *write.Connection, number int64, users, teams []string) error {
	path := connection.Base + "/pulls/" + strconv.FormatInt(number, 10) + "/requested_reviewers"
	if err := write.Send(ctx, connection.Client, http.MethodPost, path, map[string]any{"reviewers": users, "team_reviewers": teams}, nil); err != nil {
		return err
	}
	return verifyReviewers(ctx, connection, number, users, teams)
}

// verifyReviewers 在完整有界审查记录中核对请求的用户或团队。
// 服务端静默忽略部分审查者时返回未知，不能将缺失字段视作成功。
func verifyReviewers(ctx context.Context, connection *write.Connection, number int64, users, teams []string) error {
	page, err := gitea.Pages[Review](ctx, connection.Client, connection.Base+"/pulls/"+strconv.FormatInt(number, 10)+"/reviews", nil, 1, 100, true)
	if err != nil {
		return app.Wrap(app.ResultUnknown, "Review requests could not be read back. Check reviews before retrying.", err)
	}
	for _, group := range []struct {
		names []string
		team  bool
	}{{users, false}, {teams, true}} {
		for _, name := range group.names {
			found := false
			for _, review := range page.Items {
				candidate := review.User.Login
				if group.team {
					candidate = review.Team.Name
				}
				found = found || (strings.EqualFold(candidate, name) && review.State == "REQUEST_REVIEW")
			}
			if !found {
				return app.Wrap(app.ResultUnknown, "Some requested reviewers were not confirmed. Inspect reviews before retrying.", nil)
			}
		}
	}
	return nil
}

// merge 重核仓库方法并发送绑定 head SHA 的唯一合并请求。
// 合并及删除源分支分开记录，错误后只读核对已合并状态，不重新合并。
func (s Service) merge(ctx context.Context, connection *write.Connection, request Request, value PullRequest, execution write.Execution) (write.Execution, app.Status, error) {
	if err := checkMerge(ctx, connection, value, request.Method); err != nil {
		return write.Step(execution, "merge", err)
	}
	path := connection.Base + "/pulls/" + strconv.FormatInt(value.Number, 10) + "/merge"
	sendErr := write.Send(ctx, connection.Client, http.MethodPost, path, s.Version.MergeFields(request.Method, value.Head.SHA, request.DeleteBranch), nil)
	current, readErr := get(ctx, connection, value.Number)
	if readErr == nil {
		execution.Resource = current
	}
	if readErr != nil || !current.Merged {
		if sendErr == nil {
			sendErr = app.Wrap(app.ResultUnknown, "The merge response was accepted, but the PR is not confirmed merged. Inspect it before retrying.", readErr)
		}
		return write.Step(execution, "merge", sendErr)
	}
	execution, _, _ = write.Step(execution, "merge", nil)
	if !request.DeleteBranch {
		if sendErr != nil {
			return write.Step(execution, "merge_response", sendErr)
		}
		return execution, app.Success, nil
	}
	parts := strings.Split(value.Head.Repo.FullName, "/")
	if len(parts) != 2 {
		return write.Step(execution, "delete_branch", app.Wrap(app.ResultUnknown, "The PR merged, but source branch deletion could not be verified.", sendErr))
	}
	var branch map[string]any
	branchPath := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/branches/" + url.PathEscape(value.Head.Ref)
	err := write.Read(ctx, connection.Client, branchPath, &branch)
	if err != nil && app.Failure(err).HTTPStatus == 404 {
		var repository Repository
		err = write.Read(ctx, connection.Client, "/repos/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1]), &repository)
		if err == nil {
			return write.Step(execution, "delete_branch", nil)
		}
	}
	return write.Step(execution, "delete_branch", app.Wrap(app.PartialFailure, "The PR merged, but source branch deletion was not confirmed. Check the source repository; do not merge again.", errors.Join(sendErr, err)))
}

// samePreconditions 比较明确业务字段和分支 SHA，忽略只读时也会异步变化的合并缓存。
// 审查请求列表和更新时间不作为编辑锁；合并方式与保护规则仍在写前由服务器重新核验。
func samePreconditions(left, right PullRequest) bool {
	left.Mergeable, right.Mergeable = false, false
	left.RequestedReviewers, right.RequestedReviewers = nil, nil
	left.UpdatedAt, right.UpdatedAt = "", ""
	left.Head.Repo = Repository{
		ID:       left.Head.Repo.ID,
		FullName: left.Head.Repo.FullName,
	}
	right.Head.Repo = Repository{
		ID:       right.Head.Repo.ID,
		FullName: right.Head.Repo.FullName,
	}
	left.Base.Repo = Repository{
		ID:       left.Base.Repo.ID,
		FullName: left.Base.Repo.FullName,
	}
	right.Base.Repo = Repository{
		ID:       right.Base.Repo.ID,
		FullName: right.Base.Repo.FullName,
	}
	return write.Same(left, right)
}
