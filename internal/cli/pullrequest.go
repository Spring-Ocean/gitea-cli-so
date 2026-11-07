package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/pullrequest"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

type prOptions struct {
	fields       issueFields
	base         string
	head         string
	users        []string
	teams        []string
	event        string
	commit       string
	reviewID     int64
	method       string
	deleteBranch bool
}

// commonService 将已验证的上下文组合根接给 PR 和 Release 的共享写边界。
// 沿用首次来源确认、身份与版本检查，不通过执行 Issue 命令模拟其他业务。
func (r *runtime) commonService(command *cobra.Command, writing bool) (write.Service, error) {
	existing, err := r.issueService(command, writing)
	if err != nil {
		return write.Service{}, err
	}
	return write.Service{
		Auth:     existing.Auth,
		Target:   existing.Target,
		Version:  existing.Version,
		Revision: existing.Revision,
		Locks:    existing.Locks,
	}, nil
}

// addPRCommands 接齐 PR 独立命令与共同参数，缺参由 CLI 层补全。
// 所有写入先准备和预览，再默认 No 确认；JSON／CI 绝不启动选择器。
func addPRCommands(root *cobra.Command, r *runtime) {
	group := &cobra.Command{
		Use:   "pr",
		Short: "Read, review, and explicitly modify pull requests",
	}
	filter := pullrequest.Filter{
		State: "open",
		Page:  1,
		Limit: 30,
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List PRs with bounded filters",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			base, err := r.commonService(command, false)
			if err != nil {
				return err
			}
			result, err := (pullrequest.Service{Service: base}).List(command.Context(), filter)
			if err != nil {
				return err
			}
			return r.printResult(command, result)
		},
	}
	list.Flags().StringVar(&filter.State, "state", "open", "Use open, closed, or all")
	list.Flags().StringVar(&filter.Search, "search", "", "Match a title or body substring across the bounded collection")
	list.Flags().StringVar(&filter.Base, "base", "", "Filter by exact base branch")
	list.Flags().StringVar(&filter.Author, "author", "", "Filter by API account")
	list.Flags().IntVar(&filter.Page, "page", 1, "Read this page")
	list.Flags().IntVar(&filter.Limit, "limit", 30, "Read up to 100 PRs per page")
	list.Flags().BoolVar(&filter.All, "all", false, "Read subsequent pages within the 10000-item limit")
	group.AddCommand(list)
	for _, kind := range []string{"view", "files", "commits", "comments", "reviews"} {
		commandKind := kind
		page, limit, all := 1, 30, false
		use := kind + " [NUMBER]"
		command := &cobra.Command{
			Use:   use,
			Short: "Read PR " + kind,
			Args:  cobra.MaximumNArgs(1),
			RunE: func(command *cobra.Command, args []string) error {
				base, err := r.commonService(command, false)
				if err != nil {
					return err
				}
				service := pullrequest.Service{Service: base}
				numbers, err := r.prNumbers(command, service, args, false, "all", false)
				if err != nil {
					return err
				}
				var result app.Result
				if commandKind == "view" {
					result, err = service.View(command.Context(), numbers[0])
				} else {
					result, err = service.Related(command.Context(), numbers[0], commandKind, page, limit, all)
				}
				if err != nil {
					return err
				}
				return r.printResult(command, result)
			},
		}
		if kind != "view" {
			command.Flags().IntVar(&page, "page", 1, "Read this page")
			command.Flags().IntVar(&limit, "limit", 30, "Read up to 100 items per page")
			command.Flags().BoolVar(&all, "all", false, "Read subsequent pages within the 10000-item limit")
		}
		switch kind {
		case "comments":
			command.Use = "list [NUMBER]"
			parent := &cobra.Command{
				Use:   "comment",
				Short: "Read and add ordinary PR discussion comments",
			}
			parent.AddCommand(command, newPRWrite(r, "pr.comment.add", "add [NUMBER]"))
			group.AddCommand(parent)
		case "reviews":
			command.Use = "list [NUMBER]"
			parent := &cobra.Command{
				Use:   "review",
				Short: "Read and submit PR-level reviews",
			}
			parent.AddCommand(command, newPRWrite(r, "pr.review.create", "create [NUMBER]"))
			group.AddCommand(parent)
		default:
			group.AddCommand(command)
		}
	}
	for _, verb := range []string{"create", "edit", "close", "reopen", "merge"} {
		use := verb + " [NUMBER]"
		if verb == "create" {
			use = verb
		} else if verb == "close" || verb == "reopen" {
			use = verb + " [NUMBER...]"
		}
		group.AddCommand(newPRWrite(r, "pr."+verb, use))
	}
	reviewers := &cobra.Command{
		Use:   "reviewer",
		Short: "Request explicit users or teams to review a PR",
	}
	reviewers.AddCommand(newPRWrite(r, "pr.reviewer.add", "add [NUMBER]"))
	group.AddCommand(reviewers)
	root.AddCommand(group)
}

