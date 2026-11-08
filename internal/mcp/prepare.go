package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/catalog"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/pullrequest"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/release"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/repository"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/session"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
)

// prepareInput 保留显式 action、公共上下文和该动作的独立字段。
// Params 只在 SDK 已校验对应 oneOf 分支后交给类型化适配器。
type prepareInput struct {
	RequestID string          `json:"request_id"`
	Action    string          `json:"action"`
	Context   readInput       `json:"context"`
	Params    json.RawMessage `json:"params"`
}

// changeInput 是已确认的公共编辑字段，不暴露应用内部别名。
// 指针区分未提供、空正文、空集合和 false，null 已由 Schema 拒绝。
type changeInput struct {
	Title      *string   `json:"title"`
	Body       *string   `json:"body"`
	Assignees  *[]string `json:"assignees"`
	Labels     *[]int64  `json:"label_ids"`
	Milestone  *int64    `json:"milestone_id"`
	Base       string    `json:"base"`
	Name       *string   `json:"name"`
	Target     string    `json:"target"`
	Draft      *bool     `json:"draft"`
	Prerelease *bool     `json:"prerelease"`
}

// actionInput 供各动作严格分支解码，不作为发现 Schema 的公共全集。
// 同名字段只有一个来源，changes 用于编辑，创建采用顶层存在性。
type actionInput struct {
	changeInput
	Index        int64       `json:"index"`
	Indices      []int64     `json:"indices"`
	Changes      changeInput `json:"changes"`
	Head         string      `json:"head"`
	Users        []string    `json:"users"`
	Teams        []string    `json:"teams"`
	Event        string      `json:"event"`
	Commit       string      `json:"commit_id"`
	ReviewID     int64       `json:"review_id"`
	Method       string      `json:"method"`
	DeleteBranch bool        `json:"delete_branch"`
	Tag          string      `json:"tag"`
	File         string      `json:"file_path"`
}

// issueChanges 将公共字段显式映射到已有共同版本输入。
// 不自动解析模糊名称，不让未提供字段变成覆盖或清空。
func issueChanges(input changeInput) issue.Changes {
	return issue.Changes{
		Title:     input.Title,
		Body:      input.Body,
		Assignees: input.Assignees,
		Labels:    input.Labels,
		Milestone: input.Milestone,
	}
}

