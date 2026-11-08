package main

import (
	"context"
	_ "embed"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Spring-Ocean/gitea-cli-so/internal/cli"
)

// version 默认嵌入同目录 VERSION，CLI、菜单和 MCP 使用同一版本来源。
// 构建流水线可通过 -ldflags 覆盖并附加提交标识；入口去掉文件末尾换行。
//
//go:embed VERSION
var version string

// main 将操作系统中断传播给命令及其子进程。
// 退出前先释放信号订阅，再使用 CLI 契约返回实际执行状态。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, strings.TrimSpace(version))
	stop()
	os.Exit(code)
}