// newPRWrite 为一个确定的写意图注册必要字段，不接收其他操作的隐藏参数。
// ReviewID 只提交已有待提交审查，合并不暴露 force 或延迟自动合并。
func newPRWrite(r *runtime, operation, use string) *cobra.Command {
	options := &prOptions{}
	args := cobra.MaximumNArgs(1)
	if operation == "pr.create" {
		args = cobra.NoArgs
	} else if operation == "pr.close" || operation == "pr.reopen" {
		args = cobra.ArbitraryArgs
	}
	command := &cobra.Command{
		Use:   use,
		Short: "Prepare, preview, and apply " + operation,
		Args:  args,
		RunE: func(command *cobra.Command, args []string) error {
			return r.runPRWrite(command, operation, args, options)
		},
	}
	switch operation {
	case "pr.create", "pr.edit":
		options.fields.register(command, operation == "pr.edit")
		command.Flags().StringVar(&options.base, "base", "", "Set the remote base branch")
		if operation == "pr.create" {
			command.Flags().StringVar(&options.head, "head", "", "Set an already pushed branch or explicit owner/repo:branch fork head")
			command.Flags().StringArrayVar(&options.users, "reviewer", nil, "Request an exact reviewer account; repeat as needed")
			command.Flags().StringArrayVar(&options.teams, "team-reviewer", nil, "Request an exact organization team; repeat as needed")
		}
	case "pr.comment.add", "pr.review.create":
		command.Flags().StringVar(&options.fields.body, "body", "", "Set the discussion or review body")
		command.Flags().StringVar(&options.fields.bodyFile, "body-file", "", "Read a UTF-8 body file up to 256 KiB")
		if operation == "pr.review.create" {
			command.Flags().StringVar(&options.event, "event", "", "Use approve, comment, or request-changes")
			command.Flags().StringVar(&options.commit, "commit", "", "Review this full remote commit SHA; default is the confirmed PR head")
			command.Flags().Int64Var(&options.reviewID, "review-id", 0, "Submit this existing pending review instead of creating another review")
		}
	case "pr.reviewer.add":
		command.Flags().StringArrayVar(&options.users, "user", nil, "Request an explicit API account; repeat as needed")
		command.Flags().StringArrayVar(&options.teams, "team", nil, "Request an explicit organization team; repeat as needed")
	case "pr.merge":
		command.Flags().StringVar(&options.method, "method", "merge", "Use an explicitly allowed merge, squash, rebase, rebase-merge, or fast-forward-only method")
		command.Flags().BoolVar(&options.deleteBranch, "delete-branch", false, "Explicitly request source branch deletion after merging")
	}
	return command
}

