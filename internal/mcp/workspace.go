package mcp

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/fileinput"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// workspaceAccess 保存客户端单次授权集合对应的作用域和不可复用计划绑定。
// 根变化后使用新随机绑定；旧句柄留到 Host.Close 释放，保护仍在执行的调用。
type workspaceAccess struct {
	path          string
	info          os.FileInfo
	scope         *fileinput.Scope
	binding       string
	err           error
	revoked       bool
	requiresRoots bool
}

// launchWorkspace 在进程启动时记录明确客户端适配器提供的项目目录。
// Codex 的 thread cwd 和 Claude 的 CLAUDE_PROJECT_DIR 分别有实际启动与官方契约依据；没有适配器时不读取 cwd。
func launchWorkspace(source string) string {
	switch source {
	case "codex":
		directory, err := os.Getwd()
		if err == nil {
			return directory
		}
	case "claude":
		return os.Getenv("CLAUDE_PROJECT_DIR")
	}
	return ""
}

// invalidateWorkspace 响应客户端根变更通知，撤销旧计划的工作区绑定。
// 不关闭仍被在途操作使用的句柄；即使根随后恢复原路径，也必须取得新的绑定。
func (h *Host) invalidateWorkspace(client *sdk.ServerSession) {
	if !h.options.FollowWorkspace {
		return
	}
	h.workspaceMu.Lock()
	if current := h.workspaces[client]; current != nil {
		current.revoked = true
	}
	h.workspaceMu.Unlock()
}

// workspaceView 为一次工具调用建立独立的工作区视图，不复制锁和活动计数。
// 客户端未提供唯一可信目录时仅远端可用；权限取自启动时的 FollowWorkspace 授权，不接受工具参数扩权。
func (h *Host) workspaceView(ctx context.Context, client *sdk.ServerSession) *Host {
	if !h.options.FollowWorkspace {
		return h
	}
	access := h.resolveWorkspace(ctx, client)
	options := h.options
	options.Defaults.Workdir = access.path
	return &Host{
		environment:      h.environment,
		options:          options,
		redactor:         h.redactor,
		scope:            access.scope,
		plans:            h.plans,
		workspaceBinding: access.binding,
		workspaceError:   access.err,
	}
}

// resolveWorkspace 按会话串行重新查询客户端根，检测撤权、切换及目录实体替换。
// roots 查询最多两秒，每进程最多保留八个作用域；只使用明确的 roots 或启动适配器，不合并多根的父目录。
func (h *Host) resolveWorkspace(ctx context.Context, client *sdk.ServerSession) *workspaceAccess {
	h.workspaceMu.Lock()
	defer h.workspaceMu.Unlock()
	old := h.workspaces[client]
	launchDirectory := h.launchWorkspace
	if old != nil && old.requiresRoots {
		launchDirectory = ""
	}
	path, source, err := clientWorkspace(ctx, client, h.options.WorkspaceSource, launchDirectory)
	var info os.FileInfo
	if err == nil {
		info, err = os.Stat(path)
		if err != nil || !info.IsDir() {
			err = app.Wrap(app.PathOutsideRoot, "The client workspace is not an accessible local directory. Remote tools remain available.", nil)
		}
	}
	if err == nil && old != nil && !old.revoked && old.err == nil && old.path == path && os.SameFile(old.info, info) {
		return old
	}
	if err != nil && old != nil && !old.revoked && old.err != nil && app.Failure(old.err).Message == app.Failure(err).Message {
		return old
	}
	access := &workspaceAccess{path: path, info: info, err: err}
	access.requiresRoots = source == "roots" || (old != nil && old.requiresRoots)
	// 进入失败或根变化时换绑；稳定的远端模式保留绑定，不阻断其已有写计划。
	access.binding, err = randomID()
	if err != nil {
		access.err = err
	}
	if access.err == nil {
		if old != nil && old.err == nil && old.path == path && os.SameFile(old.info, info) {
			access.scope = old.scope
		} else if len(h.workspaceScopes) >= 8 {
			access.err = app.Wrap(app.ResourceLimit, "The MCP process has reached its workspace limit. Restart its server in the client; remote tools remain available.", nil)
		} else {
			access.scope, access.err = fileinput.NewScope(path, h.options.MaxFileBytes, h.options.MaxSnapshotBytes)
			if access.scope != nil {
				h.workspaceScopes = append(h.workspaceScopes, access.scope)
			}
		}
	}
	if access.err != nil {
		access.path = ""
	}
	if client != nil {
		h.workspaces[client] = access
	}
	return access
}

