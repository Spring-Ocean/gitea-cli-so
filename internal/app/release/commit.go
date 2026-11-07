package release

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
)

// Commit 消费一次 Release 计划，重核配置、身份、对象及实际标签。
// 失败保留草稿、标签和附件的已完成步骤；取消与未知结果不触发重建或回滚。
func (s Service) Commit(ctx context.Context, plan *Plan) (app.Result, error) {
	if plan == nil || !plan.consumed.CompareAndSwap(false, true) {
		return app.Result{}, app.Wrap(app.Conflict, "This release plan is missing or already consumed. Check the server and prepare again.", nil)
	}
	if plan.request.Operation == "release.asset.upload" {
		s.Auth.HTTPOptions.Timeout = 300 * time.Second
	}
	connection, err := s.Begin(ctx, plan.context, plan.revision)
	if err != nil {
		return app.Result{}, err
	}
	defer connection.Close()
	execution, status, runErr := s.execute(ctx, connection, plan)
	var cancelled error
	if errors.Is(ctx.Err(), context.Canceled) {
		cancelled = ctx.Err()
	}
	return app.NewResult(connection.Target.Context(), []app.Item{{
		Target: plan.request.Tag,
		Status: status,
		Data:   execution,
		Error:  app.Failure(runErr),
	}}, cancelled), nil
}

// execute 重读计划目标和标签，只执行预览的单一创建、编辑或附件意图。
// 实际标签与 Release 分别核对；发布可能产生标签，失败也需记录可观察副作用。
func (s Service) execute(ctx context.Context, connection *write.Connection, plan *Plan) (write.Execution, app.Status, error) {
	execution := write.Execution{Steps: []app.Item{}}
	request := plan.request
	current, err := get(ctx, connection, request.Tag)
	if request.Operation == "release.create" {
		if err == nil {
			return write.Step(execution, "preconditions", app.Wrap(app.Conflict, "A release was created after preview. Inspect it before continuing.", nil))
		}
		if app.Failure(err).HTTPStatus != 404 {
			return write.Step(execution, "preconditions", err)
		}
	} else {
		if err != nil {
			return write.Step(execution, "preconditions", err)
		}
		execution.Resource = current
		if !write.Same(current, plan.before) {
			return write.Step(execution, "preconditions", app.Wrap(app.PreconditionChanged, "The release changed after preview. Read it again and confirm a new plan.", nil))
		}
	}
	tag, err := tagSHA(ctx, connection, request.Tag)
	if err != nil {
		return write.Step(execution, "tag", err)
	}
	if tag != plan.tagSHA {
		return write.Step(execution, "tag", app.Wrap(app.PreconditionChanged, "The Git tag changed after preview. Confirm the target again.", nil))
	}
	if request.Operation == "release.asset.upload" {
		return upload(ctx, connection, plan, current, execution)
	}
	if plan.targetRef != "" {
		resolved, err := commitSHA(ctx, connection, plan.targetRef)
		if err != nil {
			return write.Step(execution, "target", err)
		}
		if resolved != plan.resolved {
			return write.Step(execution, "target", app.Wrap(app.PreconditionChanged, "The target ref moved after preview. Confirm its commit again.", nil))
		}
	}
	fields := map[string]any{}
	if request.Title != nil {
		fields["name"] = *request.Title
	}
	if request.Body != nil {
		fields["body"] = *request.Body
	}
	if request.Draft != nil {
		fields["draft"] = *request.Draft
	}
	if request.Prerelease != nil {
		fields["prerelease"] = *request.Prerelease
	}
	if plan.resolved != "" {
		fields["target_commitish"] = plan.resolved
	}
	method, path, step := http.MethodPost, connection.Base+"/releases", "create"
	if request.Operation == "release.create" {
		fields["tag_name"] = request.Tag
	} else {
		method, path, step = http.MethodPatch, path+"/"+strconv.FormatInt(current.ID, 10), "edit"
	}
	var result Release
	err = write.Send(ctx, connection.Client, method, path, fields, &result)
	if result.ID > 0 && result.Tag == request.Tag {
		execution.Resource = result
	}
	if err == nil && (result.ID < 1 || result.Tag != request.Tag) {
		err = app.Wrap(app.ResultUnknown, "The release write response could not be verified. Check the tag and releases before retrying.", nil)
	}
	if err != nil {
		execution = tagSideEffect(ctx, connection, plan, execution)
		return write.Step(execution, step, err)
	}
	execution, _, err = write.Step(execution, step, nil)
	if err != nil {
		return write.Step(execution, "result", err)
	}
	verified, err := get(ctx, connection, request.Tag)
	if err == nil {
		execution.Resource = verified
	}
	if err != nil || !matches(verified, request) || (plan.resolved != "" && verified.Target != plan.resolved) {
		return write.Step(execution, "verify_fields", app.Wrap(app.ResultUnknown, "The release exists, but its requested fields could not be confirmed. Inspect the returned release before retrying.", err))
	}
	if !verified.Draft {
		actual, err := tagSHA(ctx, connection, request.Tag)
		if err != nil || actual == "" || (plan.resolved != "" && actual != plan.resolved) {
			return write.Step(execution, "verify_tag", app.Wrap(app.ResultUnknown, "The release was published, but its Git tag target could not be confirmed.", err))
		}
	}
	return write.Step(execution, "verify_fields", nil)
}

// matches 只比较用户明确字段及创建默认状态，不把未提供布尔字段设为 false。
// Target 是预览时解析的提交，实际发布标签另行校验而不是只看元数据。
func matches(value Release, request Request) bool {
	return (request.Title == nil || value.Title == *request.Title) &&
		(request.Body == nil || value.Body == *request.Body) &&
		(request.Draft == nil || value.Draft == *request.Draft) &&
		(request.Prerelease == nil || value.Prerelease == *request.Prerelease)
}

// tagSideEffect 在失败后只读查询本来不存在的标签，记录可能已完成的标签步骤。
// 不删除或重建标签；取消后不额外发起查询，不能声称失败意味着没有副作用。
func tagSideEffect(ctx context.Context, connection *write.Connection, plan *Plan, execution write.Execution) write.Execution {
	if ctx.Err() != nil || plan.tagSHA != "" {
		return execution
	}
	sha, err := tagSHA(ctx, connection, plan.request.Tag)
	if err == nil && sha != "" {
		execution.Steps = append(execution.Steps, app.Item{
			Target: "tag_created",
			Status: app.Success,
			Data:   map[string]string{"tag": plan.request.Tag, "sha": sha},
		})
	}
	return execution
}
