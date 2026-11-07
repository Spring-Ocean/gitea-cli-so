// Package catalog 提供当前已验证 Gitea 账号的仓库与组织目录读取。
// 服务只调用只读 API、按页返回稳定资源 ID，不依赖 CLI、终端或持久会话。
package catalog

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Kind 表示账号浏览器中的一种只读目录。
// Repositories 指 Gitea /user/repos 文档定义的当前账号自有仓库，不声称包含全部可访问仓库。
type Kind string

const (
	// Repositories 查询已验证账号拥有的仓库。
	Repositories Kind = "repositories"
	// Organizations 查询已验证账号所属的组织。
	Organizations Kind = "organizations"
	// OrganizationRepositories 查询指定组织下的仓库。
	OrganizationRepositories Kind = "organization_repositories"
)

const (
	defaultLimit = 30
	maximumLimit = 100
)

var organizationPattern = regexp.MustCompile(`^[^/\\%?#\x00-\x20\x7f]{1,255}$`)

// Query 描述一次有界目录页；组织仓库查询须同时提供稳定组织 ID 和登录名。
// ID 防止同名组织重建后错误沿用旧选择；页码从 1 开始，每页最多 100 项。
type Query struct {
	Kind           Kind
	Organization   string
	OrganizationID int64
	Page           int
	Limit          int
}

// Item 保存仓库或组织的导航字段。
// ID 是列表稳定选择键；组织登录名与仓库 full_name 由服务端响应提供，不从本地 Git 推测。
type Item struct {
	ID          int64  `json:"id"`
	Name        string `json:"name,omitempty"`
	FullName    string `json:"full_name,omitempty"`
	Description string `json:"description,omitempty"`
	Visibility  string `json:"visibility,omitempty"`
	Private     bool   `json:"private,omitempty"`
	Fork        bool   `json:"fork,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
	OwnerLogin  string `json:"owner_login,omitempty"`
}

// Repository 是通过真实仓库接口核对的入口目标。
// 返回的 ID 和 FullName 必须同时匹配用户选中的目录项。
type Repository struct {
	ID          int64  `json:"id"`
	FullName    string `json:"full_name"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Visibility  string `json:"visibility,omitempty"`
	Private     bool   `json:"private,omitempty"`
	Fork        bool   `json:"fork,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
	OwnerLogin  string `json:"owner_login,omitempty"`
}

// Page 是服务端单页的只读结果，不合并后续页或猜测集合总数。
// HasMore 根据 Gitea Link rel=next 或满页提示，调用方可显式读取下一页。
type Page struct {
	Items   []Item `json:"items"`
	Page    int    `json:"page"`
	Limit   int    `json:"limit"`
	HasMore bool   `json:"has_more"`
}

type organizationResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Username    string `json:"username"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
}

type repositoryResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	Private     bool   `json:"private"`
	Fork        bool   `json:"fork"`
	Archived    bool   `json:"archived"`
	Owner       struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// List 读取当前验证账号的一页自有仓库、所属组织或组织仓库。
