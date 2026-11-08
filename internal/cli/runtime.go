package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/session"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// runtime 保存当前入口依赖，缓存仅属于当前命令树或 MCP 进程。
// 每次业务调用重新读取配置，避免继承旧的活动账号或退出状态。
type runtime struct {
	options *options
	cache   compat.Cache
	writes  write.Locks
	session *tui.Session
	rawOut  io.Writer
	rawErr  io.Writer
}

// printedResultError 标记已经完整输出的业务失败，避免重复 envelope。
// Execute 仅使用状态码，不再将该内部标记序列化。
type printedResultError struct {
	code int
}

// Error 提供固定的内部摘要，不含业务或认证数据。
// 此错误只供顶层检测，用户结果已经由 printResult 输出。
func (e *printedResultError) Error() string {
	return "The business result has already been printed."
}

// interactive 检查三个实际终端及明确禁用条件。
// --yes 不改变缺参输入和终端判定，CI 与 JSON 从不启动提示。
func (r *runtime) interactive(command *cobra.Command) bool {
	if r.options.json || r.options.noInteractive || os.Getenv("CI") != "" {
		return false
	}
	if r.session != nil {
		return true
	}
	for _, stream := range []any{command.InOrStdin(), command.OutOrStdout(), command.ErrOrStderr()} {
		file, ok := stream.(*os.File)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return false
		}
	}
	return true
}

// ask 在诊断流展示英文提示并读取一行显式输入。
// 仅用于已检查的交互终端；输入结束、空值与取消均不猜测默认目标。
func (r *runtime) ask(command *cobra.Command, prompt string) (string, error) {
	value, err := r.askLine(command, prompt)
	value = strings.TrimSpace(value)
	if err == nil && value == "" {
		return "", app.Wrap(app.InvalidArgument, "A non-empty value is required.", nil)
	}
	return value, err
}

// askLine 原样读取可以留空的终端行，保留 Git 字面参数的首尾空白。
// 取消不等于空值；仅允许真实三流终端，所有返回路径恢复输入模式。
func (r *runtime) askLine(command *cobra.Command, prompt string) (string, error) {
	if !r.interactive(command) {
		return "", app.Wrap(app.InvalidArgument, "Required input is missing. Supply explicit options in non-interactive mode.", nil)
	}
	if r.session != nil {
		return r.session.Prompt(command.Context(), prompt, r.session.Drain(), false)
	}
	if _, err := fmt.Fprint(command.ErrOrStderr(), output.SingleLine(r.options.redactor.Text(prompt))+": "); err != nil {
		return "", app.Wrap(app.IOError, "Unable to write the input prompt.", err)
	}
	file := command.InOrStdin().(*os.File)
	state, err := term.GetState(int(file.Fd()))
	if err != nil {
		return "", app.Wrap(app.IOError, "Unable to inspect the input terminal.", err)
	}
	value, readErr := readTerminal(command.Context(), file, command.ErrOrStderr())
	restoreErr := term.Restore(int(file.Fd()), state)
	if _, err := fmt.Fprintln(command.ErrOrStderr()); err != nil {
		return "", app.Wrap(app.IOError, "Unable to finish the input prompt.", err)
	}
	if restoreErr != nil {
		return "", app.Wrap(app.IOError, "Unable to restore the input terminal.", restoreErr)
	}
	if readErr != nil {
		return "", readErr
	}
	if err := command.Context().Err(); err != nil {
		return "", app.Failure(err)
	}
	return value, nil
}

// echoTerminal 仅向显式可见输入回写已经过滤的字符。
// nil writer 表示安全隐藏输入；写失败使调用终止并交由上层恢复终端模式。
func echoTerminal(writer io.Writer, value string) error {
	if writer == nil {
		return nil
	}
	if _, err := io.WriteString(writer, value); err != nil {
		return app.Wrap(app.IOError, "Unable to display terminal input.", err)
	}
	return nil
}

// saveReadTarget 仅在用户已确认来源且仓库读取成功后保存非敏感实例映射。
// 公开读取不强制登录，也不设置默认实例或保存未经显式登录的 helper 身份。
func (r *runtime) saveReadTarget(command *cobra.Command, t target.Target, snapshot config.Snapshot, service auth.Service) error {
	if snapshot.Config.Instances == nil {
		snapshot.Config.Instances = map[string]config.Instance{}
	}
	snapshot.Config.Instances[t.Name] = t.Instance
	if t.Local != nil && t.LocalMatches {
		found := false
		for i := range snapshot.Config.Repositories {
			if snapshot.Config.Repositories[i].Path == t.Local.Root {
				snapshot.Config.Repositories[i] = config.Binding{
					Path:       t.Local.Root,
					Remote:     t.Remote,
					Instance:   t.Name,
					Repository: t.Repository,
				}
				found = true
			}
		}
		if !found {
			snapshot.Config.Repositories = append(snapshot.Config.Repositories, config.Binding{
				Path:       t.Local.Root,
				Remote:     t.Remote,
				Instance:   t.Name,
				Repository: t.Repository,
			})
		}
	}
	_, err := service.Store.Save(command.Context(), snapshot, snapshot.Config)
	return err
}

