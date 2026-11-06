package git

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Version 保存 Git 的原始版本及功能判断需要的数字。
// 厂商后缀不代表上游补丁状态，调用方应分别记录实际环境证据。
type Version struct {
	Raw   string
	Major int
	Minor int
	Patch int
}

// Remote 保存配置中一个 remote 的全部 fetch URL。
// URLs 可能含嵌入式认证，禁止直接作为普通输出或 JSON 返回。
type Remote struct {
	Name string
	URLs []string `json:"-"`
}

// Repository 保存 Git 本地查询结果，不推断 Gitea 实例或远端账号。
// Head 为空表示尚无可解析提交；Branch 为空而 Head 非空表示 detached HEAD。
type Repository struct {
	Root    string
	GitDir  string
	Bare    bool
	Branch  string
	Head    string
	Remotes []Remote
}

// Version 查询实际执行的 Git 并解析其数字版本。
// 不自动升级 Git，也不把候选最低版本当作已经完成的兼容承诺。
func (r Runner) Version(ctx context.Context) (Version, error) {
	result, err := r.Capture(ctx, "", []string{"--version"}, nil)
	if err != nil {
		return Version{}, err
	}
	raw := strings.TrimSpace(string(result.Stdout))
	match := regexp.MustCompile(`^git version (\d+)\.(\d+)(?:\.(\d+))?`).FindStringSubmatch(raw)
	if match == nil {
		return Version{}, app.Wrap(app.GitError, "Git returned an unrecognized version string.", nil)
	}
	numbers := []int{0, 0, 0}
	for i, text := range match[1:] {
		if text == "" {
			continue
		}
		numbers[i], err = strconv.Atoi(text)
		if err != nil {
			return Version{}, app.Wrap(app.GitError, "Git version numbers are out of range.", err)
		}
	}
	return Version{Raw: raw, Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

// Discover 从目录查询真实 Git 根目录、分支、HEAD 和 remote。
// 支持子目录、worktree、bare 和空仓库；无法识别时返回 CONTEXT_REQUIRED，不自动创建仓库。
func (r Runner) Discover(ctx context.Context, directory string) (Repository, error) {
	query := func(args ...string) (string, error) {
		result, err := r.Capture(ctx, directory, args, nil)
		return strings.TrimSuffix(strings.TrimSuffix(string(result.Stdout), "\n"), "\r"), err
	}
	gitDir, err := query("rev-parse", "--absolute-git-dir")
	if err != nil {
		var exit *ProcessError
		if errors.As(err, &exit) {
			return Repository{}, app.Wrap(app.ContextRequired, "Unable to determine the Git repository. Check directory access or specify an API context.", err)
		}
		return Repository{}, err
	}
	bare, err := query("rev-parse", "--is-bare-repository")
	if err != nil {
		return Repository{}, err
	}
	repo := Repository{GitDir: gitDir, Bare: bare == "true", Remotes: []Remote{}}
	repo.Root = gitDir
	if !repo.Bare {
		repo.Root, err = query("rev-parse", "--show-toplevel")
		if err != nil {
			return Repository{}, err
		}
	}
	repo.Branch, err = query("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil && !exitCodeIs(err, 1) {
		return Repository{}, err
	}
	repo.Head, err = query("rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil && !exitCodeIs(err, 1) {
		return Repository{}, err
	}
	remotes, err := r.Capture(ctx, directory, []string{"config", "--null", "--get-regexp", `^remote\..*\.url$`}, nil)
	if err != nil && !exitCodeIs(err, 1) {
		return Repository{}, err
	}
	indices := map[string]int{}
	for _, entry := range strings.Split(string(remotes.Stdout), "\x00") {
		key, value, ok := strings.Cut(entry, "\n")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		index, exists := indices[name]
		if !exists {
			index = len(repo.Remotes)
			indices[name] = index
			repo.Remotes = append(repo.Remotes, Remote{Name: name})
		}
		repo.Remotes[index].URLs = append(repo.Remotes[index].URLs, value)
	}
	return repo, nil
}

// exitCodeIs 识别 Git 已约定的正常缺失状态。
// 只忽略指定状态，不把权限、取消或启动失败当成空结果。
func exitCodeIs(err error, code int) bool {
	var exit *ProcessError
	return errors.As(err, &exit) && exit.Code == code
}
