package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/repository"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

// issueFields 保存 CLI 的显式字段选项，存在性由 Cobra Changed 判定。
// 空字符串不承担编辑清空语义，明确 clear 选项才生成清空指针。
type issueFields struct {
	title          string
	body           string
	bodyFile       string
	assignees      []string
	labels         []string
	milestone      string
	clearBody      bool
	clearAssignees bool
	clearLabels    bool
	clearMilestone bool
}

// addIssueCommands 注册 TDD 的全部 Issue 命令及共同选择、确认路径。
// 每个写入口先补全与准备，再预览确认；不通过命令文本模拟应用服务。
func addIssueCommands(root *cobra.Command, r *runtime) {
	group := &cobra.Command{
		Use:   "issue",
		Short: "Read and explicitly modify repository issues",
	}
	addIssueList(group, r)
	view := &cobra.Command{
		Use:   "view [NUMBER]",
		Short: "Read one issue, rejecting pull request numbers",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := r.issueService(command, false)
			if err != nil {
				return err
			}
			numbers, err := r.issueNumbers(command, service, args, false, "all")
			if err != nil {
				return err
			}
			result, err := service.View(command.Context(), numbers[0])
			if err != nil {
				return err
			}
			return r.printResult(command, result)
		},
	}
	group.AddCommand(view)
	for _, operation := range []string{"create", "edit", "close", "reopen"} {
		use := operation + " [NUMBER]"
		arguments := cobra.MaximumNArgs(1)
		if operation == "create" {
			use = operation
			arguments = cobra.NoArgs
		} else if operation == "close" || operation == "reopen" {
			use = operation + " [NUMBER...]"
			arguments = cobra.ArbitraryArgs
		}
		fields := &issueFields{}
		command := &cobra.Command{
			Use:   use,
			Short: "Prepare, preview, and " + operation + " issues",
			Args:  arguments,
			RunE: func(command *cobra.Command, args []string) error {
				return r.runIssueWrite(command, "issue."+operation, args, fields, nil)
			},
		}
		if operation == "create" || operation == "edit" {
			fields.register(command, operation == "edit")
		}
		group.AddCommand(command)
	}
	for _, kind := range []string{"comment", "label", "assignee"} {
		resource := &cobra.Command{
			Use:   kind,
			Short: "Manage issue " + kind + " resources",
		}
		if kind != "assignee" {
			related := kind
			if kind == "comment" {
				related = "comments"
			} else {
				related = "labels"
			}
			page, limit, all := 1, 30, false
			list := &cobra.Command{
				Use:   "list [NUMBER]",
				Short: "Read issue " + kind + " resources",
				Args:  cobra.MaximumNArgs(1),
				RunE: func(command *cobra.Command, args []string) error {
					service, err := r.issueService(command, false)
					if err != nil {
						return err
					}
					numbers, err := r.issueNumbers(command, service, args, false, "all")
					if err != nil {
						return err
					}
					result, err := service.Related(command.Context(), numbers[0], related, page, limit, all)
					if err != nil {
						return err
					}
					return r.printResult(command, result)
				},
			}
			if kind == "comment" {
				list.Flags().IntVar(&page, "page", 1, "Read this page")
				list.Flags().IntVar(&limit, "limit", 30, "Read up to 100 comments per page")
				list.Flags().BoolVar(&all, "all", false, "Read subsequent pages within the 10000-item limit")
			}
			resource.AddCommand(list)
		}
		verbs := []string{"add", "remove"}
		if kind == "comment" {
			verbs = []string{"add"}
		}
		for _, verb := range verbs {
			fields := &issueFields{}
			var users []string
			operation := "issue." + kind + "." + verb
			command := &cobra.Command{
				Use:   verb + " [NUMBER]",
				Short: "Preview and " + verb + " issue " + kind + " values",
				Args:  cobra.MaximumNArgs(1),
				RunE: func(command *cobra.Command, args []string) error {
					return r.runIssueWrite(command, operation, args, fields, users)
				},
			}
			switch kind {
			case "comment":
				command.Flags().StringVar(&fields.body, "body", "", "Set a non-empty comment body")
				command.Flags().StringVar(&fields.bodyFile, "body-file", "", "Read the comment body from a UTF-8 file")
			case "label":
				command.Flags().StringArrayVar(&fields.labels, "label", nil, "Select an exact label name or ID; repeat for multiple labels")
			case "assignee":
				command.Flags().StringArrayVar(&users, "user", nil, "Select an API account; repeat for multiple assignees")
			}
			resource.AddCommand(command)
		}
		group.AddCommand(resource)
	}
	root.AddCommand(group)
}

