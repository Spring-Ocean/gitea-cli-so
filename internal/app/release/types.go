// Package release 实现标签、Release 与附件各自的生命周期。
// 不把草稿创建、附件上传和发布合成隐含事务，也不自动重复执行未知结果。
package release

import (
	"context"
	"net/url"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Release 保存独立的 Release ID、标签及草稿／预发布状态。
// Tag 仅用于解析 ID，不能将数字标签名直接作为 Release ID。
type Release struct {
	ID          int64   `json:"id"`
	Tag         string  `json:"tag_name"`
	Title       string  `json:"name"`
	Body        string  `json:"body"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	Target      string  `json:"target_commitish"`
	PublishedAt string  `json:"published_at"`
	Assets      []Asset `json:"assets"`
}

// Asset 保存服务端附件身份及名称／字节数，用于同名检查和上传回读。
// 不将下载地址当作上传目标或跨来源转发认证。
type Asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Service 复用固定上下文和身份边界，供 CLI、菜单及 MCP 直接调用。
// 写操作要求精确已研究版本和准备计划，服务内不进行终端交互。
type Service struct {
	write.Service
}

// Filter 保存 Release 名称／标签的本地筛选与有界分页。
// 多版本不依赖只有新版具备的 tag_filter，筛选先覆盖全集再分页。
type Filter struct {
	Search string
	Page   int
	Limit  int
	All    bool
}

// List 读取 Release，搜索采用完整有界集合上的标签／名称子串匹配。
// 草稿是否可见由服务器当前身份决定，has_more 不代表总数已知。
func (s Service) List(ctx context.Context, filter Filter) (app.Result, error) {
	if filter.Page < 1 || filter.Limit < 1 || filter.Limit > 100 || filter.Page > int(^uint(0)>>1)/filter.Limit {
		return app.Result{}, app.Wrap(app.InvalidArgument, "Use page >= 1 and limit between 1 and 100.", nil)
	}
	connection, err := s.Open(ctx, true)
	if err != nil {
		return app.Result{}, err
	}
	defer connection.Close()
	page, limit, all := filter.Page, filter.Limit, filter.All
	if filter.Search != "" {
		page, limit, all = 1, 100, true
	}
	found, err := gitea.Pages[Release](ctx, connection.Client, connection.Base+"/releases", nil, page, limit, all)
	if err != nil {
		return app.Result{}, err
	}
	if filter.Search != "" {
		values := []Release{}
		for _, value := range found.Items {
			if strings.Contains(strings.ToLower(value.Tag+"\n"+value.Title), strings.ToLower(filter.Search)) {
				values = append(values, value)
			}
		}
		start := (filter.Page - 1) * filter.Limit
		if start > len(values) {
			start = len(values)
		}
		end := len(values)
		if !filter.All && start+filter.Limit < end {
			end = start + filter.Limit
		}
		found = app.Page[Release]{
			Items:   values[start:end],
			Page:    filter.Page,
			Limit:   filter.Limit,
			HasMore: end < len(values),
		}
	}
	return app.NewResult(connection.Target.Context(), []app.Item{{
		Target: connection.Target.Repository,
		Status: app.Success,
		Data:   found,
	}}, nil), nil
}

// View 按一个完整标签名查找 Release 并核对返回标签与正 ID。
// 标签按路径段编码，包含斜杠和 Unicode 时仍不能被解释成其他资源路径。
func (s Service) View(ctx context.Context, tag string) (app.Result, error) {
	connection, err := s.Open(ctx, true)
	if err != nil {
		return app.Result{}, err
	}
	defer connection.Close()
	value, err := get(ctx, connection, tag)
	if err != nil {
		return app.Result{}, err
	}
	return app.NewResult(connection.Target.Context(), []app.Item{{
		Target: tag,
		Status: app.Success,
		Data:   value,
	}}, nil), nil
}

// get 校验完整标签名并解析服务端 Release ID，不接受隐式数字 ID。
// 404 保留资源缺失或权限不可见，不据此切换账号或版本。
func get(ctx context.Context, connection *write.Connection, tag string) (Release, error) {
	if !validTag(tag) {
		return Release{}, app.Wrap(app.InvalidArgument, "Provide an explicit valid release tag.", nil)
	}
	var value Release
	if err := write.Read(ctx, connection.Client, connection.Base+"/releases/tags/"+url.PathEscape(tag), &value); err != nil {
		return Release{}, err
	}
	if value.ID < 1 || value.Tag != tag {
		return Release{}, app.Wrap(app.ServerError, "The release response does not match the requested tag.", nil)
	}
	return value, nil
}