// clientWorkspace 优先使用唯一 MCP 根，空根时允许明确客户端适配器的启动目录。
// 多根不选父目录；适配器以外的 cwd／环境、远程主机、卷根及主目录均不能自动授权。
func clientWorkspace(ctx context.Context, client *sdk.ServerSession, source, launchDirectory string) (string, string, error) {
	unavailable := func(message string) (string, string, error) {
		return "", "", app.Wrap(app.PathOutsideRoot, message+" Remote tools remain available; select a fixed directory if needed.", nil)
	}
	if client == nil || client.InitializeParams() == nil || client.InitializeParams().Capabilities == nil {
		return unavailable("The MCP client did not provide workspace roots.")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := client.ListRoots(ctx, nil)
	if err == nil && result != nil && len(result.Roots) > 0 {
		if len(result.Roots) != 1 || result.Roots[0] == nil {
			return unavailable("Follow client workspace requires exactly one local workspace; no directory was selected automatically.")
		}
		path, err := workspacePath(result.Roots[0].URI)
		return path, "roots", err
	}
	if source != "" && launchDirectory != "" {
		if !filepath.IsAbs(launchDirectory) {
			return unavailable("The client launch adapter did not supply an absolute project directory.")
		}
		path := filepath.ToSlash(launchDirectory)
		if runtime.GOOS == "windows" && filepath.VolumeName(launchDirectory) != "" {
			path = "/" + path
		}
		actual, err := workspacePath((&url.URL{Scheme: "file", Path: path}).String())
		return actual, source, err
	}
	return unavailable("The client did not supply a unique workspace or an available explicitly configured launch adapter.")
}

// workspacePath 将标准本地 file URI 转为真实路径，并拒绝范围过大的授权。
// Windows 盘符从 /C:/ 形式转换；不接受 UNC 主机、URI 查询或根外映射。
func workspacePath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsRune(u.Path, 0) {
		return "", app.Wrap(app.PathOutsideRoot, "The client workspace must be a local file URI without a remote host, credentials, query or fragment.", nil)
	}
	path := filepath.FromSlash(u.Path)
	if runtime.GOOS == "windows" && strings.HasPrefix(path, "\\\\") {
		return "", app.Wrap(app.PathOutsideRoot, "UNC locations cannot be automatically authorized as a client workspace. Choose a fixed directory explicitly.", nil)
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '\\' && path[2] == ':' {
		path = path[1:]
	}
	if !filepath.IsAbs(path) {
		return "", app.Wrap(app.PathOutsideRoot, "The client workspace must identify an absolute local directory.", nil)
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", app.Wrap(app.PathOutsideRoot, "The client workspace could not be resolved locally.", nil)
	}
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return "", app.Wrap(app.PathOutsideRoot, "Unable to verify the client workspace boundary.", nil)
	}
	actualHome, err := filepath.EvalSymlinks(home)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", app.Wrap(app.PathOutsideRoot, "Unable to verify the home directory boundary.", nil)
	}
	if filepath.Dir(actual) == actual || filepath.Clean(actual) == filepath.Clean(home) || (actualHome != "" && filepath.Clean(actual) == filepath.Clean(actualHome)) {
		return "", app.Wrap(app.PathOutsideRoot, "A filesystem root or home directory cannot be automatically authorized as a project workspace. Select a narrower project or a fixed directory explicitly.", nil)
	}
	return actual, nil
}

// localAccessError 返回当前本地授权不可用的具体原因。
// 固定及仅远端模式保留明确修复提示，不以 nil scope 进入文件或 Git 适配器。
func (h *Host) localAccessError() error {
	if h.workspaceError != nil {
		return h.workspaceError
	}
	return app.Wrap(app.PathOutsideRoot, "Local access requires a fixed --workdir or an available --follow-workspace authorization.", nil)
}