// runPRWrite 在 CLI 层补全缺参并展示不可变计划，随后提交一次。
// 参数完整的非交互命令仍须 --yes；创建候选不自动 push 或默选其他 fork。
func (r *runtime) runPRWrite(command *cobra.Command, operation string, args []string, options *prOptions) error {
	base, err := r.commonService(command, true)
	if err != nil {
		return err
	}
	service := pullrequest.Service{Service: base}
	common, err := options.fields.request(command, strings.Replace(operation, "pr.", "issue.", 1))
	if err != nil {
		return err
	}
	request := pullrequest.Request{
		Operation:    operation,
		Changes:      common.Changes,
		LabelRefs:    common.LabelRefs,
		MilestoneRef: common.MilestoneRef,
		Base:         options.base,
		Head:         options.head,
		Users:        options.users,
		Teams:        options.teams,
		Event:        options.event,
		Commit:       options.commit,
		ReviewID:     options.reviewID,
		Method:       options.method,
		DeleteBranch: options.deleteBranch,
	}
	if operation == "pr.create" {
		if request.Changes.Title == nil {
			title, err := r.ask(command, "PR title")
			if err != nil {
				return err
			}
			request.Changes.Title = &title
		}
		if request.Base == "" || request.Head == "" {
			if !r.interactive(command) {
				return app.Wrap(app.InvalidArgument, "Provide explicit --base and --head in non-interactive mode.", nil)
			}
			repository, err := service.Candidates(command.Context())
			if err != nil {
				return err
			}
			if request.Base == "" {
				request.Base, err = r.ask(command, "Base branch (suggested "+repository.DefaultBranch+")")
				if err != nil {
					return err
				}
			}
			head := "enter an already pushed remote branch"
			if base.Target.LocalMatches && base.Target.Local != nil && base.Target.Local.Branch != "" {
				head = base.Target.Local.Branch
			}
			if request.Head == "" {
				request.Head, err = r.ask(command, "Head branch (suggested "+head+"; owner/repo:branch for an explicit fork)")
				if err != nil {
					return err
				}
			}
		}
	} else {
		multi := operation == "pr.close" || operation == "pr.reopen"
		state := "all"
		if operation == "pr.close" || operation == "pr.merge" {
			state = "open"
		} else if operation == "pr.reopen" {
			state = "closed"
		}
		request.Numbers, err = r.prNumbers(command, service, args, multi, state, multi || operation == "pr.merge")
		if err != nil {
			return err
		}
	}
	if operation == "pr.edit" && !options.fields.changed(command) && !command.Flags().Changed("title") && !command.Flags().Changed("base") {
		if err := r.editFlags(command); err != nil {
			return err
		}
		if !options.fields.changed(command) && !command.Flags().Changed("title") && !command.Flags().Changed("base") {
			return noChanges(command)
		}
		common, err := options.fields.request(command, "issue.edit")
		if err != nil {
			return err
		}
		request.Changes = common.Changes
		request.LabelRefs = common.LabelRefs
		request.MilestoneRef = common.MilestoneRef
		request.Base = options.base
	}
	if operation == "pr.review.create" && request.Event == "" {
		request.Event, err = r.choose(command, "Review event", []tui.Choice{{
			ID:          "approve",
			Label:       "Approve",
			Description: "Approve the pull request; a review body is optional",
		}, {
			ID:          "comment",
			Label:       "Comment",
			Description: "Submit a review comment; a review body is required",
		}, {
			ID:          "request-changes",
			Label:       "Request changes",
			Description: "Ask for revisions; a review body is required",
		}})
		if err != nil {
			return err
		}
	}
	if (operation == "pr.comment.add" || (operation == "pr.review.create" && request.Event != "approve")) && request.Changes.Body == nil {
		value, err := r.ask(command, "Discussion or review body")
		if err != nil {
			return err
		}
		request.Changes.Body = &value
	}
	if operation == "pr.reviewer.add" && len(request.Users)+len(request.Teams) == 0 {
		mode, err := r.choose(command, "Reviewers · organization teams use --team", []tui.Choice{{
			ID:          "explicit",
			Label:       "Enter explicit reviewer accounts",
			Description: "Type Gitea usernames to request a review from",
		}, {
			ID:    "candidates",
			Label: "Choose repository assignees · permissions checked by Gitea",
		}})
		if err != nil {
			return err
		}
		if mode == "candidates" {
			choices, err := r.fieldCandidates(command, "user")
			if err != nil {
				return err
			}
			for _, choice := range choices {
				request.Users = append(request.Users, choice.ID)
			}
		} else {
			value, err := r.ask(command, "Reviewer accounts, separated by commas")
			if err != nil {
				return err
			}
			request.Users = splitValues(value)
		}
	}
	if !r.options.yes && !r.interactive(command) {
		return app.Wrap(app.InvalidArgument, "PR writes require complete input and --yes in non-interactive mode.", nil)
	}
	plan, err := service.Prepare(command.Context(), request)
	if err != nil {
		return err
	}
	preview, err := plan.Preview()
	if err != nil {
		return err
	}
	if err := r.businessPreview(command, preview.Context, preview); err != nil {
		return err
	}
	if err := r.confirm(command, "Apply this exact PR operation", r.options.yes); err != nil {
		return err
	}
	result, err := service.Commit(command.Context(), plan)
	if err != nil {
		return err
	}
	return r.printResult(command, result)
}

