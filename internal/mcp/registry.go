package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/catalog"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/session"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
)

// planMetadata 是公开的进程内计划状态，不包含内部应用 Plan 或认证材料。
// confirmation_token 只在原 prepare 响应中提供，不能作为真人批准证明。
type planMetadata struct {
	RequestID   string `json:"request_id"`
	PlanID      string `json:"plan_id,omitempty"`
	Action      string `json:"action"`
	State       string `json:"plan_state"`
	Committable bool   `json:"committable"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	Token       string `json:"confirmation_token,omitempty"`
}

// preparedOperation 持有实际领域计划的执行闭包和内存估算。
// 执行闭包只能调用原计划一次，不在 commit 时重新准备或改换参数。
type preparedOperation struct {
	preview      any
	memory       int
	failures     int
	eligible     int
	repositoryID int64
	apply        func(context.Context, func()) response
}

// planEntry 保存单一请求的不可变绑定与可串行更新的执行状态。
// mu 由所属注册表持有，attempted 仅记录 HTTP 写入发送边界。
type planEntry struct {
	requestID        string
	id               string
	action           string
	digest           [32]byte
	token            string
	tokenDigest      [32]byte
	state            string
	created          time.Time
	expires          time.Time
	binding          string
	workspaceBinding string
	target           target.Target
	operation        *preparedOperation
	result           response
	size             int
	ready            chan struct{}
	attempted        atomic.Bool
}

// planRegistry 以进程为界保留去重索引，终态不被自动淘汰。
// 全部索引、准备和结果受数量及序列化预算约束，状态查询无需远端在线。
type planRegistry struct {
	mu         sync.Mutex
	requests   map[string]*planEntry
	ids        map[string]*planEntry
	used       int
	pending    int
	now        func() time.Time
	ttl        time.Duration
	maxRecords int
	maxPending int
	maxBytes   int
}

// newPlanRegistry 初始化有界注册表，不启动后台任务或保存磁盘计划。
// 过期采用访问时处理，退出后所有随机 ID 均失效。
func newPlanRegistry() *planRegistry {
	return &planRegistry{
		requests:   map[string]*planEntry{},
		ids:        map[string]*planEntry{},
		now:        time.Now,
		ttl:        5 * time.Minute,
		maxRecords: 1000,
		maxPending: 100,
		maxBytes:   64 << 20,
	}
}

// randomID 生成当前进程计划使用的不可预测标识。
// 随机源失败拒绝计划，不以计数器或时间戳作为确认凭据。
func randomID() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", app.Wrap(app.IOError, "Unable to create a secure plan identifier.", err)
	}
	return hex.EncodeToString(buffer), nil
}

// intentDigest 规范化字段顺序及默认值，不把原始密钥保存进摘要输入对象。
// 目标在解析后绑定完整实例／账号／仓库；配置另行比较，不偷偷迁移旧计划。
func intentDigest(action string, state session.State, params json.RawMessage) ([32]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return [32]byte{}, app.Wrap(app.InvalidArgument, "Invalid action parameters.", err)
	}
	defaults := map[string]json.RawMessage{}
	if action == "release.create" {
		defaults = map[string]json.RawMessage{"draft": json.RawMessage("true"), "prerelease": json.RawMessage("false")}
	}
	if action == "pr.merge" {
		defaults["delete_branch"] = json.RawMessage("false")
	}
	for key, value := range defaults {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	var canonicalParams any
	encoded, err := json.Marshal(fields)
	if err == nil {
		decoder := json.NewDecoder(strings.NewReader(string(encoded)))
		decoder.UseNumber()
		err = decoder.Decode(&canonicalParams)
	}
	if err != nil {
		return [32]byte{}, app.Wrap(app.InvalidArgument, "Unable to canonicalize the write intent.", err)
	}
	encoded, err = json.Marshal([]any{action, state.Target.Instance.URL, state.Target.Account, state.Target.Repository, canonicalParams})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

// localBinding 重读当前本地身份引用，结果查询不发网络请求。
// 使用原计划明确账号；配置修订、环境来源及当前工具入口的会话工作区绑定变化都会使比较失败。
func (h *Host) localBinding(ctx context.Context, entry *planEntry) bool {
	if h.workspaceBinding != entry.workspaceBinding {
		return false
	}
	state, err := h.environment.Resolve(ctx, target.Input{
		Instance: entry.target.Name,
		Account:  entry.target.Account,
		Repo:     entry.target.Repository,
		NoLocal:  true,
	})
	return err == nil && h.environment.Binding(state) == entry.binding
}

// expireLocked 在访问准备计划时释放大对象并留下不可重放的终态索引。
// executing 和 completed 不受准备期限影响，调用方必须持有注册表锁。
func (r *planRegistry) expireLocked(entry *planEntry) {
	if entry.state == "prepared" && !r.now().Before(entry.expires) {
		r.finishLocked(entry, "expired", failed(app.Wrap(app.PlanExpired, "This plan expired. Prepare a new request after inspecting the target.", nil)))
	}
}

// finishLocked 保存最终有界结果并释放领域计划，不删除 request_id 索引。
// 最小记录空间在准备时预留，未知执行结果不得被淘汰或改成可重试。
func (r *planRegistry) finishLocked(entry *planEntry, state string, result response) {
	if entry.state == "preparing" || entry.state == "prepared" || entry.state == "executing" {
		r.pending--
	}
	r.used -= entry.size
	entry.state = state
	entry.operation = nil
	entry.token = ""
	entry.result = result
	encoded, err := json.Marshal(result)
	if err != nil {
		entry.result = failed(app.Wrap(app.IOError, "Unable to retain the operation result. Inspect the server before retrying.", err))
		entry.size = 8192
	} else {
		entry.size = len(encoded) + 8192
	}
	r.used += entry.size
}

// snapshotLocked 返回独立的计划元数据，不能修改注册表内部字段。
// 只有 prepared 的 prepare 重放可取回原 token，状态查询不发 token。
func (h *Host) snapshotLocked(entry *planEntry, token bool) response {
	result := entry.result
	if result.SchemaVersion == 0 {
		result = failed(app.Wrap(app.IOError, "The plan has no retained result.", nil))
	}
	if entry.state == "executing" || entry.state == "preparing" {
		result = response{Result: app.NewResult(entry.target.Context(), nil, nil)}
	}
	meta := &metadata{}
	if result.Meta != nil {
		*meta = *result.Meta
	}
	meta.Plan = &planMetadata{
		RequestID:   entry.requestID,
		PlanID:      entry.id,
		Action:      entry.action,
		State:       entry.state,
		Committable: entry.state == "prepared",
		CreatedAt:   entry.created.UTC().Format(time.RFC3339),
	}
	if !entry.expires.IsZero() {
		meta.Plan.ExpiresAt = entry.expires.UTC().Format(time.RFC3339)
	}
	if token && entry.state == "prepared" {
		meta.Plan.Token = entry.token
	}
	if meta.Execution == nil {
		attempted := "no"
		if entry.attempted.Load() {
			attempted = "yes"
		}
		meta.Execution = &executionMetadata{WriteAttempted: attempted}
	}
	result.Meta = meta
	return result
}

// restrictedState 返回身份绑定变化后的最小状态，不泄露旧预览或结果。
// 保留 ID 供调用者停止重放，已执行结果仍在原注册表中而非被重新计算。
func restrictedState(entry *planEntry) response {
	result := failed(app.Wrap(app.PreconditionChanged, "The plan's local identity or configuration changed. Inspect the original account and remote objects before preparing again.", nil))
	result.Meta = &metadata{Plan: &planMetadata{
		RequestID:   entry.requestID,
		PlanID:      entry.id,
		Action:      entry.action,
		State:       entry.state,
		Committable: false,
		CreatedAt:   entry.created.UTC().Format(time.RFC3339),
	}}
	return result
}

// status 查询单一进程内计划，恰好接受一个 ID 并避免远端网络依赖。
// 身份改变时只给最小终态，正在上传不会占用此工具的执行槽。
func (h *Host) status(ctx context.Context, arguments map[string]json.RawMessage) response {
	var input struct {
		PlanID    string `json:"plan_id"`
		RequestID string `json:"request_id"`
	}
	if err := decode(arguments, &input); err != nil {
		return failed(err)
	}
	r := h.plans
	r.mu.Lock()
	entry := r.ids[input.PlanID]
	if input.RequestID != "" {
		entry = r.requests[input.RequestID]
	}
	r.mu.Unlock()
	if entry == nil {
		return failed(app.Wrap(app.NotFound, "No such plan exists in this process. After restart, inspect the remote object before preparing another write.", nil))
	}
	allowed := h.localBinding(ctx, entry)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(entry)
	if !allowed {
		if entry.state == "prepared" {
			r.finishLocked(entry, "invalidated", failed(app.Wrap(app.PreconditionChanged, "The local identity changed after preparation.", nil)))
		}
		return restrictedState(entry)
	}
	return h.snapshotLocked(entry, false)
}

// commitTimeout 为已知上传计划使用上传预算，不从任意参数扩展时间。
// 不存在或非法计划仍采用普通写超时，后续按原权限与 token 校验。
func (h *Host) commitTimeout(arguments map[string]json.RawMessage) time.Duration {
	var id string
	if err := json.Unmarshal(arguments["plan_id"], &id); err != nil {
		return h.options.WriteTimeout
	}
	h.plans.mu.Lock()
	entry := h.plans.ids[id]
	upload := entry != nil && (entry.action == "release.asset.upload" || entry.action == "api.request")
	h.plans.mu.Unlock()
	if upload {
		return h.options.UploadTimeout
	}
	return h.options.WriteTimeout
}

// commit 只有一次执行者能调用原领域 Plan，重复请求只返回现有状态。
// 账号／仓库身份和配置先重验，错误 token 不消费；发送后取消按实际结果保留 unknown。
func (h *Host) commit(ctx context.Context, arguments map[string]json.RawMessage) response {
	if !h.options.AllowWrite {
		return failed(app.Wrap(app.WriteDisabled, "Write tools require --allow-write.", nil))
	}
	var input struct {
		ID    string `json:"plan_id"`
		Token string `json:"confirmation_token"`
	}
	if err := decode(arguments, &input); err != nil {
		return failed(err)
	}
	r := h.plans
	r.mu.Lock()
	entry := r.ids[input.ID]
	if entry == nil {
		r.mu.Unlock()
		return failed(app.Wrap(app.NotFound, "This plan is not available in the current process.", nil))
	}
	expected := sha256.Sum256([]byte(entry.token))
	provided := sha256.Sum256([]byte(input.Token))
	// 终态仍需原确认凭据；只保存 token 的哈希避免响应或状态重发。
	if entry.state != "prepared" && entry.state != "executing" {
		expected = entryTokenDigest(entry)
	}
	if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		r.mu.Unlock()
		return failed(app.Wrap(app.PermissionDenied, "Invalid confirmation token. The plan was not executed.", nil))
	}
	r.mu.Unlock()
	allowed := h.localBinding(ctx, entry)
	r.mu.Lock()
	r.expireLocked(entry)
	if !allowed {
		if entry.state == "prepared" {
			r.finishLocked(entry, "invalidated", failed(app.Wrap(app.PreconditionChanged, "The identity changed after preparation.", nil)))
		}
		result := restrictedState(entry)
		r.mu.Unlock()
		return result
	}
	if entry.state != "prepared" {
		result := h.snapshotLocked(entry, false)
		r.mu.Unlock()
		return result
	}
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return failed(err)
	}
	operation := entry.operation
	entry.state = "executing"
	r.mu.Unlock()
	var result response
	if operation.repositoryID > 0 {
		state, err := h.environment.Resolve(ctx, target.Input{
			Instance: entry.target.Name,
			Account:  entry.target.Account,
			Repo:     entry.target.Repository,
			NoLocal:  true,
		})
		if err == nil {
			_, _, err = catalog.GetRepository(ctx, state.Auth, state.Target, operation.repositoryID, entry.target.Repository)
		}
		if err != nil {
			result = failed(app.Wrap(app.PreconditionChanged, "The prepared repository identity or access changed.", err))
		}
	}
	if result.SchemaVersion == 0 {
		result = operation.apply(ctx, func() {
			entry.attempted.Store(true)
		})
	}
	if result.Error != nil && result.Error.Code == app.ConfigConflict {
		result.Error = app.Wrap(app.PreconditionChanged, "Configuration changed after preparation.", result.Error)
	}
	// 只在确实超限时省略正文，提前给追加的计划元数据留空间。
	_, _, size, encodeErr := h.wire(result)
	if encodeErr != nil || size > h.options.MaxOutput-4096 {
		result = compactResponse(result)
	}
	r.mu.Lock()
	finalState := "completed"
	if !entry.attempted.Load() && result.Error != nil && result.Error.Code == app.PreconditionChanged {
		finalState = "invalidated"
	}
	r.finishLocked(entry, finalState, h.retainedResult(result))
	final := h.snapshotLocked(entry, false)
	r.mu.Unlock()
	return final
}

// entryTokenDigest 读取终态保留的随机凭据摘要，不暴露原 token。
// digest 与输入意图摘要分开保存，重复 commit 仍须证明持有原计划凭据。
func entryTokenDigest(entry *planEntry) [32]byte {
	return entry.tokenDigest
}