// confirm 展示具体操作并采用默认拒绝的确认。
// 普通确认允许 --yes，来源确认和共享 Git 清理使用独立显式参数。
func (r *runtime) confirm(command *cobra.Command, prompt string, explicit bool) error {
	if explicit {
		return nil
	}
	answer, err := r.askLine(command, prompt+" [y/N]")
	if err != nil {
		return err
	}
	answer = strings.TrimSpace(answer)
	if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
		return app.Wrap(app.Cancelled, "Operation cancelled.", nil)
	}
	return nil
}

// resolve 加载非敏感快照并处理交互候选，不取凭证或隐式保存默认值。
// MCP 禁止本地上下文时不会从宿主启动目录发现仓库，也不会读取终端输入。
func (r *runtime) resolve(command *cobra.Command, input target.Input, allowPrompt bool) (target.Target, config.Snapshot, auth.Service, error) {
	path, err := config.ResolvePath(r.options.configPath, nil)
	if err != nil {
		return target.Target{}, config.Snapshot{}, auth.Service{}, err
	}
	store := config.Store{Path: path}
	service := auth.Service{
		Store:    store,
		Vault:    auth.SystemVault{},
		Git:      git.Runner{},
		OnSecret: r.options.redactor.Add,
	}
	environment := session.Environment{Authentication: service, Cache: &r.cache, Locks: &r.writes, CAFile: r.options.caFile, Proxy: r.options.proxy}
	for {
		state, resolveErr := environment.Resolve(command.Context(), input)
		t, snapshot := state.Target, state.Snapshot
		if resolveErr == nil {
			if r.options.browseScope != nil {
				if err := r.options.browseScope.check(t, snapshot); err != nil {
					return target.Target{}, snapshot, service, err
				}
			}
			if r.options.onResolve != nil {
				r.options.onResolve(input)
			}
			if r.options.caFile != "" {
				t.Instance.CAPath = r.options.caFile
			}
			if r.options.proxy != "" {
				t.Instance.Proxy = r.options.proxy
			}
			return t, snapshot, service, nil
		}
		if !allowPrompt || !r.interactive(command) {
			return target.Target{}, snapshot, service, resolveErr
		}
		var choices *target.ChoiceError
		if errors.As(resolveErr, &choices) {
			items := []tui.Choice{}
			for _, name := range choices.Candidates {
				items = append(items, tui.Choice{ID: name, Label: name})
			}
			value, err := r.choose(command, "Choose "+choices.Kind, items)
			if err != nil {
				return target.Target{}, snapshot, service, err
			}
			switch choices.Kind {
			case "remote":
				input.Remote = value
			case "instance":
				input.Instance = value
			case "account":
				input.Account = value
			}
			continue
		}
		if app.Failure(resolveErr).Code != app.ContextRequired {
			return target.Target{}, snapshot, service, resolveErr
		}
		if input.Instance == "" {
			input.Instance, err = r.ask(command, "Instance name or complete HTTP(S) root URL")
		} else if input.RequireRepo && input.Repo == "" {
			input.Repo, err = r.ask(command, "Repository (owner/repo)")
		} else {
			return target.Target{}, snapshot, service, resolveErr
		}
		if err != nil {
			return target.Target{}, snapshot, service, err
		}
	}
}

// input 构造当前 CLI 参数对应的上下文输入，不将配置保存在全局变量。
// 是否必须有仓库由具体操作决定，实例诊断和认证允许非仓库目录。
func (r *runtime) input(requireRepo bool) target.Input {
	return target.Input{
		Workdir:     r.options.workdir,
		NoLocal:     r.options.noLocal,
		Instance:    r.options.instance,
		Account:     r.options.account,
		Repo:        r.options.repo,
		Remote:      r.options.remote,
		RequireRepo: requireRepo,
	}
}

// confirmInstance 对未知根地址先做无凭证检测，再要求明确来源确认。
// 检测异常不会自动认定非 Gitea；明显无效 JSON 要求修正地址，绝不读取密钥重试。
func (r *runtime) confirmInstance(command *cobra.Command, t target.Target, explicit bool) (target.Target, error) {
	if t.Registered {
		return t, nil
	}
	client, err := gitea.New(gitea.Options{
		BaseURL:  t.Instance.URL,
		CAFile:   t.Instance.CAPath,
		ProxyURL: t.Instance.Proxy,
	})
	if err != nil {
		return t, err
	}
	info, detectErr := compat.Detect(command.Context(), client, t.Instance.URL)
	client.Close()
	rawVersion, encodeErr := json.Marshal(info.Raw)
	if encodeErr != nil {
		return t, app.Wrap(app.IOError, "Unable to encode version evidence.", encodeErr)
	}
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "Instance candidate: %s\nVersion evidence: %s (%s)\n", t.Instance.URL, info.Detected, rawVersion); err != nil {
		return t, app.Wrap(app.IOError, "Unable to display instance evidence.", err)
	}
	if detectErr != nil {
		failure := app.Failure(detectErr)
		if failure.Code != app.AuthRequired && failure.Code != app.PermissionDenied && failure.Code != app.NotFound && failure.Code != app.VersionUnknown {
			return t, detectErr
		}
	}
	if err := r.confirm(command, "Trust this root URL as the Gitea instance", explicit); err != nil {
		return t, err
	}
	t.Registered = true
	return t, nil
}

