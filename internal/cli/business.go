package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/repository"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/session"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
	protocol "github.com/Spring-Ocean/gitea-cli-so/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// addBusinessCommands 注册已实现的认证、仓库、Issue、PR、Release 与诊断入口。
// 所有入口共用 runtime 与应用服务，不创建未来业务命令的空实现。
func addBusinessCommands(root *cobra.Command, r *runtime, version string) {
	addAuthCommands(root, r)
	addReadCommands(root, r)
	addIssueCommands(root, r)
	addPRCommands(root, r)
	addReleaseCommands(root, r)
	addAPICommand(root, r)
	instance := &cobra.Command{
		Use:   "instance",
		Short: "Inspect Gitea instance evidence",
	}
	refresh := false
	info := &cobra.Command{
		Use:   "info",
		Short: "Inspect server version without claiming full compatibility",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			t, snapshot, service, err := r.resolve(command, r.input(false), true)
			if err != nil {
				return err
			}
			info, err := r.versionInfo(command.Context(), t, snapshot, service, refresh)
			return r.printResult(command, dataResult(t, info, err))
		},
	}
	info.Flags().BoolVar(&refresh, "refresh", false, "Refresh the process-local version cache")
	instance.AddCommand(info)
	root.AddCommand(instance)
	addMCPCommand(root, r, version)
}

// addAuthCommands 连接五类显式账号生命周期操作及来源确认。
// 密钥只来自安全终端、专用 stdin、环境或已确认 helper，不接受命令行 token。
func addAuthCommands(root *cobra.Command, r *runtime) {
	group := &cobra.Command{
		Use:   "auth",
		Short: "Manage verified Gitea identities",
	}
	for _, operation := range []string{"login", "refresh"} {
		var source, kind, name string
		var withToken bool
		command := &cobra.Command{
			Use:   operation,
			Short: "Verify credentials before saving the account",
			Args:  cobra.NoArgs,
			RunE: func(command *cobra.Command, args []string) error {
				t, snapshot, service, err := r.resolve(command, r.input(false), true)
				if err != nil {
					return err
				}
				t, err = r.confirmInstance(command, t, r.options.confirmInstance)
				if err != nil {
					return err
				}
				selected := source
				if withToken {
					if selected != "" && selected != "token" {
						return app.Wrap(app.InvalidArgument, "--with-token requires credential source token.", nil)
					}
					selected = "token"
				}
				if selected == "" && os.Getenv("SOTEA_TOKEN") != "" {
					selected = "environment"
				}
				if selected == "" && operation == "refresh" {
					selected = t.Instance.Accounts[t.Account].Source
				}
				if selected == "" {
					selected, err = r.ask(command, "Credential source (git/token/environment)")
					if err != nil {
						return err
					}
				}
				secret := ""
				if selected == "token" {
					if withToken {
						secret, err = tokenStdin(command.InOrStdin())
					} else {
						secret, err = r.secureToken(command)
					}
					if err != nil {
						return err
					}
				}
				if operation == "refresh" {
					if _, exists := t.Instance.Accounts[t.Account]; !exists {
						return app.Wrap(app.InvalidArgument, "Select an existing saved account to refresh.", nil)
					}
				}
				status, err := service.Login(command.Context(), t, snapshot, auth.LoginOptions{
					Source:   selected,
					AuthType: kind,
					Secret:   secret,
					SaveName: name,
				})
				return r.printResult(command, dataResult(t, status, err))
			},
		}
		command.Flags().StringVar(&source, "credential-source", "", "Choose git, token, or environment")
		command.Flags().StringVar(&kind, "auth-type", "", "Use basic or token for Git credentials (saved type, then basic by default)")
		command.Flags().BoolVar(&withToken, "with-token", false, "Read one token from stdin without echoing it")
		command.Flags().StringVar(&name, "name", "", "Save a new instance under this name")
		group.AddCommand(command)
	}
	check := false
	status := &cobra.Command{
		Use:   "status",
		Short: "Show non-sensitive credential source and identity state",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			t, snapshot, service, err := r.resolve(command, r.input(false), true)
			if err != nil {
				return err
			}
			value := service.Status(t)
			if check {
				value, err = service.Check(command.Context(), t, snapshot)
			}
			return r.printResult(command, dataResult(t, value, err))
		},
	}
	status.Flags().BoolVar(&check, "check", false, "Verify the current API identity with GET /user")
	group.AddCommand(status)
	group.AddCommand(&cobra.Command{
		Use:   "switch [ACCOUNT]",
		Short: "Verify and activate a saved account",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			input := r.input(false)
			if len(args) == 1 {
				if input.Account != "" && input.Account != args[0] {
					return app.Wrap(app.InvalidArgument, "Account argument conflicts with --account.", nil)
				}
				input.Account = args[0]
			}
			t, snapshot, service, err := r.resolve(command, input, true)
			if err != nil {
				return err
			}
			if input.Account == "" && os.Getenv("SOTEA_ACCOUNT") == "" {
				if !r.interactive(command) {
					return app.Wrap(app.InvalidArgument, "Specify an account to switch to.", nil)
				}
				names := []string{}
				for name := range t.Instance.Accounts {
					names = append(names, name)
				}
				sort.Strings(names)
				if _, err := fmt.Fprintln(command.ErrOrStderr(), "Saved accounts: "+strings.Join(names, ", ")); err != nil {
					return app.Wrap(app.IOError, "Unable to display account candidates.", err)
				}
				t.Account, err = r.ask(command, "Account to activate")
				if err != nil {
					return err
				}
			}
			value, err := service.Switch(command.Context(), t, snapshot)
			return r.printResult(command, dataResult(t, value, err))
		},
	})
	forget, confirmForget := false, false
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Suppress automatic sign-in and remove sotea credentials",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			t, snapshot, service, err := r.resolve(command, r.input(false), true)
			if err != nil {
				return err
			}
			if t.Account == "" {
				return app.Wrap(app.InvalidArgument, "Specify --account for logout.", nil)
			}
			if err := r.confirm(command, "Sign out account "+t.Account+" from "+t.Name, r.options.yes); err != nil {
				return err
			}
			if forget {
				query, directory, err := service.ForgetScope(t)
				if err != nil {
					return err
				}
				scope, err := json.Marshal(query)
				if err != nil {
					return app.Wrap(app.IOError, "Unable to describe the Git credential scope.", err)
				}
				if _, err := fmt.Fprintf(command.ErrOrStderr(), "Git credential scope: %s\nAn empty Path may affect other repositories and tools on this host.\n", scope); err != nil {
					return app.Wrap(app.IOError, "Unable to display the Git credential scope.", err)
				}
				if err := r.confirm(command, "Reject this shared Git credential scope", confirmForget); err != nil {
					return err
				}
				// 先退出 sotea，reject 失败也不能使下个进程隐式重登。
				value, err := service.Logout(command.Context(), t, snapshot)
				if err == nil {
					err = service.ForgetGit(command.Context(), query, directory)
				}
				return r.printResult(command, dataResult(t, value, err))
			}
			value, err := service.Logout(command.Context(), t, snapshot)
			return r.printResult(command, dataResult(t, value, err))
		},
	}
	logout.Flags().BoolVar(&forget, "forget-git", false, "Also reject the saved shared Git helper credential after separate confirmation")
	logout.Flags().BoolVar(&confirmForget, "confirm-forget-git", false, "Explicitly confirm rejecting the displayed shared Git credential scope")
	group.AddCommand(logout)
	root.AddCommand(group)
}

