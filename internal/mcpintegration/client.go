// Package mcpintegration 管理客户端的本地 MCP 接入配置。
// 本包不读取终端、不保存凭证，也不修改客户端的批准策略。
package mcpintegration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/pelletier/go-toml/v2"
)

// Client 表示向导支持的客户端，配置路径由 Destination 按用户或项目范围解析。
// 不根据 PATH 中的同名命令猜测客户端，也不依赖客户端已经安装。
type Client string

const (
	// Codex 使用 TOML 的 mcp_servers 配置表。
	Codex Client = "Codex"
	// ClaudeCode 使用 mcpServers JSON 配置对象。
	ClaudeCode Client = "Claude Code"
	// Cursor 使用用户或项目的 mcpServers 配置对象。
	Cursor Client = "Cursor"
	// VSCode 使用兼容的 servers 配置对象。
	VSCode Client = "VS Code"
)

// Launch 保存不含密钥的 MCP 子进程启动参数。
// Command、ConfigPath 和非空 Workdir 必须为绝对路径；FollowWorkspace 与固定目录／仓库互斥，身份仍由服务逐次验证。
type Launch struct {
	Command         string
	ConfigPath      string
	Instance        string
	Account         string
	Repository      string
	Workdir         string
	AllowWrite      bool
	AllowRawAPI     bool
	FollowWorkspace bool
}

// Entry 生成客户端可用的启动对象或 Codex 表字段。
// 不写 env、token 或客户端自动批准设置；跟随模式只写明确适配器，固定模式只授权指定目录。
func (l Launch) Entry(client Client) (map[string]any, error) {
	if !filepath.IsAbs(l.Command) || !filepath.IsAbs(l.ConfigPath) || l.Instance == "" || l.Account == "" {
		return nil, app.Wrap(app.InvalidArgument, "Choose an absolute binary and sotea configuration path, an instance and an account.", nil)
	}
	if l.Workdir != "" && !filepath.IsAbs(l.Workdir) {
		return nil, app.Wrap(app.InvalidArgument, "The authorized working directory must be absolute.", nil)
	}
	if l.FollowWorkspace && (l.Workdir != "" || l.Repository != "") {
		return nil, app.Wrap(app.InvalidArgument, "Following the client workspace cannot pin a working directory or default repository. Clear both fixed defaults first.", nil)
	}
	args := []string{"--no-interactive", "--config", l.ConfigPath, "--instance", l.Instance, "--account", l.Account}
	if l.Repository != "" {
		args = append(args, "--repo", l.Repository)
	}
	if l.Workdir != "" {
		args = append(args, "--workdir", l.Workdir)
	}
	args = append(args, "mcp", "serve")
	if l.FollowWorkspace {
		args = append(args, "--follow-workspace")
		switch client {
		case Codex:
			args = append(args, "--workspace-source", "codex")
		case ClaudeCode:
			args = append(args, "--workspace-source", "claude")
		}
	}
	if l.AllowWrite {
		args = append(args, "--allow-write")
	}
	if l.AllowRawAPI {
		args = append(args, "--allow-raw-api")
	}
	entry := map[string]any{"command": l.Command, "args": args}
	switch client {
	case Codex:
		entry["startup_timeout_sec"] = 20
		entry["tool_timeout_sec"] = 360
	case ClaudeCode, VSCode:
		entry["type"] = "stdio"
	case Cursor:
	default:
		return nil, app.Wrap(app.InvalidArgument, "Unsupported MCP client.", nil)
	}
	return entry, nil
}

