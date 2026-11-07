package cli

import (
	"context"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/release"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

type releaseOptions struct {
	title      string
	body       string
	notesFile  string
	target     string
	draft      bool
	publish    bool
	prerelease bool
	name       string
}

// addReleaseCommands 注册按标签读取、创建／编辑及单文件附件操作。
// 写命令完整参数模式和缺参交互共用同一服务，创建默认草稿不会隐式发布。
func addReleaseCommands(root *cobra.Command, r *runtime) {
	group := &cobra.Command{
		Use:   "release",
		Short: "Read, create, and explicitly publish releases",
	}
	filter := release.Filter{
		Page:  1,
		Limit: 30,
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List visible releases with bounded pagination",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			base, err := r.commonService(command, false)
			if err != nil {
				return err
			}
			result, err := (release.Service{Service: base}).List(command.Context(), filter)
			if err != nil {
				return err
			}
			return r.printResult(command, result)
		},
	}
	list.Flags().StringVar(&filter.Search, "search", "", "Match a tag or release title across the bounded collection")
	list.Flags().IntVar(&filter.Page, "page", 1, "Read this page")
	list.Flags().IntVar(&filter.Limit, "limit", 30, "Read up to 100 releases per page")
	list.Flags().BoolVar(&filter.All, "all", false, "Read subsequent pages within the 10000-item limit")
	group.AddCommand(list)
	assetGroup := &cobra.Command{
		Use:   "asset",
		Short: "List or upload a single release attachment",
	}
	for _, kind := range []string{"view", "assets"} {
		readKind := kind
		page, limit, all := 1, 30, false
		command := &cobra.Command{
			Use:   "view [TAG]",
			Short: "Read a release by its exact tag",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(command *cobra.Command, args []string) error {
				base, err := r.commonService(command, false)
				if err != nil {
					return err
				}
				service := release.Service{Service: base}
				tag, err := r.releaseTag(command, service, args)
				if err != nil {
					return err
				}
				var result app.Result
				if readKind == "view" {
					result, err = service.View(command.Context(), tag)
				} else {
					result, err = service.Assets(command.Context(), tag, page, limit, all)
				}
				if err != nil {
					return err
				}
				return r.printResult(command, result)
			},
		}
		if kind == "assets" {
			command.Use = "list [TAG]"
			command.Short = "List release attachments after resolving the exact tag"
			command.Flags().IntVar(&page, "page", 1, "Read this page")
			command.Flags().IntVar(&limit, "limit", 30, "Read up to 100 attachments per page")
			command.Flags().BoolVar(&all, "all", false, "Read subsequent pages within the 10000-item limit")
			assetGroup.AddCommand(command)
		} else {
			group.AddCommand(command)
		}
	}
	for _, operation := range []string{"create", "edit", "asset.upload"} {
		options := &releaseOptions{}
		use := operation + " [TAG]"
		args := cobra.MaximumNArgs(1)
		if operation == "asset.upload" {
			use = "upload [TAG] [FILE]"
			args = cobra.MaximumNArgs(2)
		}
		command := &cobra.Command{
			Use:   use,
			Short: "Prepare, preview, and apply release " + operation,
			Args:  args,
			RunE: func(command *cobra.Command, args []string) error {
				return r.runReleaseWrite(command, "release."+operation, args, options)
			},
		}
		if operation == "asset.upload" {
			command.Flags().StringVar(&options.name, "name", "", "Set the attachment filename; defaults to the source filename")
			assetGroup.AddCommand(command)
		} else {
			command.Flags().StringVar(&options.title, "title", "", "Set the release title")
			command.Flags().StringVar(&options.body, "body", "", "Set release notes as UTF-8 text")
			command.Flags().StringVar(&options.notesFile, "notes-file", "", "Read release notes from a UTF-8 file up to 256 KiB")
			command.Flags().StringVar(&options.target, "target", "", "Resolve this remote branch or commit; existing tags must match")
			command.Flags().BoolVar(&options.draft, "draft", false, "Explicitly keep or return the release to draft state")
			command.Flags().BoolVar(&options.publish, "publish", false, "Explicitly publish the release")
			command.Flags().BoolVar(&options.prerelease, "prerelease", false, "Explicitly set prerelease state; accepts =false")
			group.AddCommand(command)
		}
	}
	group.AddCommand(assetGroup)
	root.AddCommand(group)
}

