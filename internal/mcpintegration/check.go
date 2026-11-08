package mcpintegration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CheckResult 描述一次本地 MCP 协议连接检查的实际范围。
// Tools 是分页发现后的工具总数，不代表远端认证成功或客户端批准流程验收。
type CheckResult struct {
	Server  string
	Version string
	Tools   int
}

// Check 重新核对托管快照后启动其子进程，仅初始化并读取工具清单。
// 调用有 20 秒上限，取消关闭会话并回收进程；不调用业务工具，不继承 SOTEA_TOKEN 等远端凭证变量。
func (s Snapshot) Check(ctx context.Context) (result CheckResult, resultErr error) {
	if s.State != "managed" {
		return result, app.Wrap(app.ConfigConflict, "Install a managed sotea integration before checking its connection.", nil)
	}
	if err := (Change{Before: s}).unchanged(ctx); err != nil {
		return result, err
	}
	if err := validateExecutable(s.Launch.Command); err != nil {
		return result, err
	}
	entry, err := s.Launch.Entry(s.Client)
	if err != nil {
		return result, err
	}
	args := entry["args"].([]string)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer func() {
		if ctx.Err() != nil {
			resultErr = app.Failure(ctx.Err())
		}
		cancel()
	}()
	command := exec.CommandContext(ctx, s.Launch.Command, args...)
	command.Env = checkEnvironment(os.Environ())
	command.Stderr = io.Discard
	command.WaitDelay = 2 * time.Second
	client := sdk.NewClient(&sdk.Implementation{Name: "sotea-integration-check", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: command, TerminateDuration: time.Second}, nil)
	if err != nil {
		return result, app.Wrap(app.IOError, "MCP initialization failed. Check the executable, arguments and sotea configuration.", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, session.Close())
	}()
	initialized := session.InitializeResult()
	if initialized.ServerInfo == nil || initialized.ServerInfo.Name != "sotea" {
		return result, app.Wrap(app.ConfigError, "The configured process did not identify itself as the sotea MCP server.", nil)
	}
	result.Server, result.Version = initialized.ServerInfo.Name, initialized.ServerInfo.Version
	cursor := ""
	seen := map[string]bool{}
	for {
		tools, err := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return result, app.Wrap(app.IOError, "MCP tool discovery failed.", err)
		}
		for _, tool := range tools.Tools {
			if tool == nil || !strings.HasPrefix(tool.Name, "sotea_") || seen[tool.Name] {
				return result, app.Wrap(app.ConfigError, "MCP discovery returned unexpected or duplicate tools.", nil)
			}
			seen[tool.Name] = true
		}
		result.Tools += len(tools.Tools)
		if result.Tools > 32 || (tools.NextCursor != "" && tools.NextCursor == cursor) {
			return result, app.Wrap(app.ConfigError, "MCP discovery exceeded the expected sotea tool catalog.", nil)
		}
		if tools.NextCursor == "" {
			break
		}
		cursor = tools.NextCursor
	}
	expected := 27
	if s.Launch.AllowWrite {
		expected += 3
	}
	if s.Launch.AllowRawAPI {
		expected++
	}
	if s.Launch.Workdir != "" || s.Launch.FollowWorkspace {
		expected++
	}
	if result.Tools != expected {
		return result, app.Wrap(app.ConfigError, fmt.Sprintf("Expected %d tools for these permissions, received %d.", expected, result.Tools), nil)
	}
	return result, nil
}

// checkEnvironment 移除远端凭证相关变量，避免本地健康检查继承开发者 token。
// 系统 PATH、证书和普通运行环境保留；服务初始化本身不访问远端或读取安全存储。
func checkEnvironment(environment []string) []string {
	filtered := []string{}
	for _, value := range environment {
		name, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(name) {
		case "SOTEA_TOKEN", "SOTEA_TOKEN_INSTANCE", "SOTEA_ACCOUNT":
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}
