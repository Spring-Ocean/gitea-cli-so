package issue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Execution 保存当前目标已完成步骤和最后可确认的 Issue。
// 多步骤失败不伪装为事务回滚，步骤内 unknown 要求先查询远端再继续。
type Execution struct {
	Issue *Issue     `json:"issue,omitempty"`
	Steps []app.Item `json:"steps"`
}

// Commit 消费一次准备计划，重核配置与身份后按仓库锁串行执行。
// 调用者必须先完成入口确认；取消保留已完成和未知步骤，未开始项明确标记。
func (s Service) Commit(ctx context.Context, plan *Plan) (app.Result, error) {
	if plan == nil || !plan.consumed.CompareAndSwap(false, true) {
		return app.Result{}, app.Wrap(app.Conflict, "This plan is missing or has already been consumed. Prepare a new operation after checking the server.", nil)
	}
	if s.Locks == nil {
		return app.Result{}, app.Wrap(app.ConfigError, "A shared write lock set is required.", nil)
	}
	if plan.targetContext.Instance != s.Target.Name || plan.targetContext.Repository != s.Target.Repository {
		return app.Result{}, app.Wrap(app.PreconditionChanged, "The selected context changed after preview.", nil)
	}
	key := strings.Join([]string{s.Target.Instance.URL, plan.targetContext.Account, s.Target.Repository}, "\x00")
	release, err := s.Locks.Acquire(ctx, key)
	if err != nil {
		return app.Result{}, err
	}
	defer release()
	snapshot, err := s.Auth.Store.Load(ctx)
	if err != nil {
		return app.Result{}, err
	}
	if snapshot.RevisionKey() != plan.revision {
		return app.Result{}, app.Wrap(app.ConfigConflict, "Configuration changed after preview. Prepare and confirm the operation again.", nil)
	}
	t := s.Target
	t.Account = plan.targetContext.Account
	client, t, err := s.Auth.Open(ctx, t, false)
	if err != nil {
		return app.Result{}, err
	}
	defer client.Close()
	base, err := s.base()
	if err != nil {
		return app.Result{}, err
	}
	items := []app.Item{}
	var stop error
	for _, prepared := range plan.targets {
		item := app.Item{Target: targetName(prepared.number)}
		if ctx.Err() != nil {
			stop = app.Failure(ctx.Err())
		}
		if stop != nil {
			item.Status = app.NotExecuted
			items = append(items, item)
			continue
		}
		if prepared.err != nil {
			item.Status, item.Error = app.Failed, app.Failure(prepared.err)
			items = append(items, item)
			continue
		}
		execution, status, runErr := s.execute(ctx, client, base, plan, prepared)
		item.Data, item.Status, item.Error = execution, status, app.Failure(runErr)
		items = append(items, item)
		if runErr != nil && systemic(runErr) {
			stop = runErr
		}
	}
	var cancelled error
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		cancelled = app.Failure(ctx.Err())
	}
	return app.NewResult(t.Context(), items, cancelled), nil
}

