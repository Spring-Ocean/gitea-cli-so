package gitea

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Pages 读取一页或显式后续页，保留次序并检测重复页和 10000 项上限。
// query 会复制后添加分页，不修改调用方；响应体在每页立即关闭。
func Pages[T any](ctx context.Context, client *Client, path string, query url.Values, page, limit int, all bool) (app.Page[T], error) {
	if page < 1 || limit < 1 || limit > 100 || page > int(^uint(0)>>1)/limit {
		return app.Page[T]{}, app.Wrap(app.InvalidArgument, "Use page >= 1 and limit between 1 and 100.", nil)
	}
	parameters := url.Values{}
	for key, values := range query {
		parameters[key] = append([]string{}, values...)
	}
	result := app.Page[T]{
		Items: []T{},
		Page:  page,
		Limit: limit,
	}
	seen := map[[32]byte]bool{}
	for {
		parameters.Set("page", strconv.Itoa(page))
		parameters.Set("limit", strconv.Itoa(limit))
		response, err := client.Do(ctx, Request{
			Path:  path,
			Query: parameters,
		})
		if err != nil {
			return result, err
		}
		link := response.Header.Get("Link")
		var current []T
		if err := Decode(response, &current); err != nil {
			return result, err
		}
		result.HasMore = len(current) == limit || strings.Contains(link, `rel="next"`)
		if len(current) == 0 {
			result.HasMore = false
		}
		if all && len(current) > 0 {
			encoded, err := json.Marshal(current)
			if err != nil {
				return result, app.Wrap(app.ServerError, "Unable to check page consistency.", err)
			}
			digest := sha256.Sum256(encoded)
			if seen[digest] {
				return result, app.Wrap(app.FilterLimit, "The server repeated a page; results are incomplete.", nil)
			}
			seen[digest] = true
		}
		result.Items = append(result.Items, current...)
		if len(result.Items) > 10000 || (all && len(result.Items) == 10000 && result.HasMore) {
			return result, app.Wrap(app.FilterLimit, "The query reached 10000 items; results are incomplete. Narrow the query.", nil)
		}
		if !all || !result.HasMore {
			return result, nil
		}
		page++
	}
}
