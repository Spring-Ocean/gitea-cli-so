// Package pullrequest 实现独立 PR 服务，复用认证、引用目录和写入契约。
// 不通过 Issue 命令操作 PR；普通评论及里程碑清空使用共享 HTTP 资源端点。
package pullrequest

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Repository 保存来源仓库及服务器允许的合并方式。
// FullName 是 owner/repo，不从 URL 推测账号或自动选取另一个 fork。
type Repository struct {
	ID               int64  `json:"id"`
	FullName         string `json:"full_name"`
	DefaultBranch    string `json:"default_branch"`
	AllowMerge       bool   `json:"allow_merge_commits"`
	AllowSquash      bool   `json:"allow_squash_merge"`
	AllowRebase      bool   `json:"allow_rebase"`
	AllowRebaseMerge bool   `json:"allow_rebase_explicit"`
	AllowFastForward bool   `json:"allow_fast_forward_only_merge"`
	Permissions      struct {
		Admin bool `json:"admin"`
		Push  bool `json:"push"`
	} `json:"permissions"`
}

// Branch 是 PR 的明确仓库、分支和已读取的 SHA。
// 合并与审查按 SHA 固定提交，不能把本地未推送提交视作远端分支。
type Branch struct {
	Ref  string     `json:"ref"`
	SHA  string     `json:"sha"`
	Repo Repository `json:"repo"`
}

// PullRequest 区分 PR 合并状态、源目标分支及共同 Issue 字段。
// Issue 内嵌仅共享数据模型；PR 类型通过 pulls 端点和分支证据验证。
type PullRequest struct {
	issue.Issue
	Head               Branch       `json:"head"`
	Base               Branch       `json:"base"`
	Merged             bool         `json:"merged"`
	Mergeable          bool         `json:"mergeable"`
	User               issue.User   `json:"user"`
	RequestedReviewers []issue.User `json:"requested_reviewers"`
}

// Filter 保存共同筛选和有界分页，不依赖版本特有的服务端搜索。
// 关键词、base 和 author 先完整扫描再过滤，避免只过滤当前页而漏项。
type Filter struct {
	State    string
	Search   string
	Base     string
	Author   string
	Page     int
	Limit    int
	All      bool
	Unmerged bool
}

// Service 共享固定实例与认证边界，提供 PR 读取和单次准备／提交。
// 所有写入均要求七个已研究精确版本；调用方负责最终人机确认。
type Service struct {
	write.Service
}

// get 读取并校验 PR 编号和分支证据，普通 Issue 编号给出类型错误。
// pulls 返回 404 时仅做只读类型核对，权限不可见仍保留原始 404。
func get(ctx context.Context, connection *write.Connection, number int64) (PullRequest, error) {
	if number < 1 {
		return PullRequest{}, app.Wrap(app.InvalidArgument, "PR numbers must be positive integers.", nil)
	}
	var value PullRequest
	path := connection.Base + "/pulls/" + strconv.FormatInt(number, 10)
	err := write.Read(ctx, connection.Client, path, &value)
	if err != nil {
		if app.Failure(err).HTTPStatus == http.StatusNotFound {
			var common issue.Issue
			checkErr := write.Read(ctx, connection.Client, connection.Base+"/issues/"+strconv.FormatInt(number, 10), &common)
			if checkErr == nil && common.Number == number && (len(common.PullRequest) == 0 || string(common.PullRequest) == "null") {
				return PullRequest{}, app.Wrap(app.ResourceTypeMismatch, "This number belongs to an issue. Use an Issue command.", nil)
			}
		}
		return PullRequest{}, err
	}
	if value.Number != number || value.Head.SHA == "" || value.Base.Ref == "" {
		return PullRequest{}, app.Wrap(app.ServerError, "The PR response does not match the requested resource.", nil)
	}
	return value, nil
}

// pageResult 保留一页或完整有界集合及实际上下文，不猜测资源总数。
// 空列表成功；查询错误由调用方在构造结果前返回。
func pageResult[T any](connection *write.Connection, page app.Page[T]) app.Result {
	return app.NewResult(connection.Target.Context(), []app.Item{{
		Target: connection.Target.Repository,
		Status: app.Success,
		Data:   page,
	}}, nil)
}

// relatedPage 读取一种已验证 PR 的关联资源并立即关闭各页响应。
// 泛型数据只在当前业务边界使用，不把普通讨论和审查混成一个接口。
func relatedPage(ctx context.Context, connection *write.Connection, path string, page, limit int, all bool) (app.Result, error) {
	result, err := gitea.Pages[map[string]any](ctx, connection.Client, path, nil, page, limit, all)
	if err != nil {
		return app.Result{}, err
	}
	return pageResult(connection, result), nil
}
