// Package cli 适配 Cobra 命令、公共选项与进程退出状态。
// 业务服务由组合根注入，Git 原始流不会经过业务 JSON 渲染。
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
)

// options 只保存当前已实现基础能力需要的选项。
// 已接入上下文、认证与只读链路，后续写入和样式选项随实际能力加入。
type options struct {
	redactor        *output.Redactor
	instance        string
	account         string
	repo            string
	remote          string
	configPath      string
	caFile          string
	proxy           string
	yes             bool
	confirmInstance bool
	json            bool
	workdir         string
	noInteractive   bool
}

// NewRootCommand 创建相互独立的命令树，不访问配置或凭证。
// version 注入构建版本；调用者可设置 I/O 与 context 后执行，未实现入口不会被注册。
func NewRootCommand(version string) *cobra.Command {
	opts := &options{redactor: output.NewRedactor(os.Getenv("SOTEA_TOKEN"))}
	root := &cobra.Command{
		Use:              "sotea",
		Short:            "CLI for self-hosted Gitea",
		SilenceErrors:    true,
		SilenceUsage:     true,
		TraverseChildren: true,
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) != 0 {
				return app.Wrap(app.InvalidArgument, "unknown command. Run sotea --help for available commands.", nil)
			}
			if opts.json {
				return app.Wrap(app.InvalidArgument, "Specify a command when using --json.", nil)
			}
			return command.Help()
		},
	}
	root.PersistentFlags().BoolVar(&opts.json, "json", false, "Print a structured JSON result")
	root.PersistentFlags().StringVar(&opts.workdir, "workdir", "", "Run in the specified working directory")
	root.PersistentFlags().BoolVar(&opts.noInteractive, "no-interactive", false, "Disable sotea prompts; Git retains its own interaction rules")
	root.PersistentFlags().StringVar(&opts.instance, "instance", "", "Select a registered instance or HTTP(S) root URL")
	root.PersistentFlags().StringVar(&opts.account, "account", "", "Select an explicit API account")
	root.PersistentFlags().StringVar(&opts.repo, "repo", "", "Select owner/repo explicitly")
	root.PersistentFlags().StringVar(&opts.remote, "remote", "", "Select a Git fetch remote")
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "Use a specific non-sensitive JSON configuration file")
	root.PersistentFlags().StringVar(&opts.caFile, "ca-file", "", "Add a private CA PEM file to system trust")
	root.PersistentFlags().StringVar(&opts.proxy, "proxy", "", "Use an HTTP(S) proxy URL, or direct")
	root.PersistentFlags().BoolVar(&opts.yes, "yes", false, "Skip the ordinary operation confirmation")
	root.PersistentFlags().BoolVar(&opts.confirmInstance, "confirm-instance", false, "Explicitly trust the displayed root URL after anonymous instance detection")
	root.SetFlagErrorFunc(func(command *cobra.Command, err error) error {
		return app.Wrap(app.InvalidArgument, "Invalid command options. Run the command with --help.", err)
	})
	root.PersistentPreRunE = func(command *cobra.Command, args []string) error {
		if err := command.Context().Err(); err != nil {
			return app.Failure(err)
		}
		if opts.workdir != "" {
			info, err := os.Stat(opts.workdir)
			if err != nil || !info.IsDir() {
				return app.Wrap(app.InvalidArgument, "The working directory does not exist or is not a directory.", err)
			}
		}
		return nil
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			if opts.json {
				result := app.NewResult(app.Context{}, []app.Item{{
					Target: "sotea",
					Status: app.Success,
					Data:   map[string]string{"version": version},
				}}, nil)
				return renderer(command).JSON(result)
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "sotea %s\n", version); err != nil {
				return app.Wrap(app.IOError, "Unable to write the version.", err)
			}
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use:                "git -- <args>",
		Short:              "Run system Git with unchanged arguments, streams, and exit status",
		DisableFlagParsing: true,
		RunE: func(command *cobra.Command, args []string) error {
			if opts.json {
				return app.Wrap(app.InvalidArgument, "--json is not supported for Git passthrough.", nil)
			}
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			runner := git.Runner{}
			return runner.Passthrough(command.Context(), opts.workdir, args,
				command.InOrStdin(), command.OutOrStdout(), command.ErrOrStderr())
		},
	})
	addBusinessCommands(root, opts, version)
	return root
}
