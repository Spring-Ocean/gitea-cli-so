package cli

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
)

// Execute 为进程创建命令树、连接 I/O 并映射错误退出状态。
// 正常 Git 非零退出不重复输出诊断；JSON 只用于业务结果，生成文案统一为英文。
func Execute(ctx context.Context, args []string, input io.Reader, out, diagnostic io.Writer, version string) int {
	command := NewRootCommand(version)
	command.SetArgs(args)
	command.SetIn(input)
	command.SetOut(out)
	command.SetErr(diagnostic)
	// Cobra 在未知子命令时报错可能早于解析根选项，先保留明确的 JSON 请求。
	if _, _, findErr := command.Find(args); findErr != nil {
		_ = command.ParseFlags(args)
	}
	executed, err := command.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}
	if printed, ok := err.(*printedResultError); ok {
		return printed.code
	}
	if process, ok := err.(*git.ProcessError); ok {
		return process.ExitCode()
	}
	var failure *app.Error
	if !errors.As(err, &failure) {
		err = app.Wrap(app.InvalidArgument, "Invalid command or arguments. Run sotea --help.", err)
	}
	machine, _ := command.PersistentFlags().GetBool("json")
	if executed != nil && executed.Annotations["sotea.raw"] == "true" {
		machine = false
	}
	result := app.NewResult(app.Context{}, nil, err)
	render := renderer(command)
	if machine {
		if writeErr := render.JSON(result); writeErr != nil {
			return app.ExitCode(writeErr)
		}
	} else if writeErr := render.Error(err); writeErr != nil {
		return app.ExitCode(writeErr)
	}
	return result.ExitCode()
}

// renderer 绑定当前命令的独立输出流与显式环境 token 脱敏。
// 不读取系统密钥库，也不将环境凭证写入应用上下文。
func renderer(command *cobra.Command) output.Renderer {
	return output.Renderer{
		Out:        command.OutOrStdout(),
		Diagnostic: command.ErrOrStderr(),
		Redactor:   output.NewRedactor(os.Getenv("SOTEA_TOKEN")),
	}
}