// printResult 输出一次完整业务结果，并携带已输出标记避免顶层重复诊断。
// 文本与 JSON 使用相同脱敏边界，失败仍保留稳定非零退出码。
func (r *runtime) printResult(command *cobra.Command, result app.Result) error {
	render := renderer(command)
	r.options.lastResult = &result
	render.Redactor = r.options.redactor
	var err error
	if r.options.json {
		err = render.JSON(result)
	} else {
		err = render.Text(result)
	}
	if err != nil {
		return err
	}
	if code := result.ExitCode(); code != 0 {
		return &printedResultError{code: code}
	}
	return nil
}

// dataResult 将诊断数据和错误一起保留，失败不会丢失已获取的证据。
// 只有整体输出完成后，顶层才按对应结果退出。
func dataResult(t target.Target, data any, err error) app.Result {
	if identity, ok := data.(auth.Status); ok && identity.Instance != "" {
		t.Name, t.Account = identity.Instance, identity.Account
	}
	status := app.Success
	if err != nil {
		status = app.Failed
	}
	return app.NewResult(t.Context(), []app.Item{{
		Target: t.Name,
		Status: status,
		Data:   data,
	}}, err)
}

// versionInfo 复用身份边界与版本缓存，缓存键不保存 token 原文。
// 配置、账号、环境 token 或 CA 文件内容变化都会选择新键，refresh 可显式绕过缓存。
func (r *runtime) versionInfo(ctx context.Context, t target.Target, snapshot config.Snapshot, service auth.Service, refresh bool) (compat.Info, error) {
	environment := session.Environment{Authentication: service, Cache: &r.cache}
	return environment.Version(ctx, session.State{Target: t, Snapshot: snapshot, Auth: service}, refresh)
}

// secureToken 从实际终端读取不回显 token，不接受命令行明文参数。
// 保存并恢复终端状态，context 取消后不继续验证或保存读取到的密钥。
func (r *runtime) secureToken(command *cobra.Command) (string, error) {
	if !r.interactive(command) {
		return "", app.Wrap(app.InvalidArgument, "Use --with-token for stdin input or SOTEA_TOKEN in non-interactive mode.", nil)
	}
	if r.session != nil {
		secret, err := r.session.Prompt(command.Context(), "API token (input hidden)", r.session.Drain(), true)
		return strings.TrimSpace(secret), err
	}
	file := command.InOrStdin().(*os.File)
	state, err := term.GetState(int(file.Fd()))
	if err != nil {
		return "", app.Wrap(app.IOError, "Unable to inspect the input terminal.", err)
	}
	if _, err := fmt.Fprint(command.ErrOrStderr(), "API token (input hidden): "); err != nil {
		return "", app.Wrap(app.IOError, "Unable to display the token prompt.", err)
	}
	secret, err := readSecret(command.Context(), file)
	restoreErr := term.Restore(int(file.Fd()), state)
	if restoreErr != nil {
		return "", app.Wrap(app.IOError, "Unable to restore the input terminal.", restoreErr)
	}
	if _, writeErr := fmt.Fprintln(command.ErrOrStderr()); writeErr != nil {
		return "", app.Wrap(app.IOError, "Unable to finish the token prompt.", writeErr)
	}
	if command.Context().Err() != nil {
		return "", app.Failure(command.Context().Err())
	}
	if err != nil {
		return "", app.Wrap(app.IOError, "Unable to read a token from the terminal.", err)
	}
	return strings.TrimSpace(secret), nil
}

// tokenStdin 有界读取专用于 token 登录的 stdin，不回显内容。
// MCP 不调用此函数；输入超限或为空时在写入系统存储之前失败。
func tokenStdin(reader io.Reader) (string, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, 1026))
	if err != nil {
		return "", app.Wrap(app.IOError, "Unable to read the token from stdin.", err)
	}
	secret := strings.TrimSpace(string(contents))
	if secret == "" || len(contents) > 1025 || len(secret) > 1024 || strings.ContainsAny(secret, "\r\n\x00") {
		return "", app.Wrap(app.InvalidArgument, "Stdin must contain one non-empty token of at most 1024 bytes.", nil)
	}
	return secret, nil
}