// addIssueList 注册明确服务端筛选与后续页选项。
// 默认只读一页，不因为未指定编号就进入目标选择。
func addIssueList(group *cobra.Command, r *runtime) {
	filter := issue.Filter{
		State: "open",
		Page:  1,
		Limit: 30,
	}
	command := &cobra.Command{
		Use:   "list",
		Short: "List issues with explicit type and bounded filters",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			service, err := r.issueService(command, false)
			if err != nil {
				return err
			}
			result, err := service.List(command.Context(), filter)
			if err != nil {
				return err
			}
			return r.printResult(command, result)
		},
	}
	command.Flags().StringVar(&filter.State, "state", "open", "Use open, closed, or all")
	command.Flags().StringVar(&filter.Search, "search", "", "Search title and body")
	command.Flags().StringArrayVar(&filter.Labels, "label", nil, "Match any requested exact label name or ID")
	command.Flags().StringVar(&filter.Assignee, "assignee", "", "Filter by API account")
	command.Flags().StringVar(&filter.Milestone, "milestone", "", "Filter by an existing milestone title or ID")
	command.Flags().IntVar(&filter.Page, "page", 1, "Read this page")
	command.Flags().IntVar(&filter.Limit, "limit", 30, "Read up to 100 issues per page")
	command.Flags().BoolVar(&filter.All, "all", false, "Read subsequent pages within the 10000-item limit")
	group.AddCommand(command)
}

// issueService 连接共享上下文、认证、版本和写锁，未知读取可采用有界回退。
// 未登记来源先确认并验证仓库再保存映射；写入必须有精确版本证据。
func (r *runtime) issueService(command *cobra.Command, write bool) (issue.Service, error) {
	t, snapshot, authentication, err := r.resolve(command, r.input(true), true)
	if err != nil {
		return issue.Service{}, err
	}
	if !t.Registered {
		t, err = r.confirmInstance(command, t, r.options.confirmInstance)
		if err != nil {
			return issue.Service{}, err
		}
		if _, err := repository.Read(command.Context(), authentication, t, repository.Query{
			Operation: "repo.view",
			Page:      1,
			Limit:     30,
		}); err != nil {
			return issue.Service{}, err
		}
		if err := r.saveReadTarget(command, t, snapshot, authentication); err != nil {
			return issue.Service{}, err
		}
		snapshot, err = authentication.Store.Load(command.Context())
		if err != nil {
			return issue.Service{}, err
		}
		t.Instance = snapshot.Config.Instances[t.Name]
	}
	version, detectErr := r.versionInfo(command.Context(), t, snapshot, authentication, false)
	if detectErr != nil && (write || command.Context().Err() != nil) {
		return issue.Service{}, detectErr
	}
	return issue.Service{
		Auth:     authentication,
		Target:   t,
		Version:  version,
		Revision: snapshot.RevisionKey(),
		Locks:    &r.writes,
	}, nil
}

