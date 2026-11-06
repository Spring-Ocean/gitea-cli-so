package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

// collectionSelection 为缺少增量值的命令提供稳定 ID 多选。
// 移除只列当前集合，添加从完整有界仓库候选选择；无终端立即返回参数错误。
func (r *runtime) collectionSelection(command *cobra.Command, service issue.Service, index int64, operation string) ([]string, error) {
	if !r.interactive(command) {
		return nil, app.Wrap(app.InvalidArgument, "Provide at least one --user or --label value in non-interactive mode.", nil)
	}
	assignees := strings.Contains(operation, ".assignee.")
	remove := strings.HasSuffix(operation, ".remove")
	users := []issue.User{}
	labels := []issue.Label{}
	if remove {
		result, err := service.View(command.Context(), index)
		if err != nil {
			return nil, err
		}
		value, ok := result.Items[0].Data.(issue.Issue)
		if !ok {
			return nil, app.Wrap(app.ServerError, "The issue response has an unexpected shape.", nil)
		}
		users, labels = value.Assignees, value.Labels
	} else {
		kind := "labels"
		if assignees {
			kind = "assignees"
		}
		catalog, err := service.Catalog(command.Context(), kind)
		if err != nil {
			return nil, err
		}
		if assignees {
			var ok bool
			users, ok = catalog.([]issue.User)
			if !ok {
				return nil, app.Wrap(app.ServerError, "The assignee catalog has an unexpected shape.", nil)
			}
		} else {
			var ok bool
			labels, ok = catalog.([]issue.Label)
			if !ok {
				return nil, app.Wrap(app.ServerError, "The label catalog has an unexpected shape.", nil)
			}
		}
	}
	choices := []tui.Choice{}
	for _, user := range users {
		choices = append(choices, tui.Choice{
			ID:    user.Login,
			Label: r.options.redactor.Text(user.Login),
		})
	}
	if !assignees {
		choices = []tui.Choice{}
		for _, label := range labels {
			choices = append(choices, tui.Choice{
				ID:    strconv.FormatInt(label.ID, 10),
				Label: r.options.redactor.Text(fmt.Sprintf("#%d %s", label.ID, label.Name)),
			})
		}
	}
	title := "Select repository candidates · complete bounded collection"
	if remove {
		title = "Select current issue values to remove · #" + strconv.FormatInt(index, 10)
	}
	selected, err := tui.Select(command.Context(), command.InOrStdin(), command.ErrOrStderr(), title, true, func(ctx context.Context, page int, search string) (tui.Page, error) {
		filtered := []tui.Choice{}
		for _, choice := range choices {
			if strings.Contains(strings.ToLower(choice.Label), strings.ToLower(search)) {
				filtered = append(filtered, choice)
			}
		}
		start := (page - 1) * 30
		if start > len(filtered) {
			start = len(filtered)
		}
		end := start + 30
		if end > len(filtered) {
			end = len(filtered)
		}
		return tui.Page{
			Choices: filtered[start:end],
			HasMore: end < len(filtered),
		}, nil
	})
	if err != nil {
		return nil, err
	}
	values := []string{}
	for _, choice := range selected {
		values = append(values, choice.ID)
	}
	return values, nil
}