// execute 重读当前目标并按已确认的单一意图执行，不自动补偿或重试。
// 预览后可观察到的变化要求重新确认；状态已达目标时可安全报告 skipped。
func (s Service) execute(ctx context.Context, client *gitea.Client, base string, plan *Plan, prepared preparedTarget) (Execution, app.Status, error) {
	execution := Execution{Steps: []app.Item{}}
	request := plan.request
	path := base + "/issues"
	if request.Operation == "issue.create" {
		return create(ctx, client, base, request.Changes)
	}
	value, err := get(ctx, client, base, prepared.number)
	if err != nil {
		return execution, app.Failed, err
	}
	execution.Issue = &value
	path += "/" + strconv.FormatInt(prepared.number, 10)
	desired := ""
	if request.Operation == "issue.close" {
		desired = "closed"
	} else if request.Operation == "issue.reopen" {
		desired = "open"
	}
	if desired != "" && value.State == desired {
		return execution, app.Skipped, nil
	}
	currentFingerprint, currentErr := fingerprint(value)
	previewFingerprint, previewErr := fingerprint(prepared.before)
	if err := errors.Join(currentErr, previewErr); err != nil {
		return execution, app.Failed, err
	}
	if currentFingerprint != previewFingerprint {
		return execution, app.Failed, app.Wrap(app.PreconditionChanged, "The issue changed after preview. Read it again and confirm a new plan.", nil)
	}
	switch request.Operation {
	case "issue.close", "issue.reopen":
		err = send(ctx, client, http.MethodPatch, path, map[string]any{"state": desired}, nil)
		if err == nil {
			current, verifyErr := verifyRead(ctx, client, base, prepared.number, func(value Issue) bool {
				return value.State == desired
			})
			err = verifyErr
			if current.Number > 0 {
				execution.Issue = &current
			}
		}
		return finish(execution, "state", err)
	case "issue.comment.add":
		var comment Comment
		err = send(ctx, client, http.MethodPost, path+"/comments", map[string]any{"body": *request.Changes.Body}, &comment)
		if err == nil && comment.ID < 1 {
			err = app.Wrap(app.ResultUnknown, "The comment response could not be verified. Check comments before retrying.", nil)
		}
		if err == nil {
			execution.Steps = append(execution.Steps, app.Item{
				Target: "comment",
				Status: app.Success,
				Data:   comment,
			})
			return execution, app.Success, nil
		}
		return finish(execution, "comment", err)
	case "issue.edit":
		fields := patchFields(request.Changes)
		if len(fields) > 0 {
			err = send(ctx, client, http.MethodPatch, path, fields, nil)
			if err == nil {
				current, verifyErr := verifyRead(ctx, client, base, prepared.number, func(value Issue) bool {
					return changesMatch(value, request.Changes, false)
				})
				err = verifyErr
				if current.Number > 0 {
					execution.Issue = &current
				}
			}
			updated, status, finishErr := finish(execution, "edit", err)
			execution = updated
			if finishErr != nil {
				return execution, status, finishErr
			}
			if request.Changes.Labels == nil {
				return execution, status, nil
			}
		}
		fresh, freshErr := get(ctx, client, base, prepared.number)
		if freshErr != nil || !equalIDs(labelIDs(fresh.Labels), labelIDs(prepared.before.Labels)) {
			return finish(execution, "replace_labels", app.Wrap(app.PreconditionChanged, "Labels changed before the replacement step. Read and confirm again.", freshErr))
		}
		err = send(ctx, client, http.MethodPut, path+"/labels", map[string]any{"labels": *request.Changes.Labels}, nil)
		if err == nil {
			current, verifyErr := verifyRead(ctx, client, base, prepared.number, func(value Issue) bool {
				return equalIDs(labelIDs(value.Labels), *request.Changes.Labels)
			})
			err = verifyErr
			if current.Number > 0 {
				execution.Issue = &current
			}
		}
		return finish(execution, "replace_labels", err)
	case "issue.assignee.add", "issue.assignee.remove":
		return s.assignees(ctx, client, base, path, plan, value, execution)
	case "issue.label.add", "issue.label.remove":
		return s.labels(ctx, client, base, path, plan, value, execution)
	}
	return execution, app.Failed, app.Wrap(app.InvalidArgument, "Unknown prepared operation.", nil)
}

// create 只创建一次 Issue，再以返回编号读回全部显式字段，防止权限导致静默丢弃。
// 已确认创建后保留编号与成功步骤；字段缺失或读回失败报告部分完成，绝不重新创建。
func create(ctx context.Context, client *gitea.Client, base string, changes Changes) (Execution, app.Status, error) {
	execution := Execution{Steps: []app.Item{}}
	payload := patchFields(changes)
	if changes.Labels != nil {
		payload["labels"] = *changes.Labels
	}
	var value Issue
	err := send(ctx, client, http.MethodPost, base+"/issues", payload, &value)
	if value.Number > 0 {
		execution.Issue = &value
	}
	if err == nil && (value.Number < 1 || (len(value.PullRequest) > 0 && string(value.PullRequest) != "null")) {
		err = app.Wrap(app.ResultUnknown, "The create response could not be verified. Check the repository before retrying.", nil)
	}
	if err != nil {
		return finish(execution, "create", err)
	}
	execution.Steps = append(execution.Steps, app.Item{
		Target: "create",
		Status: app.Success,
	})
	current, err := get(ctx, client, base, value.Number)
	if err != nil {
		return finish(execution, "verify_fields", app.Wrap(app.ResultUnknown, "The issue was created, but its fields could not be read back. Inspect the returned issue number before continuing; do not create it again.", err))
	}
	execution.Issue = &current
	if !changesMatch(current, changes, true) {
		err = app.Wrap(app.PartialFailure, "The issue was created, but some requested fields were not retained. Inspect the returned issue and account permissions; do not create it again.", nil)
	}
	return finish(execution, "verify_fields", err)
}

// send 发送单次 JSON 写入，不因错误改变请求字段或重放。
// 2xx 响应截断或无法解析时按结果未知处理，非成功状态保持服务端错误类别。
func send(ctx context.Context, client *gitea.Client, method, path string, payload any, destination any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return app.Wrap(app.InvalidArgument, "Unable to encode the write request.", err)
	}
	response, err := client.Do(ctx, gitea.Request{
		Method:  method,
		Path:    path,
		Headers: http.Header{"Content-Type": {"application/json"}},
		Body:    bytes.NewReader(encoded),
	})
	if err != nil {
		return err
	}
	if err := gitea.StatusError(response.StatusCode); err != nil {
		response.Body.Close()
		return err
	}
	if response.StatusCode == http.StatusNoContent {
		if err := response.Body.Close(); err != nil {
			return app.Wrap(app.ResultUnknown, "The accepted write response could not be closed cleanly. Check the server.", err)
		}
		return nil
	}
	if destination == nil {
		destination = new(any)
	}
	if err := gitea.Decode(response, destination); err != nil {
		return app.Wrap(app.ResultUnknown, "The server accepted the write, but the response could not be verified. Check the server before retrying.", err)
	}
	return nil
}