// addReadCommands 为全部 P2 仓库上下文操作提供一致分页与业务输出。
// commit status 只在本地映射匹配目标时使用当前明确 HEAD，不把其他仓库 HEAD 当默认值。
func addReadCommands(root *cobra.Command, r *runtime) {
	groups := map[string]*cobra.Command{}
	specs := []struct {
		group     string
		verb      string
		operation string
	}{
		{"repo", "view", "repo.view"},
		{"branch", "list", "branch.list"},
		{"tag", "list", "tag.list"},
		{"commit", "list", "commit.list"},
		{"commit", "status", "commit.status"},
		{"label", "list", "label.list"},
		{"milestone", "list", "milestone.list"},
	}
	for _, spec := range specs {
		group := groups[spec.group]
		if group == nil {
			group = &cobra.Command{
				Use:   spec.group,
				Short: "Read repository " + spec.group + " information",
			}
			groups[spec.group] = group
			root.AddCommand(group)
		}
		query := repository.Query{
			Operation: spec.operation,
			Page:      1,
			Limit:     30,
		}
		use := spec.verb
		arguments := cobra.NoArgs
		if spec.operation == "commit.status" {
			use += " [REF]"
			arguments = cobra.MaximumNArgs(1)
		}
		command := &cobra.Command{
			Use:   use,
			Short: "Read " + spec.operation + " from the selected repository",
			Args:  arguments,
			RunE: func(command *cobra.Command, args []string) error {
				t, snapshot, service, err := r.resolve(command, r.input(true), true)
				if err != nil {
					return err
				}
				firstUse := !t.Registered
				if firstUse {
					t, err = r.confirmInstance(command, t, r.options.confirmInstance)
					if err != nil {
						return err
					}
				}
				if query.Operation == "commit.status" {
					query.Ref = ""
					if len(args) == 1 {
						query.Ref = args[0]
					} else if t.LocalMatches && t.Local != nil {
						query.Ref = t.Local.Head
					}
					if query.Ref == "" && r.interactive(command) {
						query.Ref, err = r.ask(command, "Commit ref or SHA")
						if err != nil {
							return err
						}
					}
				}
				result, err := repository.Read(command.Context(), service, t, query)
				if err != nil {
					return err
				}
				if firstUse {
					if err := r.saveReadTarget(command, t, snapshot, service); err != nil {
						return err
					}
				}
				return r.printResult(command, result)
			},
		}
		if strings.HasSuffix(spec.operation, ".list") {
			command.Flags().IntVar(&query.Page, "page", 1, "Read this page (starting at 1)")
			command.Flags().IntVar(&query.Limit, "limit", 30, "Read up to 100 items per page")
			command.Flags().BoolVar(&query.All, "all", false, "Read subsequent pages, bounded at 10000 items")
		}
		group.AddCommand(command)
	}
}

