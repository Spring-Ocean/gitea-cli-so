package pullrequest

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// List 读取 PR；需要业务筛选时在完整有界结果上筛选后分页。
// State 仅接受 open／closed／all；Unmerged 供状态及合并选择器排除已合并对象。
func (s Service) List(ctx context.Context, filter Filter) (app.Result, error) {
	if (filter.State != "open" && filter.State != "closed" && filter.State != "all") || filter.Page < 1 || filter.Limit < 1 || filter.Limit > 100 || filter.Page > int(^uint(0)>>1)/filter.Limit {
		return app.Result{}, app.Wrap(app.InvalidArgument, "Use a valid PR state, page >= 1, and limit between 1 and 100.", nil)
	}
	connection, err := s.Open(ctx, true)
	if err != nil {
		return app.Result{}, err
	}
	defer connection.Close()
	local := filter.Search != "" || filter.Base != "" || filter.Author != "" || filter.Unmerged
	page, limit, all := filter.Page, filter.Limit, filter.All
	if local {
		page, limit, all = 1, 100, true
	}
	found, err := gitea.Pages[PullRequest](ctx, connection.Client, connection.Base+"/pulls", url.Values{"state": {filter.State}}, page, limit, all)
	if err != nil {
		return app.Result{}, err
	}
	if !local {
		return pageResult(connection, found), nil
	}
	values := []PullRequest{}
	for _, value := range found.Items {
		if filter.Unmerged && value.Merged {
			continue
		}
		if filter.Base != "" && value.Base.Ref != filter.Base {
			continue
		}
		if filter.Author != "" && !strings.EqualFold(value.User.Login, filter.Author) {
			continue
		}
		if filter.Search != "" && !strings.Contains(strings.ToLower(value.Title+"\n"+value.Body), strings.ToLower(filter.Search)) {
			continue
		}
		values = append(values, value)
	}
	start := (filter.Page - 1) * filter.Limit
	if start > len(values) {
		start = len(values)
	}
	end := len(values)
	if !filter.All && start+filter.Limit < end {
		end = start + filter.Limit
	}
	return pageResult(connection, app.Page[PullRequest]{
		Items:   values[start:end],
		Page:    filter.Page,
		Limit:   filter.Limit,
		HasMore: end < len(values),
	}), nil
}

// View 读取一个明确 PR，不允许普通 Issue 编号冒充。
// 可见性和权限仍由服务器裁决，匿名读取不跳过已提供身份的验证。
func (s Service) View(ctx context.Context, number int64) (app.Result, error) {
	connection, err := s.Open(ctx, true)
	if err != nil {
		return app.Result{}, err
	}
	defer connection.Close()
	value, err := get(ctx, connection, number)
	if err != nil {
		return app.Result{}, err
	}
	return app.NewResult(connection.Target.Context(), []app.Item{{
		Target: strconv.FormatInt(number, 10),
		Status: app.Success,
		Data:   value,
	}}, nil), nil
}

// Related 先验证 PR，再分页读取文件、提交、普通评论或审查。
// 普通评论明确使用共享 issues 资源，不能调用 Issue 命令绕过 PR 类型校验。
func (s Service) Related(ctx context.Context, number int64, kind string, page, limit int, all bool) (app.Result, error) {
	connection, err := s.Open(ctx, true)
	if err != nil {
		return app.Result{}, err
	}
	defer connection.Close()
	if _, err := get(ctx, connection, number); err != nil {
		return app.Result{}, err
	}
	path := connection.Base + "/pulls/" + strconv.FormatInt(number, 10)
	switch kind {
	case "files", "commits", "reviews":
		path += "/" + kind
	case "comments":
		path = connection.Base + "/issues/" + strconv.FormatInt(number, 10) + "/comments"
	default:
		return app.Result{}, app.Wrap(app.InvalidArgument, "Unknown PR related resource.", nil)
	}
	return relatedPage(ctx, connection, path, page, limit, all)
}

// Candidates 返回创建 PR 的仓库默认分支及已有负责人候选。
// 只查询固定仓库，不枚举全站账号或推测其他用户的 fork。
func (s Service) Candidates(ctx context.Context) (Repository, error) {
	connection, err := s.Open(ctx, true)
	if err != nil {
		return Repository{}, err
	}
	defer connection.Close()
	var value Repository
	err = write.Read(ctx, connection.Client, connection.Base, &value)
	return value, err
}
