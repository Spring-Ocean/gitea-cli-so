package issue

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Request 表达一个明确 Issue 意图，创建无编号，其余写操作除状态转换外为单目标。
// LabelRefs 与 MilestoneRef 会在预览前解析，Changes 中指针区分未提供和清空。
type Request struct {
	Operation    string   `json:"operation"`
	Numbers      []int64  `json:"numbers,omitempty"`
	Changes      Changes  `json:"changes"`
	Users        []string `json:"users,omitempty"`
	LabelRefs    []string `json:"label_refs,omitempty"`
	MilestoneRef string   `json:"milestone_ref,omitempty"`
}

// Preview 是入口确认时展示的固定目标与已解析变更。
// LegacyWarning 明确旧版负责人替换不是全局原子增删，--yes 仅接受已展示的语义。
type Preview struct {
	Operation     string          `json:"operation"`
	Context       app.Context     `json:"context"`
	Changes       Changes         `json:"changes"`
	Users         []string        `json:"users,omitempty"`
	Targets       []PreviewTarget `json:"targets"`
	LegacyWarning string          `json:"warning,omitempty"`
}

// PreviewTarget 保存目标查询状态与失败，不隐瞒批量内无效对象。
// BeforeState 供明确展示，实际提交仍会读回类型和前置条件。
type PreviewTarget struct {
	Number      int64      `json:"number"`
	BeforeState string     `json:"before_state,omitempty"`
	Error       *app.Error `json:"error,omitempty"`
}

type preparedTarget struct {
	number int64
	before Issue
	err    error
}

// Plan 保留一次已完成输入和目标查询的写入意图，入口确认后才能 Commit。
// 私有字段不可通过预览修改；Commit 最多消费一次，未知结果不能用同一计划重发。
type Plan struct {
	request       Request
	targetContext app.Context
	targets       []preparedTarget
	revision      string
	legacy        bool
	consumed      atomic.Bool
}

// Preview 返回可展示且不包含凭证的已准备内容。
// 批量失败也逐项列出，不能将未确认对象隐藏到提交阶段才选择。
func (plan *Plan) Preview() Preview {
	preview := Preview{
		Operation: plan.request.Operation,
		Context:   plan.targetContext,
		Changes:   copyChanges(plan.request.Changes),
		Users:     append([]string{}, plan.request.Users...),
		Targets:   []PreviewTarget{},
	}
	for _, target := range plan.targets {
		var failure *app.Error
		if original := app.Failure(target.err); original != nil {
			value := *original
			value.Cause = nil
			failure = &value
		}
		preview.Targets = append(preview.Targets, PreviewTarget{
			Number:      target.number,
			BeforeState: target.before.State,
			Error:       failure,
		})
	}
	if plan.legacy {
		preview.LegacyWarning = "This server requires replacing the full assignee set. Rechecks reduce visible conflicts but cannot prevent concurrent updates by other processes."
	}
	return preview
}

// NormalizeNumbers 去重并保留首次次序，要求 1 至 100 个正编号。
// 只适用于已明确传入或用户选择的编号，不自动从列表填充目标。
func NormalizeNumbers(numbers []int64) ([]int64, error) {
	result := []int64{}
	seen := map[int64]bool{}
	for _, number := range numbers {
		if number < 1 {
			return nil, app.Wrap(app.InvalidArgument, "Issue numbers must be positive integers.", nil)
		}
		if !seen[number] {
			seen[number] = true
			result = append(result, number)
		}
	}
	if len(result) == 0 || len(result) > 100 {
		return nil, app.Wrap(app.InvalidArgument, "Select between 1 and 100 distinct issues.", nil)
	}
	return result, nil
}

// Prepare 校验共同版本、输入、身份及所有目标，解析标签与里程碑后生成单次计划。
// 此过程只读，不发送业务写入；单项目错误留在计划中，系统性错误立即返回。
func (s Service) Prepare(ctx context.Context, input Request) (*Plan, error) {
	if !s.Version.ExactMatch {
		return nil, app.Wrap(app.CompatibilityError, "Write semantics are not verified for this exact server version. Refresh instance evidence before continuing.", nil)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, app.Wrap(app.InvalidArgument, "Unable to encode the requested changes.", err)
	}
	var request Request
	if err := json.Unmarshal(encoded, &request); err != nil {
		return nil, app.Wrap(app.InvalidArgument, "Unable to isolate the requested changes.", err)
	}
	if err := validateRequest(&request); err != nil {
		return nil, err
	}
	base, err := s.base()
	if err != nil {
		return nil, err
	}
	client, t, err := s.Auth.Open(ctx, s.Target, false)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	request.Changes, err = ResolveReferences(ctx, client, base, request.Changes, request.LabelRefs, request.MilestoneRef)
	if err != nil {
		return nil, err
	}
	legacy := (request.Operation == "issue.assignee.add" || request.Operation == "issue.assignee.remove") && s.Version.Raw != "1.27.3" && s.Version.Raw != "28.3.0"
	plan := &Plan{
		request:       request,
		targetContext: t.Context(),
		targets:       []preparedTarget{},
		revision:      s.Revision,
		legacy:        legacy,
	}
	if request.Operation == "issue.create" {
		plan.targets = append(plan.targets, preparedTarget{})
		return plan, nil
	}
	for _, number := range request.Numbers {
		value, err := get(ctx, client, base, number)
		if err != nil && systemic(err) {
			return nil, err
		}
		plan.targets = append(plan.targets, preparedTarget{
			number: number,
			before: value,
			err:    err,
		})
	}
	return plan, nil
}