// addMCPCommand 注册完整 stdio 服务，客户端管理此进程及独立权限开关。
// 初始化不联网，MCP 调用直接进入共享环境与应用服务，不创建临时 Cobra 命令。
func addMCPCommand(root *cobra.Command, r *runtime, version string) {
	options := protocol.Options{}
	group := &cobra.Command{Use: "mcp", Short: "Run the local MCP tools over stdio"}
	serve := &cobra.Command{
		Use:         "serve",
		Short:       "Serve Gitea tools over stdio; read-only by default",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"sotea.raw": "true"},
		RunE: func(command *cobra.Command, args []string) error {
			if r.options.json {
				return app.Wrap(app.InvalidArgument, "MCP stdout is reserved for the protocol; do not use --json.", nil)
			}
			path, err := config.ResolvePath(r.options.configPath, nil)
			if err != nil {
				return err
			}
			environment := &session.Environment{
				Authentication: auth.Service{Store: config.Store{Path: path}, Vault: auth.SystemVault{}, Git: git.Runner{}, OnSecret: r.options.redactor.Add},
				Cache:          &r.cache, Locks: &r.writes, CAFile: r.options.caFile, Proxy: r.options.proxy,
			}
			options.Defaults = r.input(false)
			host, err := protocol.NewHost(version, environment, options, r.options.redactor)
			if err != nil {
				return err
			}
			return host.Run(command.Context(), &sdk.StdioTransport{MaxLineLength: 256 << 10})
		},
	}
	serve.Flags().BoolVar(&options.AllowWrite, "allow-write", false, "Expose exact prepare, commit and status tools; approval remains the client's responsibility")
	serve.Flags().BoolVar(&options.AllowRawAPI, "allow-raw-api", false, "Expose bounded raw API access; API writes also require --allow-write")
	serve.Flags().BoolVar(&options.FollowWorkspace, "follow-workspace", false, "Authorize each client's project through MCP roots or an explicitly selected client launch adapter")
	serve.Flags().StringVar(&options.WorkspaceSource, "workspace-source", "", "Use a verified client launch adapter with --follow-workspace: codex or claude; otherwise require MCP roots")
	serve.Flags().DurationVar(&options.ReadTimeout, "read-timeout", 30*time.Second, "Bound reads and preparation, including queue time")
	serve.Flags().DurationVar(&options.WriteTimeout, "write-timeout", 60*time.Second, "Bound ordinary write execution, including lock and queue time")
	serve.Flags().DurationVar(&options.UploadTimeout, "upload-timeout", 300*time.Second, "Bound uploads and prepared API execution")
	serve.Flags().IntVar(&options.MaxOutput, "max-output-bytes", 256<<10, "Bound the complete serialized tool result, including its JSON text fallback")
	serve.Flags().Int64Var(&options.MaxFileBytes, "max-file-bytes", 64<<20, "Bound total source-file bytes per request")
	serve.Flags().Int64Var(&options.MaxSnapshotBytes, "max-snapshot-bytes", 256<<20, "Bound concurrent private file snapshots")
	group.AddCommand(serve)
	root.AddCommand(group)
}
