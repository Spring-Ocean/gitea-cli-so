package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Spring-Ocean/gitea-cli-so/internal/cli"
)

// version 由构建流水线通过 -ldflags 注入。
// 本地未注入时保留 dev，避免把开发构建误认成正式版本。
var version = "dev"

// main 将操作系统中断传播给命令及其子进程。
// 退出前先释放信号订阅，再使用 CLI 契约返回实际执行状态。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version)
	stop()
	os.Exit(code)
}
