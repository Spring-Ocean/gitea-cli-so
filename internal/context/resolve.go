// Package context 按独立维度解析本地仓库、实例和账号。
// 解析只读取 Git 与非敏感配置，不读取密钥、不发送网络请求。
package context

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
)

// Input 保存入口显式提供的上下文，空值才允许使用较低优先级来源。
// RequireRepo 控制是否必须解析 owner/repo；纯实例诊断不需要本地 Git。
type Input struct {
	Workdir     string
	Instance    string
	Account     string
	Repo        string
	Remote      string
	RequireRepo bool
	NoLocal     bool
}

// Target 是完成来源解析后的非敏感目标及内部 Git 上下文。
// Local 和 Credential 不可直接序列化；Registered=false 必须先确认来源再取凭证。
type Target struct {
	Name         string            `json:"instance"`
	Instance     config.Instance   `json:"-"`
	Repository   string            `json:"repo,omitempty"`
	Account      string            `json:"account,omitempty"`
	Workdir      string            `json:"workdir"`
	Remote       string            `json:"remote,omitempty"`
	Sources      map[string]string `json:"sources"`
	Registered   bool              `json:"registered"`
	Local        *git.Repository   `json:"-"`
	Credential   git.Credential    `json:"-"`
	LocalMatches bool              `json:"-"`
}

// Context 返回可用于业务 envelope 的稳定标识。
// 不把本地路径、remote URL 或凭证上下文混入公共身份字段。
func (t Target) Context() app.Context {
	return app.Context{
		Instance:   t.Name,
		Repository: t.Repository,
		Account:    t.Account,
	}
}

// ChoiceError 给交互入口提供明确候选，非交互入口仍按稳定错误返回。
// Kind 为 remote、instance 或 account；Candidates 已排序且不含凭证。
type ChoiceError struct {
	Failure    *app.Error
	Kind       string
	Candidates []string
}

// Error 返回可公开的歧义提示，不附带候选地址或底层原因。
// 入口使用 Candidates 展示选择；机器调用可以直接要求显式参数。
func (e *ChoiceError) Error() string {
	return e.Failure.Message
}

// Unwrap 保留应用错误类别供 errors.As 及退出码识别。
// 候选选择不会改变配置，保存行为由显式账号操作承担。
func (e *ChoiceError) Unwrap() error {
	return e.Failure
}

// NormalizeURL 校验完整 HTTP 实例根地址并生成来源比较键。
// 保留端口和代理子路径，拒绝认证、查询、fragment 及路径逃逸；不联网。
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", app.Wrap(app.InvalidArgument, "Use an HTTP(S) instance root URL without credentials, query, or fragment.", nil)
	}
	decoded := u.Path
	for i := 0; i < 5; i++ {
		if strings.ContainsAny(decoded, "\\\r\n\x00") {
			return "", app.Wrap(app.InvalidArgument, "Invalid instance URL path.", nil)
		}
		for _, part := range strings.Split(decoded, "/") {
			if part == "." || part == ".." {
				return "", app.Wrap(app.InvalidArgument, "Instance paths must not contain traversal segments.", nil)
			}
		}
		next, decodeErr := url.PathUnescape(decoded)
		if decodeErr != nil {
			return "", app.Wrap(app.InvalidArgument, "Invalid instance path encoding.", nil)
		}
		if next == decoded {
			u.Scheme = strings.ToLower(u.Scheme)
			u.Host = strings.ToLower(u.Host)
			u.Path = strings.TrimRight(u.Path, "/")
			u.RawPath = ""
			return u.String(), nil
		}
		decoded = next
	}
	return "", app.Wrap(app.InvalidArgument, "Instance path encoding is too deeply nested.", nil)
}

// ValidRepository 校验 owner/repo 的两个独立路径段。
// 不接受 URL、路径穿越或编码后的分隔符，业务请求仍逐段编码。
func ValidRepository(repo string) bool {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\%?#\r\n\x00") {
			return false
		}
	}
	return true
}

