package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/repository"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

// fieldCandidates 提供字段专属的仓库候选，不枚举全站账号或猜测 fork。
// 标签／账号允许多选，其他字段单选；候选只供明确输入，服务端权限仍在准备和提交时验证。
func (r *runtime) fieldCandidates(command *cobra.Command, field string) ([]tui.Choice, error) {
	service, err := r.issueService(command, false)
	if err != nil {
		return nil, err
	}
	multi := field == "label" || field == "assignee" || field == "user"
	provider := func(ctx context.Context, page int, search string) (tui.Page, error) {
		choices := []tui.Choice{}
		switch field {
		case "label", "assignee", "user":
			kind := "assignees"
			if field == "label" {
				kind = "labels"
			}
			catalog, err := service.Catalog(ctx, kind)
			if err != nil {
				return tui.Page{}, err
			}
			switch values := catalog.(type) {
			case []issue.Label:
				for _, value := range values {
					choices = append(choices, tui.Choice{
						ID:    strconv.FormatInt(value.ID, 10),
						Label: fmt.Sprintf("#%d %s", value.ID, value.Name),
					})
				}
			case []issue.User:
				for _, value := range values {
					choices = append(choices, tui.Choice{
						ID:    value.Login,
						Label: value.Login,
					})
				}
			default:
				return tui.Page{}, app.Wrap(app.ServerError, "The repository catalog has an unexpected shape.", nil)
			}
		default:
			operation := "branch.list"
			if field == "milestone" {
				operation = "milestone.list"
			} else if field == "tag" {
				operation = "tag.list"
			}
			result, err := repository.Read(ctx, service.Auth, service.Target, repository.Query{
				Operation: operation,
				Page:      1,
				Limit:     100,
				All:       true,
			})
			if err != nil {
				return tui.Page{}, err
			}
			values, ok := result.Items[0].Data.(app.Page[map[string]any])
			if !ok {
				return tui.Page{}, app.Wrap(app.ServerError, "The candidate page has an unexpected shape.", nil)
			}
			for _, value := range values.Items {
				name, _ := value["name"].(string)
				id := name
				if field == "milestone" {
					name, _ = value["title"].(string)
					id = fmt.Sprint(value["id"])
				}
				choices = append(choices, tui.Choice{
					ID:    id,
					Label: id + " · " + name,
				})
			}
		}
		for index := range choices {
			choices[index].Label = r.options.redactor.Text(choices[index].Label)
		}
		return staticChoices(choices)(ctx, page, search)
	}
	return r.selectChoices(command, "Repository candidates · "+field+" · "+service.Target.Repository+" · bounded complete collection", multi, provider)
}
