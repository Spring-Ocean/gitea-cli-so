// Package mcp 将共享应用服务暴露为本地 stdio 工具并限制写入、文件与进程资源。
// 默认只读，实际输入／结果由严格 Schema 和共同脱敏边界管理，不读取终端交互。
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/session"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/fileinput"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options 是当前 stdio 进程的显式权限、目标和有限资源配置。
// 零值采用只读默认；固定绝对根与客户端工作区跟随互斥，不能用工具参数扩大授权。
type Options struct {
	Defaults         target.Input
	AllowWrite       bool
	AllowRawAPI      bool
	FollowWorkspace  bool
	WorkspaceSource  string
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	UploadTimeout    time.Duration
	MaxOutput        int
	MaxFileBytes     int64
	MaxSnapshotBytes int64
}

// Host 管理协议服务器、进程内计划和授权文件根。
// 使用者调用 Run 或在 Connect 测试结束后 Close；Server 不接受任意终端输入。
type Host struct {
	Server           *sdk.Server
	environment      *session.Environment
	options          Options
	redactor         *output.Redactor
	scope            *fileinput.Scope
	plans            *planRegistry
	reads            chan struct{}
	writes           chan struct{}
	queued           chan struct{}
	mu               sync.Mutex
	closing          bool
	active           sync.WaitGroup
	stop             context.Context
	cancel           context.CancelFunc
	workspaceMu      sync.Mutex
	workspaces       map[*sdk.ServerSession]*workspaceAccess
	workspaceScopes  []*fileinput.Scope
	workspaceBinding string
	workspaceError   error
	launchWorkspace  string
}

// metadata 只保存 MCP 专属、已定义形状的附加信息。
// 认证材料不可存入此类型，API 远端数据必须先通过公共脱敏器。
type metadata struct {
	Plan       *planMetadata       `json:"plan,omitempty"`
	Execution  *executionMetadata  `json:"execution,omitempty"`
	Truncation *truncationMetadata `json:"truncation,omitempty"`
	API        json.RawMessage     `json:"api,omitempty"`
}

// executionMetadata 记录实际发送边界，不能被解释为服务端提交成功。
// 业务逐项状态和 unknown 仍由共享应用结果完整保留。
type executionMetadata struct {
	WriteAttempted string `json:"write_attempted"`
}

// truncationMetadata 明确省略字段和完整读取方式。
// 执行目标及步骤状态不得作为可裁剪数据。
type truncationMetadata struct {
	Truncated     bool     `json:"truncated"`
	OmittedFields []string `json:"omitted_fields"`
	Reason        string   `json:"reason"`
	NextAction    string   `json:"next_action"`
}

// response 组合共同 envelope 与 MCP 专属元数据，不改变 CLI JSON。
// 在进入协议及缓存前完成脱敏和输出预算核验。
type response struct {
	app.Result
	Meta *metadata `json:"meta,omitempty"`
}