// runIssueWrite 完成必要输入、固定预览与入口确认后提交一次计划。
// --yes 不补编号或字段，非终端缺参或未确认时业务写请求数为零。
func (r *runtime) runIssueWrite(command *cobra.Command, operation string, args []string, fields *issueFields, users []string) error {
	service, err := r.issueService(command, true)
	if err != nil {
		return err
	}
	request, err := fields.request(command, operation)
	if err != nil {
		return err
	}
	request.Users = users
	if operation != "issue.create" {
		multi := operation == "issue.close" || operation == "issue.reopen"
		state := "all"
		if operation == "issue.close" {
			state = "open"
		} else if operation == "issue.reopen" {
			state = "closed"
		}
		request.Numbers, err = r.issueNumbers(command, service, args, multi, state)
		if err != nil {
			return err
		}
	}
	if operation == "issue.create" && request.Changes.Title == nil {
		value, err := r.ask(command, "Issue title")
		if err != nil {
			return err
		}
		request.Changes.Title = &value
	}
	if operation == "issue.edit" && !command.Flags().Changed("title") && !fields.changed(command) {
		field, err := r.ask(command, "Field to edit (title/body/milestone/assignees/labels)")
		if err != nil {
			return err
		}
		value, err := r.ask(command, "New value (use clear options to remove a value)")
		if err != nil {
			return err
		}
		switch field {
		case "title":
			request.Changes.Title = &value
		case "body":
			request.Changes.Body = &value
		case "milestone":
			request.MilestoneRef = value
		case "assignees":
			values := splitValues(value)
			request.Changes.Assignees = &values
		case "labels":
			request.LabelRefs = splitValues(value)
		default:
			return app.Wrap(app.InvalidArgument, "Choose one supported field to edit.", nil)
		}
	}
	if operation == "issue.comment.add" && request.Changes.Body == nil {
		value, err := r.ask(command, "Comment body")
		if err != nil {
			return err
		}
		request.Changes.Body = &value
	}
	if (operation == "issue.assignee.add" || operation == "issue.assignee.remove") && len(request.Users) == 0 {
		request.Users, err = r.collectionSelection(command, service, request.Numbers[0], operation)
	} else if (operation == "issue.label.add" || operation == "issue.label.remove") && len(request.LabelRefs) == 0 {
		request.LabelRefs, err = r.collectionSelection(command, service, request.Numbers[0], operation)
	}
	if err != nil {
		return err
	}
	if !r.options.yes && !r.interactive(command) {
		return app.Wrap(app.InvalidArgument, "Writing requires complete input and --yes in non-interactive mode.", nil)
	}
	plan, err := service.Prepare(command.Context(), request)
	if err != nil {
		return err
	}
	preview := output.Renderer{
		Out:        command.ErrOrStderr(),
		Diagnostic: command.ErrOrStderr(),
		Redactor:   r.options.redactor,
	}
	if err := preview.Text(app.NewResult(plan.Preview().Context, []app.Item{{
		Target: "preview",
		Status: app.Success,
		Data:   plan.Preview(),
	}}, nil)); err != nil {
		return err
	}
	if err := r.confirm(command, "Apply this exact Issue operation", r.options.yes); err != nil {
		return err
	}
	result, err := service.Commit(command.Context(), plan)
	if err != nil {
		return err
	}
	return r.printResult(command, result)
}

// issueNumbers 校验直接编号或显式运行稳定 ID 选择器。
// 默认无勾选，搜索与翻页通过相同应用服务读取，不用标题作为目标键。
func (r *runtime) issueNumbers(command *cobra.Command, service issue.Service, args []string, multi bool, state string) ([]int64, error) {
	numbers := []int64{}
	for _, argument := range args {
		number, err := strconv.ParseInt(argument, 10, 64)
		if err != nil || number < 1 {
			return nil, app.Wrap(app.InvalidArgument, "Issue numbers must be positive integers.", nil)
		}
		numbers = append(numbers, number)
	}
	if len(numbers) == 0 {
		if !r.interactive(command) {
			return nil, app.Wrap(app.InvalidArgument, "Provide an issue number; selection is disabled without an interactive terminal.", nil)
		}
		choices, err := tui.Select(command.Context(), command.InOrStdin(), command.ErrOrStderr(), "Select issues · "+service.Target.Repository, multi, func(ctx context.Context, page int, search string) (tui.Page, error) {
			result, err := service.List(ctx, issue.Filter{
				State:  state,
				Search: search,
				Page:   page,
				Limit:  30,
			})
			if err != nil {
				return tui.Page{}, err
			}
			values, ok := result.Items[0].Data.(app.Page[issue.Issue])
			if !ok {
				return tui.Page{}, app.Wrap(app.ServerError, "The issue query returned an unexpected page.", nil)
			}
			choices := []tui.Choice{}
			for _, value := range values.Items {
				choices = append(choices, tui.Choice{
					ID:    strconv.FormatInt(value.Number, 10),
					Label: r.options.redactor.Text(fmt.Sprintf("#%d %s %s", value.Number, value.State, value.Title)),
				})
			}
			return tui.Page{
				Choices: choices,
				HasMore: values.HasMore,
			}, nil
		})
		if err != nil {
			return nil, err
		}
		for _, choice := range choices {
			number, err := strconv.ParseInt(choice.ID, 10, 64)
			if err != nil {
				return nil, app.Wrap(app.ServerError, "The selector returned an invalid issue ID.", err)
			}
			numbers = append(numbers, number)
		}
	}
	numbers, err := issue.NormalizeNumbers(numbers)
	if err != nil {
		return nil, err
	}
	if !multi && len(numbers) != 1 {
		return nil, app.Wrap(app.InvalidArgument, "This command accepts exactly one issue.", nil)
	}
	return numbers, nil
}