// runReleaseWrite 补齐明确标签／字段，准备并预览后提交唯一计划。
// --draft／--publish 和正文来源互斥，--yes 不补新标签目标或上传文件。
func (r *runtime) runReleaseWrite(command *cobra.Command, operation string, args []string, options *releaseOptions) error {
	base, err := r.commonService(command, true)
	if err != nil {
		return err
	}
	service := release.Service{Service: base}
	request := release.Request{
		Operation: operation,
		Target:    options.target,
		Name:      options.name,
	}
	if len(args) > 0 {
		request.Tag = args[0]
	} else if operation == "release.create" {
		mode, chooseErr := r.choose(command, "Release tag", []tui.Choice{{
			ID:          "existing",
			Label:       "Choose an existing repository tag",
			Description: "Browse tags already present in this repository",
		}, {
			ID:    "new",
			Label: "Enter a tag explicitly · new tags require an explicit target",
		}})
		err = chooseErr
		if err == nil && mode == "existing" {
			var selected []tui.Choice
			selected, err = r.fieldCandidates(command, "tag")
			if err == nil {
				request.Tag = selected[0].ID
			}
		} else if err == nil {
			request.Tag, err = r.ask(command, "Release tag (new tags require --target)")
		}
	} else {
		request.Tag, err = r.releaseTag(command, service, nil)
	}
	if err != nil {
		return err
	}
	if operation == "release.asset.upload" {
		if len(args) > 1 {
			request.File = args[1]
		} else {
			request.File, err = r.ask(command, "Single attachment file path")
			if err != nil {
				return err
			}
		}
	} else {
		if command.Flags().Changed("draft") && command.Flags().Changed("publish") {
			return app.Wrap(app.InvalidArgument, "--draft and --publish are mutually exclusive.", nil)
		}
		if command.Flags().Changed("body") && command.Flags().Changed("notes-file") {
			return app.Wrap(app.InvalidArgument, "Use one release notes source: --body or --notes-file.", nil)
		}
		if command.Flags().Changed("title") {
			request.Title = &options.title
		}
		if command.Flags().Changed("body") {
			request.Body = &options.body
		}
		if command.Flags().Changed("notes-file") {
			value, err := readBodyFile(options.notesFile)
			if err != nil {
				return err
			}
			request.Body = &value
		}
		if command.Flags().Changed("draft") {
			request.Draft = &options.draft
		}
		if command.Flags().Changed("publish") {
			draft := !options.publish
			request.Draft = &draft
		}
		if command.Flags().Changed("prerelease") {
			request.Prerelease = &options.prerelease
		}
		if operation == "release.edit" && request.Title == nil && request.Body == nil && request.Draft == nil && request.Prerelease == nil && request.Target == "" {
			if err := r.editFlags(command); err != nil {
				return err
			}
			changed := false
			for _, name := range []string{"title", "body", "notes-file", "draft", "publish", "prerelease", "target"} {
				changed = changed || command.Flags().Changed(name)
			}
			if !changed {
				return noChanges(command)
			}
			// 字段容器已由真实 flag 更新；复用同一请求构造路径并保留已选标签。
			return r.runReleaseWrite(command, operation, []string{request.Tag}, options)
		}

	}
	if !r.options.yes && !r.interactive(command) {
		return app.Wrap(app.InvalidArgument, "Release writes require complete input and --yes in non-interactive mode.", nil)
	}
	plan, err := service.Prepare(command.Context(), request)
	if err != nil && operation == "release.create" && request.Target == "" && r.interactive(command) && app.Failure(err).Code == app.InvalidArgument {
		request.Target, err = r.ask(command, "Explicit target branch or commit for the new tag")
		if err != nil {
			return err
		}
		plan, err = service.Prepare(command.Context(), request)
	}
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
	if err := r.confirm(command, "Apply this exact release operation", r.options.yes); err != nil {
		return err
	}
	result, err := service.Commit(command.Context(), plan)
	if err != nil {
		return err
	}
	return r.printResult(command, result)
}

// releaseTag 将完整标签参数原样交给服务，缺参才启动稳定 Release ID 单选。
// 搜索覆盖完整有界集合，不默认选第一项，JSON／CI／无终端立即报参数错误。
func (r *runtime) releaseTag(command *cobra.Command, service release.Service, args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if !r.interactive(command) {
		return "", app.Wrap(app.InvalidArgument, "Provide an exact release tag; selection requires an interactive terminal.", nil)
	}
	selected, err := r.selectChoices(command, "Select release · "+service.Target.Repository, false, func(ctx context.Context, page int, search string) (tui.Page, error) {
		result, err := service.List(ctx, release.Filter{
			Search: search,
			Page:   page,
			Limit:  30,
		})
		if err != nil {
			return tui.Page{}, err
		}
		found, ok := result.Items[0].Data.(app.Page[release.Release])
		if !ok {
			return tui.Page{}, app.Wrap(app.ServerError, "The release selector received an invalid page.", nil)
		}
		choices := []tui.Choice{}
		for _, value := range found.Items {
			choice := tui.Choice{
				ID:          value.Tag,
				Label:       r.options.redactor.Text(value.Tag),
				Description: r.options.redactor.Text(value.Title),
			}
			if value.Draft {
				choice.Badge = "◇ Draft"
			}
			choices = append(choices, choice)
		}
		return tui.Page{
			Choices: choices,
			HasMore: found.HasMore,
		}, nil
	})
	if err != nil {
		return "", err
	}
	return selected[0].ID, nil
}