// NewHost 创建完整 MCP 组合入口，初始化时不访问实例或用户凭证。
// environment 必须提供配置服务；跟随模式在工具调用时读取客户端根，成功后由 Run／Close 释放所有根句柄。
func NewHost(version string, environment *session.Environment, options Options, redactor *output.Redactor) (*Host, error) {
	if environment == nil {
		return nil, app.Wrap(app.ConfigError, "MCP requires shared application services.", nil)
	}
	if options.FollowWorkspace && options.Defaults.Workdir != "" {
		return nil, app.Wrap(app.InvalidArgument, "Choose either --follow-workspace or --workdir, not both.", nil)
	}
	if options.WorkspaceSource != "" && (!options.FollowWorkspace || (options.WorkspaceSource != "codex" && options.WorkspaceSource != "claude")) {
		return nil, app.Wrap(app.InvalidArgument, "--workspace-source requires --follow-workspace and must be codex or claude.", nil)
	}
	if options.ReadTimeout == 0 {
		options.ReadTimeout = 30 * time.Second
	}
	if options.WriteTimeout == 0 {
		options.WriteTimeout = 60 * time.Second
	}
	if options.UploadTimeout == 0 {
		options.UploadTimeout = 300 * time.Second
	}
	if options.MaxOutput == 0 {
		options.MaxOutput = 256 << 10
	}
	if options.MaxFileBytes == 0 {
		options.MaxFileBytes = 64 << 20
	}
	if options.MaxSnapshotBytes == 0 {
		options.MaxSnapshotBytes = 256 << 20
	}
	if options.ReadTimeout <= 0 || options.WriteTimeout <= 0 || options.UploadTimeout <= 0 || options.MaxOutput < 4096 || options.MaxOutput > 256<<10 || options.MaxFileBytes <= 0 || options.MaxSnapshotBytes <= 0 {
		return nil, app.Wrap(app.InvalidArgument, "Specify positive timeouts, file budgets, and an output limit between 4096 and 262144 bytes.", nil)
	}
	if environment.Cache == nil {
		environment.Cache = &compat.Cache{}
	}
	if environment.Locks == nil {
		environment.Locks = &write.Locks{}
	}
	host := &Host{
		environment: environment,
		options:     options,
		redactor:    redactor,
		reads:       make(chan struct{}, 4),
		writes:      make(chan struct{}, 4),
		queued:      make(chan struct{}, 64),
		workspaces:  make(map[*sdk.ServerSession]*workspaceAccess),
	}
	host.launchWorkspace = launchWorkspace(options.WorkspaceSource)
	if options.Defaults.Workdir != "" {
		scope, err := fileinput.NewScope(options.Defaults.Workdir, options.MaxFileBytes, options.MaxSnapshotBytes)
		if err != nil {
			return nil, err
		}
		host.scope = scope
		host.options.Defaults.Workdir = scope.Base
	}
	host.stop, host.cancel = context.WithCancel(context.Background())
	host.plans = newPlanRegistry()
	host.Server = sdk.NewServer(&sdk.Implementation{
		Name:    "sotea",
		Version: version,
	}, &sdk.ServerOptions{
		Instructions:              serverInstructions,
		SupportedProtocolVersions: []string{"2025-11-25", "2025-06-18"},
		RootsListChangedHandler: func(ctx context.Context, request *sdk.RootsListChangedRequest) {
			host.invalidateWorkspace(request.Session)
		},
	})
	host.registerReads()
	if options.AllowRawAPI {
		host.register("api_read", "Call a bounded GET, HEAD, or OPTIONS API request. Custom endpoint effects depend on the server.", apiSchema(true), false, false, (*Host).apiRead)
	}
	if options.AllowWrite {
		host.register("write_prepare", prepareDescription, prepareSchema(options.AllowRawAPI), true, false, (*Host).prepare)
		host.register("write_commit", "Commit the original immutable plan once. Repeated calls return its existing state or result; never resend unknown outcomes.", objectSchema(schema{
			"plan_id": textSchema(true), "confirmation_token": textSchema(true),
		}, "plan_id", "confirmation_token"), true, false, (*Host).commit)
		status := objectSchema(schema{"plan_id": textSchema(true), "request_id": textSchema(true)})
		status["oneOf"] = []schema{{"required": []string{"plan_id"}}, {"required": []string{"request_id"}}}
		host.register("write_status", "Inspect a process-local plan without executing it or requiring the remote server to be online.", status, true, true, (*Host).status)
	}
	return host, nil
}