// register 注册字段输入及独立清空选项，保留重复值的原始边界。
// 标题、正文、负责人、标签与里程碑均由实际 Changes 存在性驱动写入。
func (fields *issueFields) register(command *cobra.Command, edit bool) {
	command.Flags().StringVar(&fields.title, "title", "", "Set the issue title")
	command.Flags().StringVar(&fields.body, "body", "", "Set the issue body")
	command.Flags().StringVar(&fields.bodyFile, "body-file", "", "Read the body from a UTF-8 regular file (at most 256 KiB)")
	command.Flags().StringArrayVar(&fields.assignees, "assignee", nil, "Set the complete assignee collection; repeat as needed")
	command.Flags().StringArrayVar(&fields.labels, "label", nil, "Set the complete label collection using exact names or IDs")
	command.Flags().StringVar(&fields.milestone, "milestone", "", "Set an existing milestone title or ID")
	if edit {
		command.Flags().BoolVar(&fields.clearBody, "clear-body", false, "Explicitly clear the body")
		command.Flags().BoolVar(&fields.clearAssignees, "clear-assignees", false, "Explicitly clear all assignees")
		command.Flags().BoolVar(&fields.clearLabels, "clear-labels", false, "Explicitly clear all labels")
		command.Flags().BoolVar(&fields.clearMilestone, "clear-milestone", false, "Explicitly clear the milestone")
	}
}

// changed 检查是否提供了任何非标题编辑意图，false 允许交互式字段表单。
// 清空选项只有显式 true 才承担清空语义。
func (fields *issueFields) changed(command *cobra.Command) bool {
	for _, name := range []string{"body", "body-file", "assignee", "label", "milestone"} {
		if command.Flags().Changed(name) {
			return true
		}
	}
	return fields.clearBody || fields.clearAssignees || fields.clearLabels || fields.clearMilestone
}

// request 将 CLI 来源与清空规则转换为明确应用输入。
// 文件与文本互斥，编辑空正文要求 clear 选项，不发生隐式清空。
func (fields *issueFields) request(command *cobra.Command, operation string) (issue.Request, error) {
	request := issue.Request{Operation: operation}
	bodySet := command.Flags().Changed("body")
	fileSet := command.Flags().Changed("body-file")
	if (bodySet && fileSet) || (fields.clearBody && (bodySet || fileSet)) ||
		(fields.clearAssignees && command.Flags().Changed("assignee")) ||
		(fields.clearLabels && command.Flags().Changed("label")) ||
		(fields.clearMilestone && command.Flags().Changed("milestone")) {
		return request, app.Wrap(app.InvalidArgument, "Body sources and each set/clear option are mutually exclusive.", nil)
	}
	if command.Flags().Changed("title") {
		request.Changes.Title = &fields.title
	}
	if bodySet || fileSet {
		body := fields.body
		if fileSet {
			var err error
			body, err = readBodyFile(fields.bodyFile)
			if err != nil {
				return request, err
			}
		}
		if operation == "issue.edit" && body == "" {
			return request, app.Wrap(app.InvalidArgument, "Use --clear-body to clear an existing body.", nil)
		}
		request.Changes.Body = &body
	}
	if fields.clearBody {
		empty := ""
		request.Changes.Body = &empty
	}
	if command.Flags().Changed("assignee") {
		values := append([]string{}, fields.assignees...)
		request.Changes.Assignees = &values
	} else if fields.clearAssignees {
		values := []string{}
		request.Changes.Assignees = &values
	}
	request.LabelRefs = append([]string{}, fields.labels...)
	if fields.clearLabels {
		values := []int64{}
		request.Changes.Labels = &values
	}
	if command.Flags().Changed("milestone") && fields.milestone == "" {
		return request, app.Wrap(app.InvalidArgument, "Use --clear-milestone instead of an empty milestone.", nil)
	}
	request.MilestoneRef = fields.milestone
	if fields.clearMilestone {
		zero := int64(0)
		request.Changes.Milestone = &zero
	}
	return request, nil
}

// readBodyFile 有界读取普通 UTF-8 文件，禁止把截断内容当作完整正文。
// 与 API 上传共用特殊文件拒绝边界；读取或关闭失败时不会继续写入。
func readBodyFile(path string) (string, error) {
	file, err := openRegular(path)
	if err != nil {
		return "", err
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, (256<<10)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > 256<<10 || !utf8.Valid(contents) {
		return "", app.Wrap(app.InvalidArgument, "The body file must be complete UTF-8 text of at most 256 KiB.", errors.Join(readErr, closeErr))
	}
	return string(contents), nil
}

// splitValues 将交互式多值输入拆分为明确条目，不用于带逗号的标签命令行选项。
// 空项被剔除，最终集合仍由应用服务验证。
func splitValues(value string) []string {
	result := []string{}
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