// 每次调用都重新验证实例与账号；组织仓库会先按 ID 核对组织身份，再请求其仓库页。
// ctx 取消会中止当前读取；401、403、404 和无效 JSON 保留为明确错误，不返回伪成功空页。
func List(ctx context.Context, authentication auth.Service, t target.Target, query Query) (Page, target.Target, error) {
	query, err := normalizeQuery(query)
	if err != nil {
		return Page{}, t, err
	}
	if query.Kind == OrganizationRepositories {
		if !validOrganization(query.Organization) || query.OrganizationID < 1 {
			return Page{}, t, app.Wrap(app.InvalidArgument, "Select an organization with a valid name and ID.", nil)
		}
	} else if query.Organization != "" || query.OrganizationID != 0 {
		return Page{}, t, app.Wrap(app.InvalidArgument, "Organization fields are only valid for an organization repository listing.", nil)
	}
	client, verified, err := openVerified(ctx, authentication, t)
	if err != nil {
		return Page{}, t, err
	}
	defer client.Close()
	if query.Kind == OrganizationRepositories {
		if err := verifyOrganization(ctx, client, query.OrganizationID, query.Organization); err != nil {
			return Page{}, t, err
		}
	}
	path, err := listPath(query)
	if err != nil {
		return Page{}, t, err
	}
	response, err := client.Do(ctx, gitea.Request{
		Path: path,
		Query: url.Values{
			"page":  {strconv.Itoa(query.Page)},
			"limit": {strconv.Itoa(query.Limit)},
		},
	})
	if err != nil {
		return Page{}, verified, err
	}
	if query.Kind == Organizations {
		var values []organizationResponse
		if err := gitea.Decode(response, &values); err != nil {
			return Page{}, verified, err
		}
		if len(values) > query.Limit {
			return Page{}, verified, app.Wrap(app.ResponseTooLarge, "The server returned more organizations than the requested page limit.", nil)
		}
		items := make([]Item, 0, len(values))
		seenIDs := make(map[int64]struct{}, len(values))
		for _, value := range values {
			item, err := mapOrganization(value)
			if err != nil {
				return Page{}, verified, err
			}
			if _, exists := seenIDs[item.ID]; exists {
				return Page{}, verified, app.Wrap(app.ServerError, "The organization response contains a duplicate stable ID.", nil)
			}
			seenIDs[item.ID] = struct{}{}
			items = append(items, item)
		}
		return Page{
			Items:   items,
			Page:    query.Page,
			Limit:   query.Limit,
			HasMore: hasNext(response.Header.Get("Link"), len(items), query.Limit),
		}, verified, nil
	}
	var values []repositoryResponse
	if err := gitea.Decode(response, &values); err != nil {
		return Page{}, verified, err
	}
	if len(values) > query.Limit {
		return Page{}, verified, app.Wrap(app.ResponseTooLarge, "The server returned more repositories than the requested page limit.", nil)
	}
	items := make([]Item, 0, len(values))
	seenIDs := make(map[int64]struct{}, len(values))
	for _, value := range values {
		repository, err := mapRepository(value)
		if err != nil {
			return Page{}, verified, err
		}
		if _, exists := seenIDs[repository.ID]; exists {
			return Page{}, verified, app.Wrap(app.ServerError, "The repository response contains a duplicate stable ID.", nil)
		}
		seenIDs[repository.ID] = struct{}{}
		item := repositoryItem(repository)
		items = append(items, item)
	}
	return Page{
		Items:   items,
		Page:    query.Page,
		Limit:   query.Limit,
		HasMore: hasNext(response.Header.Get("Link"), len(items), query.Limit),
	}, verified, nil
}

// GetOrganization 按所选稳定 ID 再查组织详情，防止同名组织被重建后误入。
// 只有 ID 与 name／username 都一致才返回信息；认证、权限及组织不可见错误原样保留。
func GetOrganization(ctx context.Context, authentication auth.Service, t target.Target, expectedID int64, name string) (Item, target.Target, error) {
	if expectedID < 1 || !validOrganization(name) {
		return Item{}, t, app.Wrap(app.InvalidArgument, "Select an organization with a valid name and ID.", nil)
	}
	client, verified, err := openVerified(ctx, authentication, t)
	if err != nil {
		return Item{}, t, err
	}
	defer client.Close()
	response, err := client.Do(ctx, gitea.Request{
		Path: "/orgs/" + url.PathEscape(name),
	})
	if err != nil {
		return Item{}, verified, err
	}
	var value organizationResponse
	if err := gitea.Decode(response, &value); err != nil {
		return Item{}, verified, err
	}
	item, err := mapOrganization(value)
	if err != nil {
		return Item{}, verified, err
	}
	if item.ID != expectedID || !strings.EqualFold(item.Name, name) {
		return Item{}, verified, app.Wrap(app.PreconditionChanged, "The selected organization changed. Refresh the organization list.", nil)
	}
	return item, verified, nil
}