// Resolve 按 CLI 契约逐维选择目标，不默认优先 origin 或上游仓库。
// 显式实例与仓库同时完整时可跨仓库；缺失或歧义返回可操作错误且不改变配置。
func Resolve(ctx context.Context, input Input, settings config.Config, runner git.Runner, getenv func(string) string) (Target, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	directory := input.Workdir
	if directory == "" {
		directory = "."
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return Target{}, app.Wrap(app.InvalidArgument, "Invalid working directory.", err)
	}
	if actual, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = actual
	}
	t := Target{
		Workdir: absolute,
		Sources: map[string]string{},
	}
	var local git.Repository
	var discoverErr error
	if !input.NoLocal {
		version, versionErr := runner.Version(ctx)
		switch {
		case versionErr != nil:
			discoverErr = versionErr
		case version.Major < 2 || (version.Major == 2 && version.Minor < 43):
			discoverErr = app.Wrap(app.GitUnavailable, "Repository discovery requires Git 2.43 or newer. Upgrade Git or supply an explicit API context.", nil)
		default:
			local, discoverErr = runner.Discover(ctx, absolute)
		}
	} else {
		discoverErr = app.Wrap(app.ContextRequired, "Local context is disabled.", nil)
	}
	if discoverErr == nil {
		t.Local = &local
	} else if !errors.Is(discoverErr, context.Canceled) {
		failure := app.Failure(discoverErr)
		if failure.Code != app.ContextRequired && failure.Code != app.GitUnavailable {
			return Target{}, discoverErr
		}
	} else {
		return Target{}, discoverErr
	}
	var binding config.Binding
	if t.Local != nil {
		for _, candidate := range settings.Repositories {
			path := candidate.Path
			if actual, pathErr := filepath.EvalSymlinks(path); pathErr == nil {
				path = actual
			}
			if filepath.Clean(path) == filepath.Clean(local.Root) {
				binding = candidate
				break
			}
		}
	}
	remoteName := input.Remote
	if remoteName != "" {
		t.Sources["remote"] = "argument"
	} else if binding.Remote != "" {
		remoteName = binding.Remote
		t.Sources["remote"] = "binding"
	}
	var selected *git.Remote
	if t.Local != nil {
		if remoteName == "" && len(local.Remotes) == 1 {
			remoteName = local.Remotes[0].Name
			t.Sources["remote"] = "unique_remote"
		}
		if remoteName == "" && len(local.Remotes) > 1 && !(input.Instance != "" && input.Repo != "") {
			choices := []string{}
			for _, remote := range local.Remotes {
				choices = append(choices, remote.Name)
			}
			return Target{}, choice("remote", choices)
		}
		for i := range local.Remotes {
			if local.Remotes[i].Name == remoteName {
				selected = &local.Remotes[i]
			}
		}
	}
	if remoteName != "" && selected == nil {
		return Target{}, app.Wrap(app.ContextRequired, "The selected remote does not exist in this repository.", nil)
	}
	var remoteRoot, remoteRepo string
	if selected != nil {
		if len(selected.URLs) != 1 {
			return Target{}, app.Wrap(app.ContextAmbiguous, "The selected remote has multiple URLs. Configure one unambiguous fetch URL.", nil)
		}
		t.Remote = selected.Name
		remoteRoot, remoteRepo, t.Credential, err = parseRemote(selected.URLs[0], settings)
		if err != nil && !(input.Instance != "" && input.Repo != "") {
			return Target{}, err
		}
	}
	t.Repository = input.Repo
	t.Sources["repo"] = "argument"
	if t.Repository == "" {
		t.Repository = remoteRepo
		t.Sources["repo"] = "remote"
	}
	instanceName := input.Instance
	t.Sources["instance"] = "argument"
	if instanceName == "" {
		if discoverErr != nil && app.Failure(discoverErr).Code == app.GitUnavailable {
			return Target{}, discoverErr
		}
		instanceName = getenv("SOTEA_INSTANCE")
		t.Sources["instance"] = "environment"
	}
	if instanceName == "" && binding.Instance != "" {
		instanceName = binding.Instance
		t.Sources["instance"] = "binding"
	}
	if instanceName == "" && remoteRoot != "" {
		instanceName = remoteRoot
		t.Sources["instance"] = "remote"
	}
	if instanceName == "" && selected == nil && binding.Instance == "" {
		instanceName = settings.DefaultInstance
		t.Sources["instance"] = "default"
	}
	if instanceName == "" {
		return Target{}, app.Wrap(app.ContextRequired, "Specify --instance with a registered name or HTTP(S) root URL.", nil)
	}
	instance, ok := settings.Instances[instanceName]
	if ok {
		t.Name, t.Instance, t.Registered = instanceName, instance, true
	} else {
		root, normalizeErr := NormalizeURL(instanceName)
		if normalizeErr != nil {
			return Target{}, app.Wrap(app.ContextRequired, "Unknown instance. Supply its complete HTTP(S) root URL when signing in.", nil)
		}
		names := []string{}
		for name, saved := range settings.Instances {
			canonical, canonicalErr := NormalizeURL(saved.URL)
			if canonicalErr == nil && canonical == root {
				names = append(names, name)
			}
		}
		if len(names) > 1 {
			return Target{}, choice("instance", names)
		}
		if len(names) == 1 {
			t.Name, t.Instance, t.Registered = names[0], settings.Instances[names[0]], true
		} else {
			t.Name, t.Instance = root, config.Instance{URL: root}
		}
	}
	root, err := NormalizeURL(t.Instance.URL)
	if err != nil {
		return Target{}, err
	}
	t.Instance.URL = root
	if remoteRoot != "" && root != remoteRoot && input.Repo == "" {
		return Target{}, app.Wrap(app.ContextAmbiguous, "The selected instance conflicts with the remote. Supply both --instance and --repo to target another repository.", nil)
	}
	if t.Repository == "" && binding.Instance == t.Name && t.Local != nil {
		t.Repository = binding.Repository
		t.Sources["repo"] = "binding"
	}
	if t.Repository != "" && !ValidRepository(t.Repository) {
		return Target{}, app.Wrap(app.InvalidArgument, "Repository must be owner/repo with two valid path segments.", nil)
	}
	if input.RequireRepo && t.Repository == "" {
		if discoverErr != nil && app.Failure(discoverErr).Code == app.GitUnavailable {
			return Target{}, discoverErr
		}
		return Target{}, app.Wrap(app.ContextRequired, "Specify --repo owner/repo or select a mapped Git remote.", nil)
	}
	t.LocalMatches = remoteRoot == root && remoteRepo == t.Repository && remoteRepo != ""
	if !t.LocalMatches {
		t.Credential = git.Credential{}
	}
	t.Account = input.Account
	t.Sources["account"] = "argument"
	if t.Account == "" {
		t.Account = getenv("SOTEA_ACCOUNT")
		t.Sources["account"] = "environment"
	}
	if t.Account == "" && t.Instance.ActiveAccount != "" {
		t.Account = t.Instance.ActiveAccount
		t.Sources["account"] = "active_account"
	}
	if t.Account == "" && len(t.Instance.LoggedOut) == 0 {
		accounts := []string{}
		for name := range t.Instance.Accounts {
			accounts = append(accounts, name)
		}
		if len(accounts) > 1 {
			return Target{}, choice("account", accounts)
		}
		if len(accounts) == 1 {
			t.Account = accounts[0]
			t.Sources["account"] = "unique_account"
		}
	}
	for _, character := range t.Account {
		if unicode.IsControl(character) {
			return Target{}, app.Wrap(app.InvalidArgument, "Account names must not contain control characters.", nil)
		}
	}
	return t, nil
}