// businessPreview 将公共无敏感业务计划输出到诊断流，不污染最终 JSON。
// 输出失败即阻止提交，远端字符串经过脱敏及终端控制字符清理。
func (r *runtime) businessPreview(command *cobra.Command, target app.Context, value any) error {
	color := r.options.color
	if r.options.json || r.session != nil {
		color = "never"
	}
	renderer := output.Renderer{
		Color:      color,
		Out:        command.ErrOrStderr(),
		Diagnostic: command.ErrOrStderr(),
		Redactor:   r.options.redactor,
	}
	return renderer.Text(app.NewResult(target, []app.Item{{
		Target: "preview",
		Status: app.Success,
		Data:   value,
	}}, nil))
}

// prNumbers 校验直接编号或在交互终端明确选择稳定 PR ID。
// 状态和合并选择排除已合并 PR，其他读取可选已合并对象，默认无选择。
func (r *runtime) prNumbers(command *cobra.Command, service pullrequest.Service, args []string, multi bool, state string, unmerged bool) ([]int64, error) {
	numbers := []int64{}
	for _, argument := range args {
		number, err := strconv.ParseInt(argument, 10, 64)
		if err != nil || number < 1 {
			return nil, app.Wrap(app.InvalidArgument, "PR numbers must be positive integers.", nil)
		}
		numbers = append(numbers, number)
	}
	if len(numbers) == 0 {
		if !r.interactive(command) {
			return nil, app.Wrap(app.InvalidArgument, "Provide a PR number; selection is disabled without an interactive terminal.", nil)
		}
		choices, err := r.selectChoices(command, "Select PRs · "+service.Target.Repository, multi, func(ctx context.Context, page int, search string) (tui.Page, error) {
			result, err := service.List(ctx, pullrequest.Filter{
				State:    state,
				Search:   search,
				Page:     page,
				Limit:    30,
				Unmerged: unmerged,
			})
			if err != nil {
				return tui.Page{}, err
			}
			pageResult, ok := result.Items[0].Data.(app.Page[pullrequest.PullRequest])
			if !ok {
				return tui.Page{}, app.Wrap(app.ServerError, "The PR selector received an invalid page.", nil)
			}
			candidates := []tui.Choice{}
			for _, value := range pageResult.Items {
				candidates = append(candidates, tui.Choice{
					ID:    strconv.FormatInt(value.Number, 10),
					Label: r.options.redactor.Text(fmt.Sprintf("#%d %s %s", value.Number, value.State, value.Title)),
				})
			}
			return tui.Page{
				Choices: candidates,
				HasMore: pageResult.HasMore,
			}, nil
		})
		if err != nil {
			return nil, err
		}
		for _, choice := range choices {
			number, err := strconv.ParseInt(choice.ID, 10, 64)
			if err != nil {
				return nil, app.Wrap(app.ServerError, "The PR selector returned an invalid ID.", err)
			}
			numbers = append(numbers, number)
		}
	}
	numbers, err := issue.NormalizeNumbers(numbers)
	if err != nil {
		return nil, err
	}
	if !multi && len(numbers) != 1 {
		return nil, app.Wrap(app.InvalidArgument, "This PR operation accepts exactly one number.", nil)
	}
	return numbers, nil
}
