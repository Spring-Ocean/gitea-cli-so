package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// init 仅在显式测试环境下把本测试二进制作为合成子进程。
// 无需 Python、shell 测试脚本或真实 helper，正常测试运行不会进入此分支。
func init() {
	switch os.Getenv("SOTEA_GO_TEST_CHILD") {
	case "exit":
		fmt.Fprint(os.Stdout, "raw-output")
		fmt.Fprint(os.Stderr, "raw-diagnostic")
		os.Exit(7)
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 128))
		os.Exit(0)
	case "wait":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "credential":
		input, _ := io.ReadAll(os.Stdin)
		operation := os.Args[len(os.Args)-1]
		if operation == "get" {
			if strings.Contains(string(input), "path=team/a.git") {
				fmt.Fprint(os.Stdout, "username=oauth2\npassword=synthetic-helper-value\n\n")
			}
		}
		os.Exit(0)
	case "group-parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "SOTEA_GO_TEST_CHILD=late-child")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Run(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "late-child":
		directory := os.Getenv("SOTEA_GO_TEST_MARKERS")
		if err := os.WriteFile(filepath.Join(directory, "ready"), []byte("ready"), 0o600); err != nil {
			os.Exit(1)
		}
		time.Sleep(500 * time.Millisecond)
		if err := os.WriteFile(filepath.Join(directory, "late"), []byte("late"), 0o600); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// TestCancellationStopsDescendants 保护捕获进程取消时的后代清理。
// 子进程先报告就绪，再等待写入标记；取消后标记不得继续产生。
func TestCancellationStopsDescendants(t *testing.T) {
	directory := t.TempDir()
	runner := Runner{
		Executable:  os.Args[0],
		Environment: []string{"SOTEA_GO_TEST_CHILD=group-parent", "SOTEA_GO_TEST_MARKERS=" + directory},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		_, err := runner.Capture(ctx, "", nil, nil)
		completed <- err
	}()
	for {
		if _, err := os.Stat(filepath.Join(directory, "ready")); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("child did not become ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-completed; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was lost: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(directory, "late")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("descendant continued after cancellation")
	}
}

// TestRunnerBoundaries 保护透传退出码、捕获上限、超时与缺少 Git 的分类。
// 合成子进程不访问真实仓库或凭证，不比较内部实现调用顺序。
func TestRunnerBoundaries(t *testing.T) {
	var out, diagnostic bytes.Buffer
	runner := Runner{Executable: os.Args[0], Environment: []string{"SOTEA_GO_TEST_CHILD=exit"}}
	err := runner.Passthrough(context.Background(), "", nil, nil, &out, &diagnostic)
	if app.ExitCode(err) != 7 || out.String() != "raw-output" || diagnostic.String() != "raw-diagnostic" {
		t.Fatalf("passthrough did not preserve its contract: %v", err)
	}
	runner.Environment = []string{"SOTEA_GO_TEST_CHILD=large"}
	runner.OutputLimit = 10
	result, err := runner.Capture(context.Background(), "", nil, nil)
	if err == nil || app.Failure(err).Code != app.ResponseTooLarge || len(result.Stdout) != 10 {
		t.Fatal("capture limit was not enforced")
	}
	runner.Environment = []string{"SOTEA_GO_TEST_CHILD=wait"}
	runner.Timeout = 100 * time.Millisecond
	started := time.Now()
	_, err = runner.Capture(context.Background(), "", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
		t.Fatalf("capture did not stop at its deadline: %v", err)
	}
	runner.Executable = filepath.Join(t.TempDir(), "missing-git")
	_, err = runner.Capture(context.Background(), "", nil, nil)
	if err == nil || app.Failure(err).Code != app.GitUnavailable {
		t.Fatal("missing Git was not classified")
	}
}

// TestRepositoryAndHelperContext 保护空仓库、子目录、worktree 与 helper 路径语义。
// 所有 Git 配置来自临时仓库及显式覆盖，不读取真实 credential helper。
func TestRepositoryAndHelperContext(t *testing.T) {
	runner := Runner{Environment: []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_COUNT=0",
		"SOTEA_GO_TEST_CHILD=credential",
	}}
	directory := filepath.Join(t.TempDir(), "中文 repository")
	run := func(path string, args ...string) {
		t.Helper()
		if _, err := runner.Capture(context.Background(), path, args, nil); err != nil {
			t.Fatalf("fixture setup failed: %v", err)
		}
	}
	run("", "init", "--initial-branch=main", directory)
	repository, err := runner.Discover(context.Background(), directory)
	if err != nil || repository.Head != "" || repository.Branch != "main" {
		t.Fatalf("empty repository failed: %v", err)
	}
	run(directory, "-c", "user.name=P1", "-c", "user.email=p1@sotea.invalid", "commit", "--allow-empty", "-m", "P1 fixture")
	run(directory, "remote", "add", "upstream", "https://git.sotea.invalid/team/a.git")
	worktree := filepath.Join(t.TempDir(), "worktree path")
	run(directory, "worktree", "add", "--detach", worktree)
	subdir := filepath.Join(worktree, "sub")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	repository, err = runner.Discover(context.Background(), subdir)
	if err != nil || repository.Head == "" || repository.Branch != "" || len(repository.Remotes) != 1 {
		t.Fatalf("worktree discovery failed: %v", err)
	}
	helper := "!" + "'" + strings.ReplaceAll(os.Args[0], "'", "'\"'\"'") + "'"
	run(directory, "config", "credential.helper", "")
	run(directory, "config", "--add", "credential.helper", helper)
	run(directory, "config", "credential.useHttpPath", "true")
	credential, err := runner.Credential(context.Background(), directory, "fill", Credential{
		Protocol: "https",
		Host:     "git.sotea.invalid:8443",
		Path:     "team/a.git",
	}, false)
	if err != nil || credential.Username != "oauth2" || credential.Password != "synthetic-helper-value" {
		t.Fatalf("isolated helper failed: %v", err)
	}
	for _, operation := range []string{"approve", "reject"} {
		if _, err := runner.Credential(context.Background(), directory, operation, credential, false); err != nil {
			t.Fatal(err)
		}
	}
}