// register 为单一工具连接 SDK 严格校验和共同取消／输出边界。
// input 不可在注册后修改；handler 收到独立会话工作区视图，业务失败返回一致结构化／文本结果。
func (h *Host) register(name, description string, input schema, write, status bool, handler func(*Host, context.Context, map[string]json.RawMessage) response) {
	describeInputFields(input["properties"].(schema))
	sdk.AddTool(h.Server, &sdk.Tool{
		Name:         "sotea_" + name,
		Description:  description,
		InputSchema:  input,
		OutputSchema: resultSchema(),
		Annotations:  &sdk.ToolAnnotations{ReadOnlyHint: status || (!write && name != "api_read")},
	}, func(ctx context.Context, request *sdk.CallToolRequest, arguments map[string]json.RawMessage) (*sdk.CallToolResult, map[string]any, error) {
		h.mu.Lock()
		if h.closing {
			h.mu.Unlock()
			content, value := h.reply(failed(app.Wrap(app.Cancelled, "The MCP service is shutting down.", nil)), name)
			return content, value, nil
		}
		h.active.Add(1)
		h.mu.Unlock()
		defer h.active.Done()
		requestContext, stopRequest := context.WithCancel(ctx)
		stopHook := context.AfterFunc(h.stop, stopRequest)
		defer stopHook()
		defer stopRequest()
		ctx = requestContext
		timeout := h.options.ReadTimeout
		slots := h.reads
		if name == "write_commit" {
			timeout = h.commitTimeout(arguments)
			slots = h.writes
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if !status {
			release, err := h.acquire(ctx, slots)
			if err != nil {
				content, value := h.reply(failed(err), name)
				return content, value, nil
			}
			defer release()
		}
		view := h.workspaceView(ctx, request.Session)
		result := handler(view, ctx, arguments)
		content, value := h.reply(result, name)
		return content, value, nil
	})
}

// acquire 在共享执行槽前使用有限等待队列，队列时间计入 context 超时。
// 调用者只获取一次；取消或满队列不进入应用服务。
func (h *Host) acquire(ctx context.Context, slots chan struct{}) (func(), error) {
	select {
	case slots <- struct{}{}:
		return func() {
			<-slots
		}, nil
	default:
	}
	select {
	case h.queued <- struct{}{}:
	default:
		return nil, app.Wrap(app.ResourceLimit, "The MCP request queue is full. Try again after active calls finish.", nil)
	}
	defer func() {
		<-h.queued
	}()
	select {
	case slots <- struct{}{}:
		return func() {
			<-slots
		}, nil
	case <-ctx.Done():
		return nil, app.Failure(ctx.Err())
	}
}

// Run 运行仅承载协议的传输并在 EOF／取消后释放进程资源。
// 客户端拥有子进程；本方法不读取登录输入或要求其他 sotea 进程存在。
func (h *Host) Run(ctx context.Context, transport sdk.Transport) error {
	runErr := h.Server.Run(ctx, transport)
	return errors.Join(runErr, h.Close())
}

// Close 停止接纳调用并最多等待十秒清理活动请求与授权文件根。
// 调用者须先取消传输；资源关闭错误不会被静默吞掉，不承诺崩溃后恢复计划。
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return nil
	}
	h.closing = true
	h.cancel()
	h.mu.Unlock()
	done := make(chan struct{})
	go func() {
		h.active.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		return app.Wrap(app.IOError, "Timed out waiting for MCP requests to stop; remote writes may require inspection.", nil)
	}
	var closeErr error
	if h.scope != nil {
		closeErr = h.scope.Close()
	}
	for _, scope := range h.workspaceScopes {
		closeErr = errors.Join(closeErr, scope.Close())
	}
	return closeErr
}

// failed 创建可解析的业务错误，保留实际错误类别。
// 协议语法错误由 SDK 处理，此帮助函数不关闭服务器。
func failed(err error) response {
	return response{Result: app.NewResult(app.Context{}, nil, err)}
}

// dataResponse 将单一应用数据包装成共同结果。
// 失败仍保留可用数据和上下文，不伪造成功状态。
func dataResponse(state session.State, value any, err error) response {
	status := app.Success
	if err != nil {
		status = app.Failed
	}
	return response{Result: app.NewResult(state.Target.Context(), []app.Item{{
		Target: state.Target.Repository,
		Status: status,
		Data:   value,
	}}, err)}
}

// decode 将 SDK 已通过 Schema 的参数解码成适配层类型。
// 类型化 int64／指针保留编号精度和字段存在性，错误作为参数错误返回。
func decode(arguments map[string]json.RawMessage, destination any) error {
	encoded, err := json.Marshal(arguments)
	if err == nil {
		err = json.Unmarshal(encoded, destination)
	}
	if err != nil {
		return app.Wrap(app.InvalidArgument, "Unable to decode the tool arguments.", err)
	}
	return nil
}

// safeResult 使用共同 JSON 输出边界脱敏业务内容并保留数字精度。
// 生成的计划凭据不属于 API 凭证，不经过按值替换而破坏后续确认。
func (h *Host) safeResult(result app.Result) (map[string]any, error) {
	var buffer bytes.Buffer
	render := output.Renderer{
		Out:      &buffer,
		Redactor: h.redactor,
	}
	if err := render.JSON(result); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(&buffer)
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, app.Wrap(app.IOError, "Unable to encode the structured tool result.", err)
	}
	return value, nil
}
