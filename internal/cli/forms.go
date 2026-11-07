package cli

import (
	"fmt"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// editFlags 补全当前动作的显式字段，使用真实参数绑定及相同校验规则。
// 留空跳过不改变字段，空正文须选 Clear；提供过的值标识为 set，只有主动编辑才会覆盖。
func (r *runtime) editFlags(command *cobra.Command) error {
	if !r.interactive(command) {
		return app.Wrap(app.InvalidArgument, "Provide at least one explicit change in non-interactive mode.", nil)
	}
	for {
		choices := []tui.Choice{{
			ID:          "continue",
			Label:       "Continue with current fields",
			Description: "Use the current input; no changes returns without writing",
		}}
		command.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Hidden || flag.Name == "help" || flag.Name == "with-token" {
				return
			}
			label := "--" + flag.Name + " · " + flag.Usage
			if flag.Changed {
				label = "[set] " + label
			}
			choices = append(choices, tui.Choice{
				ID:    flag.Name,
				Label: label,
			})
		})
		field, err := r.choose(command, "Fields · "+command.CommandPath(), choices)
		if err != nil || field == "continue" {
			return err
		}
		flag := command.Flags().Lookup(field)
		if flag == nil {
			return app.Wrap(app.InvalidArgument, "The selected field no longer exists.", nil)
		}
		mode, err := r.choose(command, "--"+field+" · "+flag.Usage, fieldModes(flag))
		if err != nil {
			return err
		}
		if mode == "skip" {
			continue
		}
		value := mode
		if mode == "candidates" {
			values, err := r.fieldCandidates(command, field)
			if err != nil {
				return err
			}
			ids := []string{}
			for _, choice := range values {
				ids = append(ids, choice.ID)
			}
			value = strings.Join(ids, ",")
		} else if mode == "value" {
			value, err = r.askLine(command, "Value for --"+field+" (empty skips; repeated collection values use the flag format)")
			if err != nil {
				return err
			}
			if value == "" {
				continue
			}
		} else if mode == "clear" {
			if command.Flags().Lookup("clear-"+field) != nil {
				if err := command.Flags().Set("clear-"+field, "true"); err != nil {
					return app.Wrap(app.InvalidArgument, "Unable to set the explicit clear option.", err)
				}
				continue
			}
			value = ""
		}
		if err := command.Flags().Set(field, value); err != nil {
			return app.Wrap(app.InvalidArgument, "Invalid field value for --"+field+".", err)
		}
	}
}

// fieldModes 明确区分设置值、布尔值、跳过及允许清空的正文字段。
// Issue／PR 集合清空由各自 clear 参数承担，不能将空输入当作清空。
func fieldModes(flag *pflag.Flag) []tui.Choice {
	choices := []tui.Choice{{
		ID:    "skip",
		Label: "Skip · keep current input",
	}}
	if flag.Value.Type() == "bool" {
		return append(choices, tui.Choice{
			ID:          "true",
			Label:       "Set true",
			Description: "Explicitly set this option to true",
		}, tui.Choice{
			ID:          "false",
			Label:       "Set false",
			Description: "Explicitly set this option to false",
		})
	}
	choices = append(choices, tui.Choice{
		ID:          "value",
		Label:       "Enter a value",
		Description: "Type an explicit value; empty input keeps the current field",
	})
	switch flag.Name {
	case "label", "assignee", "user", "milestone", "base", "head", "target":
		choices = append(choices, tui.Choice{
			ID:    "candidates",
			Label: "Choose repository candidates · explicit input is also available",
		})
	}
	if flag.Name == "body" {
		choices = append(choices, tui.Choice{
			ID:    "clear",
			Label: "Clear · explicitly use an empty value",
		})
	}
	return choices
}

// noChanges 向用户说明表单没有生成写入意图，正常返回上一层。
// 此路径不准备或提交写计划，也不把未修改字段变为零值。
func noChanges(command *cobra.Command) error {
	if _, err := fmt.Fprintln(command.ErrOrStderr(), "No changes selected. Nothing was written."); err != nil {
		return app.Wrap(app.IOError, "Unable to report unchanged input.", err)
	}
	return nil
}