// verifyRead 在写入成功后核对预期状态，不重试写入。
// 读取失败或不匹配都保留结果未知，不声称写入已回滚。
func verifyRead(ctx context.Context, client *gitea.Client, base string, index int64, matches func(Issue) bool) (Issue, error) {
	value, err := get(ctx, client, base, index)
	if err != nil || !matches(value) {
		return value, app.Wrap(app.ResultUnknown, "The write could not be confirmed by a read-back. Inspect the issue before retrying.", err)
	}
	return value, nil
}

// finish 追加一个已执行步骤并按成功／失败／未知聚合目标。
// 同一目标的多步骤已经部分成功时返回 partial，不丢弃已完成步骤。
func finish(execution Execution, name string, err error) (Execution, app.Status, error) {
	status := app.Success
	if err != nil {
		status = app.Failed
		if app.Failure(err).Code == app.ResultUnknown {
			status = app.Unknown
		} else if errors.Is(err, context.Canceled) || app.Failure(err).Code == app.Cancelled {
			status = app.Canceled
		}
	}
	execution.Steps = append(execution.Steps, app.Item{
		Target: name,
		Status: status,
		Error:  app.Failure(err),
	})
	result := app.NewResult(app.Context{}, execution.Steps, nil)
	return execution, result.Status, err
}

// fingerprint 比较可见 Issue 状态及版本时间，不依赖不稳定的集合顺序。
// 此比较只能发现已观察到的变化，不能消除读取与写入之间的远端竞争。
func fingerprint(value Issue) ([32]byte, error) {
	value.Assignees = append([]User{}, value.Assignees...)
	value.Labels = append([]Label{}, value.Labels...)
	sort.Slice(value.Assignees, func(i, j int) bool {
		return value.Assignees[i].Login < value.Assignees[j].Login
	})
	sort.Slice(value.Labels, func(i, j int) bool {
		return value.Labels[i].ID < value.Labels[j].ID
	})
	encoded, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, app.Wrap(app.ServerError, "Unable to compare issue preconditions.", err)
	}
	return sha256.Sum256(encoded), nil
}

// changesMatch 核对显式编辑字段，不把未提供字段纳入清空条件。
// 标签独立替换时单独核对，里程碑 0 对应服务端空对象。
func changesMatch(value Issue, changes Changes, labels bool) bool {
	if changes.Title != nil && value.Title != *changes.Title {
		return false
	}
	if changes.Body != nil && value.Body != *changes.Body {
		return false
	}
	if changes.Assignees != nil && !equalUsers(usernames(value.Assignees), *changes.Assignees) {
		return false
	}
	if changes.Milestone != nil {
		current := int64(0)
		if value.Milestone != nil {
			current = value.Milestone.ID
		}
		if current != *changes.Milestone {
			return false
		}
	}
	return !labels || changes.Labels == nil || equalIDs(labelIDs(value.Labels), *changes.Labels)
}

// usernames 提取负责人集合并保留服务端账号名称。
// 比较函数另行排序，输出不会把 helper 占位用户名引入集合。
func usernames(users []User) []string {
	names := []string{}
	for _, user := range users {
		names = append(names, user.Login)
	}
	return names
}

// labelIDs 提取稳定标签 ID，不用可重名标题作为写入键。
// 返回独立切片，调用方排序不会改变读取结果。
func labelIDs(labels []Label) []int64 {
	ids := []int64{}
	for _, label := range labels {
		ids = append(ids, label.ID)
	}
	return ids
}

// equalUsers 按大小写无关的账号集合比较，不改变输入次序。
// 重复元素在调用方规范化，服务端异常重复仍会导致不匹配。
func equalUsers(left, right []string) bool {
	a, b := append([]string{}, left...), append([]string{}, right...)
	for i := range a {
		a[i] = strings.ToLower(a[i])
	}
	for i := range b {
		b[i] = strings.ToLower(b[i])
	}
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}

// equalIDs 比较两个完整标签 ID 集合，不依赖排序。
// 空数组和空集合等价，但明确替换请求仍序列化为空数组。
func equalIDs(left, right []int64) bool {
	a, b := append([]int64{}, left...), append([]int64{}, right...)
	sort.Slice(a, func(i, j int) bool {
		return a[i] < a[j]
	})
	sort.Slice(b, func(i, j int) bool {
		return b[i] < b[j]
	})
	return reflect.DeepEqual(a, b)
}
