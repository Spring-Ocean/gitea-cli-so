package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

// selectChoices 统一业务候选和菜单选择的入口约束及终端降级。
// 不具备 VT 能力时单选编号直接接受，多选显式切换；搜索保留稳定 ID。
func (r *runtime) selectChoices(command *cobra.Command, title string, multi bool, provider tui.Provider) ([]tui.Choice, error) {
	if !r.interactive(command) {
		return nil, app.Wrap(app.InvalidArgument, "Select explicit targets in non-interactive mode.", nil)
	}
	if r.session != nil {
		return r.session.Select(command.Context(), title, multi, provider)
	}
	if output.CapableTerminal(os.Getenv("TERM")) {
		return tui.SelectStyled(command.Context(), command.InOrStdin(), command.ErrOrStderr(), title, multi, provider, r.options.color)
	}
	page, search, review := 1, "", false
	selected := map[string]tui.Choice{}
	order := []string{}
	for {
		values := tui.Page{}
		if review {
			for _, id := range order {
				if choice, exists := selected[id]; exists {
					values.Choices = append(values.Choices, choice)
				}
			}
		} else {
			var err error
			values, err = provider(command.Context(), page, search)
			if err != nil {
				return nil, err
			}
		}
		lines := []string{output.SingleLine(r.options.redactor.Text(title)), fmt.Sprintf("Page %d · Selected %d · Search: %s", page, len(selected), output.SingleLine(search))}
		if !multi {
			lines[1] = fmt.Sprintf("Page %d · Search: %s", page, output.SingleLine(search))
		}
		for index, choice := range values.Choices {
			marker := " "
			if _, exists := selected[choice.ID]; exists {
				marker = "x"
			}
			label := output.SingleLine(r.options.redactor.Text(choice.Label))
			description := output.SingleLine(r.options.redactor.Text(choice.Description))
			if description == "" {
				label, description, _ = strings.Cut(label, " · ")
			}
			role := choice.Role
			if role == "" {
				role = output.StyleTitle
			}
			if choice.ID == "@options" {
				role = output.StyleOption
			}
			label = output.StyleText(command.ErrOrStderr(), r.options.color, role, label)
			if choice.Badge != "" {
				label = output.StyleText(command.ErrOrStderr(), r.options.color, output.StyleWarning, output.SingleLine(choice.Badge)) + " " + label
			}
			if multi {
				lines = append(lines, fmt.Sprintf("%d [%s] %s", index+1, marker, label))
			} else {
				lines = append(lines, fmt.Sprintf("%d %s", index+1, label))
			}
			if description != "" {
				lines = append(lines, "  "+output.StyleText(command.ErrOrStderr(), r.options.color, output.StyleDescription, description))
			}
			lines = append(lines, "")
		}
		if len(values.Choices) == 0 {
			lines = append(lines, "No items.")
		}
		if _, err := fmt.Fprintln(command.ErrOrStderr(), strings.Join(lines, "\n")); err != nil {
			return nil, app.Wrap(app.IOError, "Unable to display choices.", err)
		}
		primary := "Number Toggle · done Continue"
		secondary := "n/p Page · /query Search · s Selected"
		back := "q/Esc Cancel"
		if !multi {
			primary = "Number Choose"
			secondary = "n/p Page · /query Search"
			back = "q/Esc Back"
		}
		hints := output.FormatKeyHints(command.ErrOrStderr(), r.options.color, 240, primary, secondary, back)
		if _, err := fmt.Fprintln(command.ErrOrStderr(), hints); err != nil {
			return nil, app.Wrap(app.IOError, "Unable to display selection controls.", err)
		}
		answer, err := r.askLine(command, "Choice")
		if err != nil {
			return nil, err
		}
		answer = strings.TrimSpace(answer)
		switch {
		case answer == "done" && len(selected) > 0:
			result := []tui.Choice{}
			for _, id := range order {
				if value, exists := selected[id]; exists {
					result = append(result, value)
				}
			}
			return result, nil
		case answer == "n" && !review && values.HasMore:
			page++
		case answer == "p" && !review && page > 1:
			page--
		case answer == "s":
			review = !review
		case strings.HasPrefix(answer, "/"):
			search, page, review = strings.TrimPrefix(answer, "/"), 1, false
		case answer == "q":
			return nil, app.Wrap(app.Cancelled, "Selection cancelled.", context.Canceled)
		default:
			index, err := strconv.Atoi(answer)
			if err != nil || index < 1 || index > len(values.Choices) {
				continue
			}
			choice := values.Choices[index-1]
			if !multi {
				return []tui.Choice{choice}, nil
			}
			if _, exists := selected[choice.ID]; exists {
				delete(selected, choice.ID)
				continue
			}
			if len(selected) >= 100 {
				continue
			}
			if !multi {
				selected = map[string]tui.Choice{}
			}
			known := false
			for _, id := range order {
				known = known || id == choice.ID
			}
			if !known {
				order = append(order, choice.ID)
			}
			selected[choice.ID] = choice
		}
	}
}

// choose 从完整本地候选集选择一个稳定值，支持搜索和分页。
// 用于字段、操作及导航；取消返回错误，不隐式接受第一项。
func (r *runtime) choose(command *cobra.Command, title string, choices []tui.Choice) (string, error) {
	if r.session != nil {
		return r.session.Navigate(command.Context(), title, choices)
	}
	values, err := r.selectChoices(command, title, false, staticChoices(choices))
	if err != nil {
		return "", err
	}
	return values[0].ID, nil
}

// staticChoices 为有界本地候选建立可搜索分页 provider。
// 每页 30 项，过滤只改变视图，不修改稳定 ID 或原集合。
func staticChoices(choices []tui.Choice) tui.Provider {
	return func(ctx context.Context, page int, query string) (tui.Page, error) {
		if err := ctx.Err(); err != nil {
			return tui.Page{}, err
		}
		filtered := []tui.Choice{}
		for _, choice := range choices {
			if query != "" && strings.EqualFold(choice.ID, strings.TrimSpace(query)) {
				return tui.Page{Choices: []tui.Choice{choice}}, nil
			}
		}
		for _, choice := range choices {
			if strings.Contains(strings.ToLower(choice.Label+" "+choice.Description+" "+choice.ID), strings.ToLower(query)) {
				filtered = append(filtered, choice)
			}
		}
		start := min(max(0, (page-1)*30), len(filtered))
		end := min(start+30, len(filtered))
		return tui.Page{
			Choices: filtered[start:end],
			HasMore: end < len(filtered),
		}, nil
	}
}