// prepare 去重并保存原业务计划，只读准备不会发业务写请求。
// 占位、预览、领域快照和最终结果预算均在发放 token 前检查。
func (h *Host) prepare(ctx context.Context, arguments map[string]json.RawMessage) response {
	var input prepareInput
	if err := decode(arguments, &input); err != nil {
		return failed(err)
	}
	if !h.options.AllowWrite || (input.Action == "api.request" && !h.options.AllowRawAPI) {
		return failed(app.Wrap(app.WriteDisabled, "This action is not authorized by the MCP startup mode.", nil))
	}
	r := h.plans
	r.mu.Lock()
	existing := r.requests[input.RequestID]
	r.mu.Unlock()
	if existing != nil {
		return h.repeatPrepare(ctx, existing, input)
	}
	requireRepo := input.Action != "api.request"
	state, err := h.resolveInput(ctx, input.Context, requireRepo)
	if err != nil {
		return failed(err)
	}
	client, resolved, err := state.Auth.Open(ctx, state.Target, false)
	if err != nil {
		return failed(err)
	}
	client.Close()
	state.Target = resolved
	digest, err := intentDigest(input.Action, state, input.Params)
	if err != nil {
		return failed(err)
	}
	r.mu.Lock()
	// 其他 prepare 可能在本调用认证期间已完成注册。
	if existing = r.requests[input.RequestID]; existing != nil {
		r.mu.Unlock()
		return h.repeatPrepare(ctx, existing, input)
	}
	for _, entry := range r.requests {
		r.expireLocked(entry)
	}
	reserved := h.options.MaxOutput + 8192
	if len(r.requests) >= r.maxRecords || r.pending >= r.maxPending || reserved > r.maxBytes-r.used {
		r.mu.Unlock()
		return failed(app.Wrap(app.ResourceLimit, "The process plan registry is full. Inspect existing outcomes before starting a new process.", nil))
	}
	entry := &planEntry{
		requestID:        input.RequestID,
		action:           input.Action,
		digest:           digest,
		state:            "preparing",
		created:          r.now(),
		binding:          h.environment.Binding(state),
		workspaceBinding: h.workspaceBinding,
		target:           state.Target,
		size:             reserved,
		ready:            make(chan struct{}),
	}
	r.requests[input.RequestID] = entry
	r.pending++
	r.used += reserved
	r.mu.Unlock()
	operation, prepareErr := h.prepareOperation(ctx, state, input)
	var result response
	var id, token string
	if prepareErr == nil {
		id, prepareErr = randomID()
	}
	if prepareErr == nil {
		token, prepareErr = randomID()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer close(entry.ready)
	if prepareErr != nil {
		r.finishLocked(entry, "completed", failed(prepareErr))
		return h.snapshotLocked(entry, false)
	}
	if operation.eligible == 0 {
		result = dataResponse(state, operation.preview, app.Wrap(app.PartialFailure, "No target passed preparation. Inspect every target before starting a new request.", nil))
		result.Status = app.Failed
		r.finishLocked(entry, "completed", compactResponse(result))
		return h.snapshotLocked(entry, false)
	}
	result = dataResponse(state, operation.preview, nil)
	if operation.failures > 0 {
		result.Error = app.Wrap(app.PartialFailure, "Some targets failed preparation. Only the fixed eligible targets may execute; show all failures before confirming.", nil)
		result.Status = app.Partial
	}
	entry.id = id
	entry.token = token
	entry.tokenDigest = sha256.Sum256([]byte(token))
	entry.expires = r.now().Add(r.ttl)
	entry.state = "prepared"
	entry.operation = operation
	entry.result = result
	candidate := h.snapshotLocked(entry, true)
	_, _, wireBytes, encodeErr := h.wire(candidate)
	encoded, marshalErr := json.Marshal(candidate)
	size := operation.memory + len(encoded) + reserved
	// 批量终态必须有空间容纳每个目标的状态、错误和有限步骤，不能等发送后再裁掉目标。
	minimumOutcome := 2048 + (operation.eligible+operation.failures)*2048
	if minimumOutcome > h.options.MaxOutput {
		encodeErr = app.Wrap(app.ResponseTooLarge, "The target set cannot fit complete execution states within the output budget.", nil)
	}
	if encodeErr != nil || marshalErr != nil || wireBytes > h.options.MaxOutput || size-entry.size > r.maxBytes-r.used {
		entry.id = ""
		r.finishLocked(entry, "completed", failed(app.Wrap(app.ResponseTooLarge, "The complete write preview or retained plan exceeds the MCP budget. Reduce the target set or use the CLI.", encodeErr)))
		return h.snapshotLocked(entry, false)
	}
	r.used += size - entry.size
	entry.size = size
	r.ids[id] = entry
	return candidate
}

// repeatPrepare 返回同一规范化意图已有的计划或结果，不延长期限。
// 等待者取消不会取消原准备；本地身份变化不返回旧预览和凭据。
func (h *Host) repeatPrepare(ctx context.Context, entry *planEntry, input prepareInput) response {
	if !h.localBinding(ctx, entry) {
		h.plans.mu.Lock()
		defer h.plans.mu.Unlock()
		return restrictedState(entry)
	}
	state, err := h.resolveInput(ctx, input.Context, input.Action != "api.request")
	if err != nil {
		return failed(err)
	}
	if state.Target.Account == "" {
		state.Target.Account = entry.target.Account
	}
	digest, err := intentDigest(input.Action, state, input.Params)
	if err != nil {
		return failed(err)
	}
	if digest != entry.digest {
		return failed(app.Wrap(app.Conflict, "This request_id already belongs to a different write intent.", nil))
	}
	select {
	case <-entry.ready:
	case <-ctx.Done():
		return failed(ctx.Err())
	}
	r := h.plans
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(entry)
	return h.snapshotLocked(entry, true)
}

// prepareOperation 调用真实应用服务的 Prepare 并闭包保留该 Plan。
// 不启动 CLI、不重复领域校验实现，固定仓库 ID 独立于可重用的 owner/repo 名称。
func (h *Host) prepareOperation(ctx context.Context, state session.State, input prepareInput) (*preparedOperation, error) {
	if input.Action == "api.request" {
		return h.prepareAPI(ctx, state, input.Params)
	}
	var args actionInput
	if err := json.Unmarshal(input.Params, &args); err != nil {
		return nil, app.Wrap(app.InvalidArgument, "Unable to decode action parameters.", err)
	}
	repositoryID, err := resolveRepositoryID(ctx, state)
	if err != nil {
		return nil, err
	}
	repository, verified, err := catalog.GetRepository(ctx, state.Auth, state.Target, repositoryID, state.Target.Repository)
	if err != nil {
		return nil, err
	}
	state.Target = verified
	version, err := h.environment.Version(ctx, state, false)
	if err != nil {
		return nil, err
	}
	service := write.Service{
		Auth:     state.Auth,
		Target:   state.Target,
		Version:  version,
		Revision: state.Snapshot.RevisionKey(),
		Locks:    h.environment.Locks,
	}
	operation := &preparedOperation{
		repositoryID: repository.ID,
		eligible:     1,
	}
	numbers := args.Indices
	if args.Index > 0 {
		numbers = []int64{args.Index}
	}
	changes := args.changeInput
	if strings.HasSuffix(input.Action, ".edit") {
		changes = args.Changes
	}
	switch {
	case strings.HasPrefix(input.Action, "issue."):
		application := issue.Service{
			Auth:     service.Auth,
			Target:   service.Target,
			Version:  service.Version,
			Revision: service.Revision,
			Locks:    service.Locks,
		}
		request := issue.Request{
			Operation: input.Action,
			Numbers:   numbers,
			Changes:   issueChanges(changes),
			Users:     args.Users,
		}
		plan, err := application.Prepare(ctx, request)
		if err != nil {
			return nil, err
		}
		preview := plan.Preview()
		operation.preview = preview
		operation.eligible = len(preview.Targets)
		for _, item := range preview.Targets {
			if item.Error != nil {
				operation.eligible--
				operation.failures++
			}
		}
		operation.memory, err = plan.MemoryBytes()
		if err != nil {
			return nil, err
		}
		operation.apply = func(ctx context.Context, observed func()) response {
			current := application
			current.Auth.HTTPOptions.OnWrite = observed
			current.Auth.HTTPOptions.Timeout = h.options.WriteTimeout
			result, err := current.Commit(ctx, plan)
			return businessResponse(state, result, err)
		}
	case strings.HasPrefix(input.Action, "pr."):
		application := pullrequest.Service{Service: service}
		request := pullrequest.Request{
			Operation:    input.Action,
			Numbers:      numbers,
			Changes:      issueChanges(changes),
			Base:         changes.Base,
			Head:         args.Head,
			Users:        args.Users,
			Teams:        args.Teams,
			Event:        args.Event,
			Commit:       args.Commit,
			ReviewID:     args.ReviewID,
			Method:       args.Method,
			DeleteBranch: args.DeleteBranch,
		}
		plan, err := application.Prepare(ctx, request)
		if err != nil {
			return nil, err
		}
		preview, err := plan.Preview()
		if err != nil {
			return nil, err
		}
		operation.preview = preview
		operation.eligible = len(preview.Targets)
		for _, item := range preview.Targets {
			if item.Error != nil {
				operation.eligible--
				operation.failures++
			}
		}
		operation.memory, err = plan.MemoryBytes()
		if err != nil {
			return nil, err
		}
		operation.apply = func(ctx context.Context, observed func()) response {
			current := application
			current.Auth.HTTPOptions.OnWrite = observed
			current.Auth.HTTPOptions.Timeout = h.options.WriteTimeout
			result, err := current.Commit(ctx, plan)
			return businessResponse(state, result, err)
		}
	case strings.HasPrefix(input.Action, "release."):
		application := release.Service{Service: service}
		if input.Action == "release.asset.upload" {
			if h.scope == nil {
				return nil, h.localAccessError()
			}
			application.OpenInput = h.scope.Open
			application.FreezeInput = h.scope.Freeze
		}
		request := release.Request{
			Operation:  input.Action,
			Tag:        args.Tag,
			Title:      changes.Name,
			Body:       changes.Body,
			Draft:      changes.Draft,
			Prerelease: changes.Prerelease,
			Target:     changes.Target,
			File:       args.File,
		}
		if input.Action == "release.asset.upload" && args.Name != nil {
			request.Name = *args.Name
			request.Title = nil
		}
		plan, err := application.Prepare(ctx, request)
		if err != nil {
			return nil, err
		}
		operation.preview, err = plan.Preview()
		if err != nil {
			return nil, err
		}
		operation.memory, err = plan.MemoryBytes()
		if err != nil {
			return nil, err
		}
		operation.apply = func(ctx context.Context, observed func()) response {
			current := application
			current.Auth.HTTPOptions.OnWrite = observed
			current.Auth.HTTPOptions.Timeout = h.options.WriteTimeout
			if input.Action == "release.asset.upload" {
				current.Auth.HTTPOptions.Timeout = h.options.UploadTimeout
			}
			result, err := current.Commit(ctx, plan)
			return businessResponse(state, result, err)
		}
	default:
		return nil, app.Wrap(app.InvalidArgument, "Unknown write action.", nil)
	}
	return operation, nil
}

// resolveRepositoryID 读取当前目标的稳定仓库 ID，再交给目录服务做完整名称核对。
// 计划必须绑定服务端 ID；同名仓库重建或改名时，后续 commit 不能沿用旧计划。
func resolveRepositoryID(ctx context.Context, state session.State) (int64, error) {
	result, err := repository.Read(ctx, state.Auth, state.Target, repository.Query{
		Operation: "repo.view",
		Page:      1,
		Limit:     1,
	})
	if err != nil {
		return 0, err
	}
	if len(result.Items) != 1 {
		return 0, app.Wrap(app.ServerError, "The repository response did not contain one repository record.", nil)
	}
	encoded, err := json.Marshal(result.Items[0].Data)
	if err != nil {
		return 0, app.Wrap(app.ServerError, "Unable to read the repository identity.", err)
	}
	var value struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(encoded, &value); err != nil || value.ID < 1 {
		return 0, app.Wrap(app.ServerError, "The repository response did not contain a valid stable ID.", err)
	}
	return value.ID, nil
}
