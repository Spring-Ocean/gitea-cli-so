package issue

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// List 查询 Issue，明确发送 type=issues 并再次排除 PR。
// 未知版本或无法无损表达的筛选先扫描完整有界集合，再本地筛选及分页。
func (s Service) List(ctx context.Context, filter Filter) (app.Result, error) {
	if filter.State != "open" && filter.State != "closed" && filter.State != "all" {
		return app.Result{}, app.Wrap(app.InvalidArgument, "Issue state must be open, closed, or all.", nil)
	}
	if filter.Page < 1 || filter.Limit < 1 || filter.Limit > 100 || filter.Page > int(^uint(0)>>1)/filter.Limit {
		return app.Result{}, app.Wrap(app.InvalidArgument, "Use page >= 1 and limit between 1 and 100.", nil)
	}
	base, err := s.base()
	if err != nil {
		return app.Result{}, err
	}
	client, t, err := s.Auth.Open(ctx, s.Target, true)
	if err != nil {
		return app.Result{}, err
	}
	defer client.Close()
	labels, err := resolveLabels(ctx, client, base, filter.Labels)
	if err != nil {
		return app.Result{}, err
	}
	var milestone int64
	if filter.Milestone != "" {
		milestone, err = resolveMilestone(ctx, client, base, filter.Milestone)
		if err != nil {
			return app.Result{}, err
		}
	}
	parameters := url.Values{"state": {filter.State}, "type": {"issues"}}
	// 服务端多标签通常采用全部匹配；CLI 承诺任一匹配，需完整读取后计算。
	// 文本搜索同样避开异步索引，保证增加搜索条件不会改变标签语义。
	local := !s.Version.ExactMatch || filter.Search != "" || len(labels) > 1
	for _, label := range labels {
		local = local || strings.Contains(label.Name, ",")
	}
	if !local {
		parameters.Set("q", filter.Search)
		parameters.Set("assigned_by", filter.Assignee)
		if len(labels) > 0 {
			names := []string{}
			for _, label := range labels {
				names = append(names, label.Name)
			}
			parameters.Set("labels", strings.Join(names, ","))
		}
		if milestone != 0 {
			parameters.Set("milestones", strconv.FormatInt(milestone, 10))
		}
	}
	page, limit, all := filter.Page, filter.Limit, filter.All
	if local {
		page, limit, all = 1, 100, true
	}
	found, err := gitea.Pages[Issue](ctx, client, base+"/issues", parameters, page, limit, all)
	if err != nil {
		return app.Result{}, err
	}
	values := []Issue{}
	for _, value := range found.Items {
		if len(value.PullRequest) > 0 && string(value.PullRequest) != "null" {
			continue
		}
		if local && !matches(value, filter, labels, milestone) {
			continue
		}
		values = append(values, value)
	}
	result := app.Page[Issue]{
		Items:   values,
		Page:    filter.Page,
		Limit:   filter.Limit,
		HasMore: found.HasMore,
	}
	if local {
		start := (filter.Page - 1) * filter.Limit
		if start < 0 || start > len(values) {
			start = len(values)
		}
		end := len(values)
		if !filter.All && start+filter.Limit < end {
			end = start + filter.Limit
		}
		result.Items = values[start:end]
		result.HasMore = end < len(values)
	}
	return app.NewResult(t.Context(), []app.Item{{
		Target: t.Repository,
		Status: app.Success,
		Data:   result,
	}}, nil), nil
}

// View 显示一个已校验的 Issue，不允许 PR 编号冒充。
// 公开资源可匿名读取，使用凭证时仍先验证明确身份。
func (s Service) View(ctx context.Context, index int64) (app.Result, error) {
	base, err := s.base()
	if err != nil {
		return app.Result{}, err
	}
	client, t, err := s.Auth.Open(ctx, s.Target, true)
	if err != nil {
		return app.Result{}, err
	}
	defer client.Close()
	value, err := get(ctx, client, base, index)
	if err != nil {
		return app.Result{}, err
	}
	return app.NewResult(t.Context(), []app.Item{{
		Target: strconv.FormatInt(index, 10),
		Status: app.Success,
		Data:   value,
	}}, nil), nil
}

