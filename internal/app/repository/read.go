// Package repository 提供开发上下文的只读应用服务。
// CLI 与 MCP 可直接复用，不依赖终端输入或命令文本解析。
package repository

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Query 描述一个仓库只读操作与受限分页。
// Ref 仅用于 commit.status，默认 HEAD 必须由入口提供已解析的明确 SHA。
type Query struct {
	Operation string
	Page      int
	Limit     int
	All       bool
	Ref       string
}

// Read 验证身份后执行固定只读操作，列表默认一页且最多扫描 10000 项。
// 返回原始业务字段但保留数字精度；权限错误不解释成版本不支持。
func Read(ctx context.Context, authentication auth.Service, t target.Target, query Query) (app.Result, error) {
	if !target.ValidRepository(t.Repository) {
		return app.Result{}, app.Wrap(app.ContextRequired, "A valid owner/repo target is required.", nil)
	}
	if query.Page < 1 || query.Limit < 1 || query.Limit > 100 {
		return app.Result{}, app.Wrap(app.InvalidArgument, "Use page >= 1 and limit between 1 and 100.", nil)
	}
	parts := strings.Split(t.Repository, "/")
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	list := true
	switch query.Operation {
	case "repo.view":
		list = false
	case "branch.list":
		path += "/branches"
	case "tag.list":
		path += "/tags"
	case "commit.list":
		path += "/commits"
	case "commit.status":
		if query.Ref == "" {
			return app.Result{}, app.Wrap(app.InvalidArgument, "Specify a ref or use a mapped local repository with a valid HEAD.", nil)
		}
		path += "/commits/" + url.PathEscape(query.Ref) + "/status"
		list = false
	case "assignee.list":
		path += "/assignees"
	case "label.list":
		path += "/labels"
	case "milestone.list":
		path += "/milestones"
	default:
		return app.Result{}, app.Wrap(app.InvalidArgument, "Unknown repository read operation.", nil)
	}
	client, t, err := authentication.Open(ctx, t, true)
	if err != nil {
		return app.Result{}, err
	}
	defer client.Close()
	if !list {
		response, err := client.Do(ctx, gitea.Request{Path: path})
		if err != nil {
			return app.Result{}, err
		}
		var value map[string]any
		if err := gitea.Decode(response, &value); err != nil {
			return app.Result{}, err
		}
		return app.NewResult(t.Context(), []app.Item{{
			Target: t.Repository,
			Status: app.Success,
			Data:   value,
		}}, nil), nil
	}
	values := []map[string]any{}
	seen := map[string]bool{}
	page := query.Page
	hasMore := false
	for {
		response, err := client.Do(ctx, gitea.Request{
			Path:  path,
			Query: url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(query.Limit)}},
		})
		if err != nil {
			return app.Result{}, err
		}
		nextLink := strings.Contains(response.Header.Get("Link"), `rel="next"`)
		var current []map[string]any
		if err := gitea.Decode(response, &current); err != nil {
			return app.Result{}, err
		}
		hasMore = nextLink || len(current) == query.Limit
		if len(current) == 0 {
			hasMore = false
		}
		if query.All && len(current) > 0 {
			encoded, err := json.Marshal(current)
			if err != nil {
				return app.Result{}, app.Wrap(app.ServerError, "Unable to track repository pagination.", err)
			}
			if seen[string(encoded)] {
				return app.Result{}, app.Wrap(app.ServerError, "The server repeated a page; results are incomplete.", nil)
			}
			seen[string(encoded)] = true
		}
		values = append(values, current...)
		if len(values) > 10000 || (len(values) == 10000 && hasMore && query.All) {
			return app.Result{}, app.Wrap(app.ResponseTooLarge, "The scan reached 10000 items; results are incomplete. Narrow the query.", nil)
		}
		if !query.All || !hasMore {
			break
		}
		page++
	}
	data := app.Page[map[string]any]{
		Items:   values,
		Page:    query.Page,
		Limit:   query.Limit,
		HasMore: hasMore,
	}
	return app.NewResult(t.Context(), []app.Item{{
		Target: t.Repository,
		Status: app.Success,
		Data:   data,
	}}, nil), nil
}