// GetRepository 通过仓库 API 核对所选 ID 和 full_name 后返回进入工作台的目标。
// 调用仍使用原实例和已验证账号；成功后清除旧本地仓库／remote／credential，避免误用工作目录来源。
func GetRepository(ctx context.Context, authentication auth.Service, t target.Target, expectedID int64, fullName string) (Repository, target.Target, error) {
	if expectedID < 1 || !target.ValidRepository(fullName) {
		return Repository{}, t, app.Wrap(app.InvalidArgument, "Select a repository with a valid ID and owner/repo name.", nil)
	}
	client, verified, err := openVerified(ctx, authentication, t)
	if err != nil {
		return Repository{}, t, err
	}
	defer client.Close()
	parts := strings.Split(fullName, "/")
	response, err := client.Do(ctx, gitea.Request{
		Path: "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]),
	})
	if err != nil {
		return Repository{}, verified, err
	}
	var value repositoryResponse
	if err := gitea.Decode(response, &value); err != nil {
		return Repository{}, verified, err
	}
	repository, err := mapRepository(value)
	if err != nil {
		return Repository{}, verified, err
	}
	if repository.ID != expectedID || !strings.EqualFold(repository.FullName, fullName) {
		return Repository{}, verified, app.Wrap(app.PreconditionChanged, "The selected repository changed. Refresh the repository list.", nil)
	}
	verified.Repository = repository.FullName
	verified.Remote = ""
	verified.Local = nil
	verified.LocalMatches = false
	verified.Credential = git.Credential{}
	verified.Sources = cloneSources(verified.Sources)
	if verified.Sources == nil {
		verified.Sources = map[string]string{}
	}
	verified.Sources["repo"] = "account_catalog"
	verified.Sources["remote"] = "unselected"
	return repository, verified, nil
}

// openVerified 为每次目录操作创建本次认证客户端，拒绝未确认实例。
// 返回的已验证账号固定到结果目标；客户端调用方必须关闭，错误结果不允许继续使用。
func openVerified(ctx context.Context, authentication auth.Service, t target.Target) (*gitea.Client, target.Target, error) {
	if !t.Registered {
		return nil, t, app.Wrap(app.AuthRequired, "Confirm the selected Gitea instance and sign in before browsing account repositories.", nil)
	}
	if t.Account == "" {
		return nil, t, app.Wrap(app.AuthRequired, "Select a verified account before browsing account repositories.", nil)
	}
	client, verified, err := authentication.Open(ctx, t, false)
	if err != nil {
		return nil, verified, err
	}
	if verified.Account == "" || !strings.EqualFold(verified.Account, t.Account) {
		client.Close()
		return nil, verified, app.Wrap(app.IdentityMismatch, "The selected API account could not be verified.", nil)
	}
	return client, verified, nil
}

// normalizeQuery 固定服务端分页边界，避免因遍历所有页造成无界请求。
// page=0 和 limit=0 代表使用默认值；负页码、非正 limit 及超过上限的 limit 返回参数错误。
func normalizeQuery(query Query) (Query, error) {
	if query.Page == 0 {
		query.Page = 1
	}
	if query.Limit == 0 {
		query.Limit = defaultLimit
	}
	if query.Page < 1 || query.Limit < 1 || query.Limit > maximumLimit {
		return Query{}, app.Wrap(app.InvalidArgument, "Use page >= 1 and limit between 1 and 100.", nil)
	}
	switch query.Kind {
	case Repositories, Organizations, OrganizationRepositories:
		return query, nil
	default:
		return Query{}, app.Wrap(app.InvalidArgument, "Choose repositories, organizations, or organization repositories.", nil)
	}
}

// listPath 仅从已验证枚举拼接 API 路径，组织名按单个路径段编码。
// 未知 kind 不得成为通用端点入口；原始 API 命令负责其他用户选定路径。
func listPath(query Query) (string, error) {
	switch query.Kind {
	case Repositories:
		return "/user/repos", nil
	case Organizations:
		return "/user/orgs", nil
	case OrganizationRepositories:
		return "/orgs/" + url.PathEscape(query.Organization) + "/repos", nil
	default:
		return "", app.Wrap(app.InvalidArgument, "Unknown account directory kind.", nil)
	}
}