// Related 在验证 Issue 类型后读取评论或当前标签，默认有界一页。
// label list 的集合无分页参数，comments 沿用通用分页与扫描上限。
func (s Service) Related(ctx context.Context, index int64, kind string, page, limit int, all bool) (app.Result, error) {
	base, err := s.base()
	if err != nil {
		return app.Result{}, err
	}
	client, t, err := s.Auth.Open(ctx, s.Target, true)
	if err != nil {
		return app.Result{}, err
	}
	defer client.Close()
	if _, err := get(ctx, client, base, index); err != nil {
		return app.Result{}, err
	}
	path := base + "/issues/" + strconv.FormatInt(index, 10)
	var data any
	switch kind {
	case "comments":
		data, err = gitea.Pages[Comment](ctx, client, path+"/comments", nil, page, limit, all)
	case "labels":
		var response *gitea.Response
		response, err = client.Do(ctx, gitea.Request{Path: path + "/labels"})
		if err == nil {
			var labels []Label
			err = gitea.Decode(response, &labels)
			data = labels
		}
	default:
		err = app.Wrap(app.InvalidArgument, "Unknown related Issue resource.", nil)
	}
	if err != nil {
		return app.Result{}, err
	}
	return app.NewResult(t.Context(), []app.Item{{
		Target: strconv.FormatInt(index, 10),
		Status: app.Success,
		Data:   data,
	}}, nil), nil
}

// Catalog 返回标签或负责人候选，供交互组件使用稳定 ID 选择。
// 候选完整读取有 10000 项上限，不无限扫描仓库成员或标签。
func (s Service) Catalog(ctx context.Context, kind string) (any, error) {
	base, err := s.base()
	if err != nil {
		return nil, err
	}
	client, _, err := s.Auth.Open(ctx, s.Target, true)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	switch kind {
	case "labels":
		page, err := gitea.Pages[Label](ctx, client, base+"/labels", nil, 1, 100, true)
		return page.Items, err
	case "assignees":
		page, err := gitea.Pages[User](ctx, client, base+"/assignees", nil, 1, 100, true)
		return page.Items, err
	}
	return nil, app.Wrap(app.InvalidArgument, "Unknown Issue candidate catalog.", nil)
}

// resolveLabels 将每个名称或 ID 解析为唯一资源，防止服务器静默丢弃不存在的条件。
// 名称恰与其他数字 ID 冲突也报歧义，重复引用按 ID 去重。
func resolveLabels(ctx context.Context, client *gitea.Client, base string, references []string) ([]Label, error) {
	if len(references) == 0 {
		return []Label{}, nil
	}
	page, err := gitea.Pages[Label](ctx, client, base+"/labels", nil, 1, 100, true)
	if err != nil {
		return nil, err
	}
	result := []Label{}
	seen := map[int64]bool{}
	for _, reference := range references {
		matches := map[int64]Label{}
		for _, label := range page.Items {
			if label.Name == reference || strconv.FormatInt(label.ID, 10) == reference {
				matches[label.ID] = label
			}
		}
		if len(matches) == 0 {
			return nil, app.Wrap(app.NotFound, "A requested label does not exist in this repository.", nil)
		}
		if len(matches) != 1 {
			return nil, app.Wrap(app.ContextAmbiguous, "A label reference is ambiguous. Use an unambiguous label ID.", nil)
		}
		for id, label := range matches {
			if !seen[id] {
				seen[id] = true
				result = append(result, label)
			}
		}
	}
	return result, nil
}

// resolveMilestone 将标题或 ID 解析为唯一里程碑，不忽略不存在的筛选。
// 同时读取 open／closed 集合，明确清空由独立输入表达而不是空名称。
func resolveMilestone(ctx context.Context, client *gitea.Client, base, reference string) (int64, error) {
	page, err := gitea.Pages[Milestone](ctx, client, base+"/milestones", url.Values{"state": {"all"}}, 1, 100, true)
	if err != nil {
		return 0, err
	}
	matches := map[int64]bool{}
	for _, value := range page.Items {
		if value.Title == reference || strconv.FormatInt(value.ID, 10) == reference {
			matches[value.ID] = true
		}
	}
	if len(matches) != 1 {
		return 0, app.Wrap(app.ContextAmbiguous, "The milestone is missing or ambiguous. Use an existing unambiguous ID.", nil)
	}
	for id := range matches {
		return id, nil
	}
	return 0, app.Wrap(app.NotFound, "Milestone not found.", nil)
}

// matches 在完整查询结果上计算明确的本地筛选语义。
// 文本为不区分大小写的标题／正文子串，多标签统一采用 CLI 约定的任一匹配。
func matches(value Issue, filter Filter, labels []Label, milestone int64) bool {
	if filter.State != "all" && value.State != filter.State {
		return false
	}
	if filter.Search != "" && !strings.Contains(strings.ToLower(value.Title+"\n"+value.Body), strings.ToLower(filter.Search)) {
		return false
	}
	if filter.Assignee != "" {
		found := false
		for _, user := range value.Assignees {
			found = found || strings.EqualFold(user.Login, filter.Assignee)
		}
		if !found {
			return false
		}
	}
	if milestone != 0 && (value.Milestone == nil || value.Milestone.ID != milestone) {
		return false
	}
	if len(labels) > 0 {
		found := false
		for _, requested := range labels {
			for _, label := range value.Labels {
				found = found || requested.ID == label.ID
			}
		}
		if !found {
			return false
		}
	}
	return true
}
