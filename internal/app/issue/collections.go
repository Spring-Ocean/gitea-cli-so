package issue

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// assignees 计算明确增量，再按已确认版本使用原生端点或完整集合替换。
// 写前已重读并检查预览，写后核对完整集合；不宣称旧版全局原子无丢失。
func (s Service) assignees(ctx context.Context, client *gitea.Client, base, path string, plan *Plan, value Issue, execution Execution) (Execution, app.Status, error) {
	before := usernames(value.Assignees)
	after := append([]string{}, before...)
	delta := []string{}
	add := plan.request.Operation == "issue.assignee.add"
	for _, requested := range plan.request.Users {
		present := false
		for _, current := range after {
			present = present || strings.EqualFold(current, requested)
		}
		if add && !present {
			after = append(after, requested)
			delta = append(delta, requested)
		} else if !add && present {
			next := []string{}
			for _, current := range after {
				if !strings.EqualFold(current, requested) {
					next = append(next, current)
				}
			}
			after = next
			delta = append(delta, requested)
		}
	}
	if equalUsers(before, after) {
		return execution, app.Skipped, nil
	}
	method, target, payload := http.MethodPost, path+"/assignees", map[string]any{"assignees": delta}
	if !add {
		method = http.MethodDelete
	}
	if plan.legacy {
		method, target, payload = http.MethodPatch, path, map[string]any{"assignees": after}
	}
	err := send(ctx, client, method, target, payload, nil)
	if err == nil {
		current, verifyErr := verifyRead(ctx, client, base, value.Number, func(value Issue) bool {
			return equalUsers(usernames(value.Assignees), after)
		})
		err = verifyErr
		if current.Number > 0 {
			execution.Issue = &current
		}
	}
	return finish(execution, "assignees", err)
}

// labels 使用原生增量添加与逐个删除，不把添加误实现成覆盖整组。
// 多个删除步骤部分完成时保留各项，遇到未知结果立即停止，不自动补偿。
func (s Service) labels(ctx context.Context, client *gitea.Client, base, path string, plan *Plan, value Issue, execution Execution) (Execution, app.Status, error) {
	current := labelIDs(value.Labels)
	requested := *plan.request.Changes.Labels
	delta := []int64{}
	for _, id := range requested {
		present := false
		for _, existing := range current {
			present = present || existing == id
		}
		if (plan.request.Operation == "issue.label.add" && !present) || (plan.request.Operation == "issue.label.remove" && present) {
			delta = append(delta, id)
		}
	}
	if len(delta) == 0 {
		return execution, app.Skipped, nil
	}
	if plan.request.Operation == "issue.label.add" {
		expected := append(append([]int64{}, current...), delta...)
		err := send(ctx, client, http.MethodPost, path+"/labels", map[string]any{"labels": delta}, nil)
		if err == nil {
			result, verifyErr := verifyRead(ctx, client, base, value.Number, func(value Issue) bool {
				return equalIDs(labelIDs(value.Labels), expected)
			})
			err = verifyErr
			if result.Number > 0 {
				execution.Issue = &result
			}
		}
		return finish(execution, "add_labels", err)
	}
	for _, id := range delta {
		if ctx.Err() != nil {
			return finish(execution, "remove_label:"+strconv.FormatInt(id, 10), app.Failure(ctx.Err()))
		}
		expected := []int64{}
		for _, existing := range current {
			if existing != id {
				expected = append(expected, existing)
			}
		}
		err := send(ctx, client, http.MethodDelete, path+"/labels/"+strconv.FormatInt(id, 10), nil, nil)
		if err == nil {
			result, verifyErr := verifyRead(ctx, client, base, value.Number, func(value Issue) bool {
				return equalIDs(labelIDs(value.Labels), expected)
			})
			err = verifyErr
			if result.Number > 0 {
				execution.Issue = &result
			}
		}
		updated, status, finishErr := finish(execution, "remove_label:"+strconv.FormatInt(id, 10), err)
		execution = updated
		if finishErr != nil {
			return execution, status, finishErr
		}
		current = expected
	}
	return execution, app.Success, nil
}