// verifyOrganization 在读取该组织仓库前重新核对稳定组织 ID。
// 名称复用或组织删除会返回可识别冲突，组织成员不可见仍由服务端权限判断。
func verifyOrganization(ctx context.Context, client *gitea.Client, expectedID int64, name string) error {
	response, err := client.Do(ctx, gitea.Request{
		Path: "/orgs/" + url.PathEscape(name),
	})
	if err != nil {
		return err
	}
	var value organizationResponse
	if err := gitea.Decode(response, &value); err != nil {
		return err
	}
	item, err := mapOrganization(value)
	if err != nil {
		return err
	}
	if item.ID != expectedID || !strings.EqualFold(item.Name, name) {
		return app.Wrap(app.PreconditionChanged, "The selected organization changed. Refresh the organization list.", nil)
	}
	return nil
}

// mapOrganization 合并新版本 name 和旧版本 username 字段为稳定登录名。
// 必须存在正数 ID 及有效单段名称，不能回退到列表下标当选择键。
func mapOrganization(value organizationResponse) (Item, error) {
	name := strings.TrimSpace(value.Name)
	if name == "" {
		name = strings.TrimSpace(value.Username)
	}
	if value.ID < 1 || !validOrganization(name) {
		return Item{}, app.Wrap(app.ServerError, "The organization response does not contain a stable ID and name.", nil)
	}
	return Item{
		ID:          value.ID,
		Name:        name,
		FullName:    value.FullName,
		Description: value.Description,
		Visibility:  value.Visibility,
	}, nil
}

// mapRepository 提取所有支持版本均具备的仓库导航字段。
// full_name 和 ID 缺失、格式异常时返回服务端错误，避免进入同名或错误仓库。
func mapRepository(value repositoryResponse) (Repository, error) {
	if value.ID < 1 || !target.ValidRepository(value.FullName) {
		return Repository{}, app.Wrap(app.ServerError, "The repository response does not contain a stable ID and owner/repo name.", nil)
	}
	return Repository{
		ID:          value.ID,
		Name:        value.Name,
		FullName:    value.FullName,
		Description: value.Description,
		Visibility:  value.Visibility,
		Private:     value.Private,
		Fork:        value.Fork,
		Archived:    value.Archived,
		OwnerLogin:  value.Owner.Login,
	}, nil
}

// repositoryItem 将仓库详情投影为目录中的可选择字段。
// 页面不暴露详情专有状态，ID 与 full_name 保留作为后续二次核验键。
func repositoryItem(repository Repository) Item {
	return Item{
		ID:          repository.ID,
		Name:        repository.Name,
		FullName:    repository.FullName,
		Description: repository.Description,
		Visibility:  repository.Visibility,
		Private:     repository.Private,
		Fork:        repository.Fork,
		Archived:    repository.Archived,
		OwnerLogin:  repository.OwnerLogin,
	}
}

// cloneSources 复制非敏感上下文来源，避免返回目标修改调用方共享 map。
// 来源值用于展示解析依据；nil 输入保持 nil，由调用方按需初始化。
func cloneSources(sources map[string]string) map[string]string {
	if sources == nil {
		return nil
	}
	cloned := make(map[string]string, len(sources))
	for key, value := range sources {
		cloned[key] = value
	}
	return cloned
}

// validOrganization 检查组织登录名是无分隔符的单个路径段。
// 所有端点同时使用 PathEscape，控制字符和路径注入输入不能成为请求 URL。
func validOrganization(name string) bool {
	if !organizationPattern.MatchString(name) || name == "." || name == ".." {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

// hasNext 保留服务器分页 Link 证据，并为无 Link 的兼容版本使用满页提示。
// 空页不会宣称还有下一页；调用方只请求当前一页，不自动扫描整个组织。
func hasNext(link string, count, limit int) bool {
	if count == 0 {
		return false
	}
	for _, relation := range strings.Split(link, ",") {
		for _, attribute := range strings.Split(relation, ";")[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(attribute), "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "rel") &&
				strings.Trim(strings.TrimSpace(value), "\"") == "next" {
				return true
			}
		}
	}
	return count == limit
}