// validateRequest 检查操作输入与字段存在性，禁止非目标状态命令夹带编辑字段。
// 空正文清空由 CLI 明确转换为指针；评论始终要求非空正文。
func validateRequest(request *Request) error {
	if (len(request.LabelRefs) > 0 && request.Changes.Labels != nil) ||
		(request.MilestoneRef != "" && request.Changes.Milestone != nil) {
		return app.Wrap(app.InvalidArgument, "Provide one explicit source for each label or milestone change.", nil)
	}
	if request.Changes.Labels != nil {
		values := []int64{}
		seen := map[int64]bool{}
		for _, id := range *request.Changes.Labels {
			if id < 1 {
				return app.Wrap(app.InvalidArgument, "Label IDs must be positive.", nil)
			}
			if !seen[id] {
				seen[id] = true
				values = append(values, id)
			}
		}
		request.Changes.Labels = &values
	}
	if request.Changes.Assignees != nil {
		values, err := normalizeUsers(*request.Changes.Assignees)
		if err != nil {
			return err
		}
		request.Changes.Assignees = &values
	}
	users, err := normalizeUsers(request.Users)
	if err != nil {
		return err
	}
	request.Users = users
	changes := request.Changes
	if changes.Milestone != nil && *changes.Milestone < 0 {
		return app.Wrap(app.InvalidArgument, "Milestone IDs must be positive, or 0 for an explicit clear.", nil)
	}
	if changes.Title != nil && strings.TrimSpace(*changes.Title) == "" {
		return app.Wrap(app.InvalidArgument, "An issue title must not be empty.", nil)
	}
	if changes.Body != nil && len(*changes.Body) > 256<<10 {
		return app.Wrap(app.InvalidArgument, "Issue text exceeds the 256 KiB business command limit. Use an explicit raw API request for larger content.", nil)
	}
	hasCollection := changes.Assignees != nil || changes.Milestone != nil || request.MilestoneRef != ""
	hasLabels := changes.Labels != nil || len(request.LabelRefs) > 0
	unexpected := false
	switch request.Operation {
	case "issue.create", "issue.edit":
		unexpected = len(request.Users) > 0
	case "issue.comment.add":
		unexpected = changes.Title != nil || hasCollection || hasLabels || len(request.Users) > 0
	case "issue.assignee.add", "issue.assignee.remove":
		unexpected = changes.Title != nil || changes.Body != nil || hasCollection || hasLabels
	case "issue.label.add", "issue.label.remove":
		unexpected = changes.Title != nil || changes.Body != nil || hasCollection || len(request.Users) > 0
	}
	if unexpected {
		return app.Wrap(app.InvalidArgument, "The operation contains fields that belong to a different command.", nil)
	}
	switch request.Operation {
	case "issue.create":
		if changes.Title == nil || len(request.Numbers) > 0 {
			return app.Wrap(app.InvalidArgument, "Issue creation requires a title and no existing issue numbers.", nil)
		}
		return nil
	case "issue.edit":
		if changes.Title == nil && changes.Body == nil && changes.Assignees == nil && changes.Labels == nil && changes.Milestone == nil && len(request.LabelRefs) == 0 && request.MilestoneRef == "" {
			return app.Wrap(app.InvalidArgument, "Provide at least one explicit edit or clear option.", nil)
		}
	case "issue.close", "issue.reopen":
		if changes.Title != nil || changes.Body != nil || changes.Assignees != nil || changes.Labels != nil || changes.Milestone != nil || len(request.Users) > 0 || len(request.LabelRefs) > 0 || request.MilestoneRef != "" {
			return app.Wrap(app.InvalidArgument, "State transitions cannot include unrelated edit fields.", nil)
		}
	case "issue.comment.add":
		if changes.Body == nil || strings.TrimSpace(*changes.Body) == "" {
			return app.Wrap(app.InvalidArgument, "A non-empty comment body is required.", nil)
		}
	case "issue.assignee.add", "issue.assignee.remove":
		if len(request.Users) == 0 {
			return app.Wrap(app.InvalidArgument, "Select at least one assignee.", nil)
		}
	case "issue.label.add", "issue.label.remove":
		if len(request.LabelRefs) == 0 && (changes.Labels == nil || len(*changes.Labels) == 0) {
			return app.Wrap(app.InvalidArgument, "Select at least one label.", nil)
		}
	default:
		return app.Wrap(app.InvalidArgument, "Unknown Issue write operation.", nil)
	}
	numbers, err := NormalizeNumbers(request.Numbers)
	if err != nil {
		return err
	}
	if request.Operation != "issue.close" && request.Operation != "issue.reopen" && len(numbers) != 1 {
		return app.Wrap(app.InvalidArgument, "This command accepts exactly one issue.", nil)
	}
	request.Numbers = numbers
	return nil
}