// Destination 返回官方支持的默认配置位置，project 非空表示项目范围。
// 用户路径遵守 CODEX_HOME／CLAUDE_CONFIG_DIR；VS Code 使用当前标准配置目录，其他配置档可由界面覆盖。
func Destination(client Client, project string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", app.Wrap(app.ConfigError, "Unable to locate the home directory.", err)
	}
	if project != "" {
		if !filepath.IsAbs(project) {
			return "", app.Wrap(app.InvalidArgument, "Choose an absolute project directory.", nil)
		}
		switch client {
		case Codex:
			return filepath.Join(project, ".codex", "config.toml"), nil
		case ClaudeCode:
			return filepath.Join(project, ".mcp.json"), nil
		case Cursor:
			return filepath.Join(project, ".cursor", "mcp.json"), nil
		case VSCode:
			return filepath.Join(project, ".vscode", "mcp.json"), nil
		}
	}
	switch client {
	case Codex:
		base := os.Getenv("CODEX_HOME")
		if base == "" {
			base = filepath.Join(home, ".codex")
		}
		return filepath.Abs(filepath.Join(base, "config.toml"))
	case ClaudeCode:
		base := os.Getenv("CLAUDE_CONFIG_DIR")
		if base != "" {
			return filepath.Abs(filepath.Join(base, ".claude.json"))
		}
		return filepath.Join(home, ".claude.json"), nil
	case Cursor:
		return filepath.Join(home, ".cursor", "mcp.json"), nil
	case VSCode:
		base, err := os.UserConfigDir()
		if err != nil {
			return "", app.Wrap(app.ConfigError, "Unable to locate the VS Code configuration directory.", err)
		}
		if runtime.GOOS == "darwin" {
			base = filepath.Join(home, "Library", "Application Support")
		}
		return filepath.Join(base, "Code", "User", "mcp.json"), nil
	}
	return "", app.Wrap(app.InvalidArgument, "Unsupported MCP client.", nil)
}

// encodeEntry 仅序列化即将写入的 sotea 条目供预览和格式适配复用。
// Codex 只输出独立的 sotea 子表，避免重定义已有父表；路径与参数使用标准转义。
func encodeEntry(client Client, entry map[string]any) ([]byte, error) {
	if client == Codex {
		encoded, err := toml.Marshal(map[string]any{"mcp_servers": map[string]any{"sotea": entry}})
		if err != nil {
			return nil, err
		}
		// 仅移除编码器生成的空父表，不扫描或改写用户配置。
		// 保留子表的完整限定路径，嵌套设置也仍归属于 sotea。
		return bytes.TrimPrefix(encoded, []byte("[mcp_servers]\n")), nil
	}
	return json.MarshalIndent(entry, "", "  ")
}

// clientRoot 返回相应 JSON 格式的服务器容器名称。
// Codex 单独使用 TOML 适配，不将 JSON 包装格式误用于 TOML。
func clientRoot(client Client) string {
	if client == VSCode {
		return "servers"
	}
	return "mcpServers"
}

// validateExecutable 检查即将注册的绝对可执行文件，而不启动它。
// macOS 拒绝没有执行位的普通文件；Windows 的实际进程启动结果由连接检查验证。
func validateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil || !filepath.IsAbs(path) || !info.Mode().IsRegular() {
		return app.Wrap(app.InvalidArgument, "Select an existing absolute sotea executable path.", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return app.Wrap(app.InvalidArgument, "The selected sotea binary is not executable.", nil)
	}
	return nil
}

// launchFromEntry 提取已托管条目的启动参数，用于检查及更新表单初值。
// 不执行用户手工创建的同名条目；未知启动结构一律报错。
func launchFromEntry(entry map[string]any) (Launch, error) {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return Launch{}, err
	}
	var command struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(encoded, &command); err != nil {
		return Launch{}, app.Wrap(app.ConfigError, "The managed launch entry is invalid.", err)
	}
	l := Launch{Command: command.Command}
	for i := 0; i < len(command.Args); i++ {
		arg := command.Args[i]
		switch arg {
		case "--config", "--instance", "--account", "--repo", "--workdir", "--workspace-source":
			if i+1 == len(command.Args) {
				return Launch{}, fmt.Errorf("Invalid managed argument: %s", arg)
			}
			i++
			value := command.Args[i]
			switch arg {
			case "--config":
				l.ConfigPath = value
			case "--instance":
				l.Instance = value
			case "--account":
				l.Account = value
			case "--repo":
				l.Repository = value
			case "--workdir":
				l.Workdir = value
			case "--workspace-source":
				if value != "codex" && value != "claude" {
					return Launch{}, app.Wrap(app.ConfigError, "The managed workspace adapter is invalid.", nil)
				}
			}
		case "--allow-write":
			l.AllowWrite = true
		case "--allow-raw-api":
			l.AllowRawAPI = true
		case "--follow-workspace":
			l.FollowWorkspace = true
		case "--no-interactive", "mcp", "serve":
		default:
			return Launch{}, app.Wrap(app.ConfigConflict, "The managed entry has unexpected arguments. Restore its last installed version before continuing.", nil)
		}
	}
	if strings.TrimSpace(l.Instance) == "" {
		return Launch{}, app.Wrap(app.ConfigError, "The managed entry does not identify a Gitea instance.", nil)
	}
	return l, nil
}