// choice 构造不泄露 remote 地址的有序候选错误。
// 输入切片由本次解析持有，交互选择由调用方明确传回。
func choice(kind string, values []string) error {
	sort.Strings(values)
	return &ChoiceError{
		Failure:    app.Wrap(app.ContextAmbiguous, "Select an explicit "+kind+" to resolve ambiguous context.", nil),
		Kind:       kind,
		Candidates: values,
	}
}

// parseRemote 保留 HTTP 凭证上下文并从两个末尾路径段提出仓库候选。
// SSH 必须匹配已保存 authority 到 HTTP 根映射，绝不把 SSH 端口当 HTTP 端口。
func parseRemote(raw string, settings config.Config) (string, string, git.Credential, error) {
	var empty git.Credential
	u, err := url.Parse(raw)
	if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" {
		if u.RawQuery != "" || u.Fragment != "" {
			return "", "", empty, app.Wrap(app.ContextRequired, "Remote URLs containing query or fragment cannot be mapped automatically.", nil)
		}
		username := ""
		if u.User != nil {
			if _, hasPassword := u.User.Password(); hasPassword {
				return "", "", empty, app.Wrap(app.ContextRequired, "Remove embedded passwords from the remote before reusing Git credentials.", nil)
			}
			username = u.User.Username()
		}
		path := strings.Trim(u.Path, "/")
		parts := strings.Split(path, "/")
		if len(parts) < 2 {
			return "", "", empty, app.Wrap(app.ContextRequired, "The remote does not contain owner/repo.", nil)
		}
		repo := parts[len(parts)-2] + "/" + strings.TrimSuffix(parts[len(parts)-1], ".git")
		u.User = nil
		u.Path = "/" + strings.Join(parts[:len(parts)-2], "/")
		u.RawPath = ""
		root, rootErr := NormalizeURL(u.String())
		return root, repo, git.Credential{
			Protocol: u.Scheme,
			Host:     u.Host,
			Path:     path,
			Username: username,
		}, rootErr
	}
	authority, path := "", ""
	if err == nil && u.Scheme == "ssh" && u.Host != "" {
		authority, path = u.Host, strings.Trim(u.Path, "/")
	} else if before, after, ok := strings.Cut(raw, ":"); ok && !strings.Contains(before, "/") {
		_, host, hasUser := strings.Cut(before, "@")
		if !hasUser {
			host = before
		}
		authority, path = host, strings.Trim(after, "/")
	}
	if authority != "" {
		roots := map[string]bool{}
		for _, instance := range settings.Instances {
			if mapped := instance.SSHMappings[authority]; mapped != "" {
				root, rootErr := NormalizeURL(mapped)
				if rootErr == nil {
					roots[root] = true
				}
			}
		}
		if len(roots) == 1 {
			parts := strings.Split(path, "/")
			if len(parts) >= 2 {
				for root := range roots {
					return root, parts[len(parts)-2] + "/" + strings.TrimSuffix(parts[len(parts)-1], ".git"), empty, nil
				}
			}
		}
	}
	return "", "", empty, app.Wrap(app.ContextRequired, "Map the SSH host to an HTTP instance, or supply both --instance and --repo.", nil)
}