// copyChanges 复制预览内的所有指针与集合，防止展示数据反向修改提交意图。
// 值类型不包含密钥，明确空集合仍保留为非 nil 指针。
func copyChanges(changes Changes) Changes {
	copy := changes
	if changes.Title != nil {
		value := *changes.Title
		copy.Title = &value
	}
	if changes.Body != nil {
		value := *changes.Body
		copy.Body = &value
	}
	if changes.Milestone != nil {
		value := *changes.Milestone
		copy.Milestone = &value
	}
	if changes.Assignees != nil {
		values := append([]string{}, (*changes.Assignees)...)
		copy.Assignees = &values
	}
	if changes.Labels != nil {
		values := append([]int64{}, (*changes.Labels)...)
		copy.Labels = &values
	}
	return copy
}

// normalizeUsers 校验并去重明确账号，保留首次出现的拼写及次序。
// 负责人与 helper 用户名不混用，空值或控制字符不能进入请求。
func normalizeUsers(users []string) ([]string, error) {
	values := []string{}
	seen := map[string]bool{}
	for _, user := range users {
		if strings.TrimSpace(user) == "" || len(user) > 255 || strings.ContainsAny(user, "/\\") {
			return nil, app.Wrap(app.InvalidArgument, "Assignee names must be non-empty valid accounts.", nil)
		}
		for _, character := range user {
			if unicode.IsControl(character) {
				return nil, app.Wrap(app.InvalidArgument, "Assignee names must not contain control characters.", nil)
			}
		}
		key := strings.ToLower(user)
		if !seen[key] {
			seen[key] = true
			values = append(values, user)
		}
	}
	return values, nil
}

// systemic 判断应停止剩余批量请求的共享来源或网络故障。
// 404、403 和目标冲突属于逐项错误，写入未知则停止避免扩大不确定状态。
func systemic(err error) bool {
	if err == nil {
		return false
	}
	switch app.Failure(err).Code {
	case app.AuthRequired, app.IdentityMismatch, app.NetworkError, app.TLSError, app.ConfigError, app.ConfigConflict, app.Cancelled, app.ResultUnknown:
		return true
	}
	return false
}

// patchFields 只序列化共同 PATCH 字段，标签由独立端点处理。
// 具体清空值由非 nil 指针携带，未提供字段绝不会写入空值。
func patchFields(changes Changes) map[string]any {
	fields := map[string]any{}
	if changes.Title != nil {
		fields["title"] = *changes.Title
	}
	if changes.Body != nil {
		fields["body"] = *changes.Body
	}
	if changes.Assignees != nil {
		fields["assignees"] = *changes.Assignees
	}
	if changes.Milestone != nil {
		fields["milestone"] = *changes.Milestone
	}
	return fields
}

// targetName 为创建或既有资源构造稳定逐项标识。
// 不使用标题作为键，以免同名 Issue 或远端内容改变目标身份。
func targetName(number int64) string {
	if number == 0 {
		return "new_issue"
	}
	return strconv.FormatInt(number, 10)
}

// MemoryBytes 估算计划持有的序列化意图与完整前置快照大小，供入口限制缓存。
// 不返回快照内容或凭证；编码失败应拒绝注册，额外预留结构体和索引开销。
func (plan *Plan) MemoryBytes() (int, error) {
	before := []Issue{}
	for _, item := range plan.targets {
		before = append(before, item.before)
	}
	encoded, err := json.Marshal(struct {
		Request Request
		Before  []Issue
	}{Request: plan.request, Before: before})
	if err != nil {
		return 0, app.Wrap(app.IOError, "Unable to measure the prepared plan.", err)
	}
	return len(encoded)*2 + 4096, nil
}
