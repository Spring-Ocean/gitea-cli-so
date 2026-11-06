// Package issue 实现七版本共用的 Issue 读取、预览及明确提交。
// 服务不读取终端，PR 类型、配置修订和写入结果未知均在业务边界处理。
package issue

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// User 是负责人候选的稳定账号标识。
// Login 与 helper 认证用户名无关，不保存认证数据。
type User struct {
	Login string `json:"login"`
}

// Label 保存服务端标签 ID 与明确名称。
// 名称解析必须唯一，不能以模糊匹配覆盖同名资源。
type Label struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Milestone 保留里程碑 ID 与标题。
// nil 表示 Issue 无里程碑，写入清空使用明确的 0。
type Milestone struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// Issue 保存共同字段和资源类型证据，不把缺失的新字段冒充有效版本锁。
// PullRequest 非空且不为 null 时属于 PR，Issue 操作必须拒绝。
type Issue struct {
	ID             int64           `json:"id"`
	Number         int64           `json:"number"`
	Title          string          `json:"title"`
	Body           string          `json:"body"`
	State          string          `json:"state"`
	Assignees      []User          `json:"assignees"`
	Labels         []Label         `json:"labels"`
	Milestone      *Milestone      `json:"milestone"`
	PullRequest    json.RawMessage `json:"pull_request,omitempty"`
	UpdatedAt      string          `json:"updated_at"`
	ContentVersion *int64          `json:"content_version,omitempty"`
	URL            string          `json:"html_url,omitempty"`
}

// Comment 是普通 Issue 评论，不混用 PR 审查记录。
// ID 是独立评论资源编号，不能当成仓库内 Issue 编号。
type Comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User User   `json:"user"`
}

// Changes 用指针区分未提供与明确清空，集合指针指向空切片表示替换为空。
// Labels 由独立 PUT 完成，不能把其混入不支持该字段的 Issue PATCH。
type Changes struct {
	Title     *string   `json:"title,omitempty"`
	Body      *string   `json:"body,omitempty"`
	Assignees *[]string `json:"assignees,omitempty"`
	Labels    *[]int64  `json:"labels,omitempty"`
	Milestone *int64    `json:"milestone,omitempty"`
}

// Filter 描述共同筛选及分页，不含远端任意请求参数。
// ExactMatch 未知时采用完整有界读取后本地筛选，不能只过滤当前页。
type Filter struct {
	State     string
	Search    string
	Labels    []string
	Assignee  string
	Milestone string
	Page      int
	Limit     int
	All       bool
}

// Service 绑定一次已解析目标和当前配置修订，复用认证及进程内写锁。
// 每次 Commit 都重新核对修订与身份；Version 只决定有证据的请求策略。
type Service struct {
	Auth     auth.Service
	Target   target.Target
	Version  compat.Info
	Revision string
	Locks    *write.Locks
}

// base 返回已校验并逐段编码的仓库路径。
// 只允许 owner/repo，不把用户提供的 URL 当作请求目的地。
func (s Service) base() (string, error) {
	if !target.ValidRepository(s.Target.Repository) {
		return "", app.Wrap(app.ContextRequired, "A valid owner/repo is required.", nil)
	}
	parts := strings.Split(s.Target.Repository, "/")
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
}

// get 在已选客户端中读回一个 Issue，并核对编号与资源类型。
// PR 共用接口仍必须拒绝，保证后续评论或状态写入不会误作用于 PR。
func get(ctx context.Context, client *gitea.Client, base string, index int64) (Issue, error) {
	if index < 1 {
		return Issue{}, app.Wrap(app.InvalidArgument, "Issue numbers must be positive integers.", nil)
	}
	response, err := client.Do(ctx, gitea.Request{Path: base + "/issues/" + strconv.FormatInt(index, 10)})
	if err != nil {
		return Issue{}, err
	}
	var value Issue
	if err := gitea.Decode(response, &value); err != nil {
		return Issue{}, err
	}
	if value.Number != index {
		return Issue{}, app.Wrap(app.ServerError, "The issue response does not match the requested number.", nil)
	}
	if len(value.PullRequest) > 0 && string(value.PullRequest) != "null" {
		return Issue{}, app.Wrap(app.ResourceTypeMismatch, "This number belongs to a pull request. Use a PR command.", nil)
	}
	return value, nil
}
