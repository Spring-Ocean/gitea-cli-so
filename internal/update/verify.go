package update

import (
	"bytes"
	"context"
	"debug/macho"
	"debug/pe"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// boundedOutput 拒绝超过验证预算的子进程输出。
// 不将远端程序的任意输出带入普通诊断或机器结果。
type boundedOutput struct {
	buffer bytes.Buffer
}

// Write 限制版本验证输出在 16 KiB 内。
// 返回错误使 exec 的复制循环停止，超时仍负责回收异常程序。
func (b *boundedOutput) Write(value []byte) (int, error) {
	if len(value) > (16<<10)-b.buffer.Len() {
		return 0, errors.New("The staged executable produced too much output.")
	}
	return b.buffer.Write(value)
}

// verifyExecutable 先检查原生文件平台与架构，再通过隔离 version 命令确认版本。
// 只执行已通过发布摘要核对的暂存程序，十秒取消，不读取真实配置或传递凭证环境。
func (s *Service) verifyExecutable(ctx context.Context, filename, expected string) error {
	switch s.goos {
	case "darwin":
		file, err := macho.Open(filename)
		if err != nil {
			return app.Wrap(app.CompatibilityError, "The staged update is not a native macOS executable.", err)
		}
		cpu, kind := file.Cpu, file.Type
		if err := file.Close(); err != nil {
			return app.Wrap(app.IOError, "Unable to close the staged macOS file.", err)
		}
		expectedCPU := macho.CpuAmd64
		if s.goarch == "arm64" {
			expectedCPU = macho.CpuArm64
		}
		if cpu != expectedCPU || kind != macho.TypeExec {
			return app.Wrap(app.CompatibilityError, "The staged macOS executable does not match this architecture.", nil)
		}
	case "windows":
		file, err := pe.Open(filename)
		if err != nil {
			return app.Wrap(app.CompatibilityError, "The staged update is not a native Windows executable.", err)
		}
		machine, characteristics := file.Machine, file.Characteristics
		if err := file.Close(); err != nil {
			return app.Wrap(app.IOError, "Unable to close the staged Windows file.", err)
		}
		if machine != pe.IMAGE_FILE_MACHINE_AMD64 || characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || characteristics&pe.IMAGE_FILE_DLL != 0 {
			return app.Wrap(app.CompatibilityError, "The staged Windows executable does not match this architecture.", nil)
		}
	default:
		return app.Wrap(app.CompatibilityError, "Automatic installation is supported on macOS and Windows only.", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filename, "version", "--json", "--no-interactive")
	command.WaitDelay = time.Second
	command.Dir = os.TempDir()
	command.Env = []string{}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "TMP", "TEMP"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	var stdout, stderr boundedOutput
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return app.Wrap(app.CompatibilityError, "The staged executable failed its version check. The installed program was not changed.", err)
	}
	var result struct {
		Status string `json:"status"`
		Items  []struct {
			Data struct {
				Version string `json:"version"`
			} `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.buffer.Bytes(), &result); err != nil || result.Status != "success" || len(result.Items) != 1 || result.Items[0].Data.Version != expected || strings.TrimSpace(stderr.buffer.String()) != "" {
		return app.Wrap(app.PreconditionChanged, "The staged executable version does not match the selected release. The installed program was not changed.", err)
	}
	return nil
}
