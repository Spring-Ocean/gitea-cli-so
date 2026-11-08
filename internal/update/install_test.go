package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// init 仅为显式测试子进程提供固定版本与正在运行文件的替换场景。
// 普通 go test 不匹配这些参数；不使用真实安装路径、凭证或外部服务。
func init() {
	if len(os.Args) == 4 && os.Args[1] == "version" && os.Args[2] == "--json" && os.Args[3] == "--no-interactive" {
		fmt.Fprint(os.Stdout, `{"status":"success","items":[{"data":{"version":"v9.0.0"}}]}`)
		os.Exit(0)
	}
	if len(os.Args) == 5 && os.Args[1] == "update-replace-fixture" {
		if err := replaceExecutable(os.Args[2], os.Args[3], os.Args[4]); err != nil {
			fmt.Fprint(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// archiveEntry 描述小型隔离归档条目，不接受宿主路径作为内容来源。
// 类型支持普通文件和恶意链接，供真实解包逻辑验证边界。
type archiveEntry struct {
	name string
	body []byte
	link bool
}

// makeArchive 生成 tar.gz 或 ZIP 夹具，供摘要与解包共同使用。
// 写入和关闭错误使测试失败，避免使用损坏夹具误判产品拒绝路径。
func makeArchive(t *testing.T, windows bool, entries []archiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	if windows {
		writer := zip.NewWriter(&output)
		for _, entry := range entries {
			header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
			header.SetMode(0o755)
			if entry.link {
				header.SetMode(os.ModeSymlink | 0o777)
			}
			file, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		compressed := gzip.NewWriter(&output)
		writer := tar.NewWriter(compressed)
		for _, entry := range entries {
			header := &tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
			if entry.link {
				header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, "outside", 0
			}
			if err := writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if !entry.link {
				if _, err := writer.Write(entry.body); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return output.Bytes()
}

// TestArchiveAndChecksumBoundaries 保护二种平台包的路径、链接和重复可执行文件边界。
// 校验值必须精确且唯一，禁止通过相似文件名或恶意目录项写到 staging 外。
func TestArchiveAndChecksumBoundaries(t *testing.T) {
	for _, system := range []string{"darwin", "windows"} {
		t.Run(system, func(t *testing.T) {
			binary, extension := "sotea", ".tar.gz"
			if system == "windows" {
				binary, extension = "sotea.exe", ".zip"
			}
			folder := "sotea-v9.0.0-" + system + "-amd64"
			for _, sample := range []struct {
				name    string
				entries []archiveEntry
				valid   bool
			}{
				{"valid", []archiveEntry{{name: folder + "/" + binary, body: []byte("payload")}}, true},
				{"root", []archiveEntry{{name: binary, body: []byte("payload")}}, true},
				{"traversal", []archiveEntry{{name: "../" + binary, body: []byte("payload")}}, false},
				{"windows-separator", []archiveEntry{{name: "folder\\" + binary, body: []byte("payload")}}, false},
				{"link", []archiveEntry{{name: binary, link: true}}, false},
				{"duplicate", []archiveEntry{{name: binary, body: []byte("one")}, {name: folder + "/" + binary, body: []byte("two")}}, false},
			} {
				t.Run(sample.name, func(t *testing.T) {
					directory := t.TempDir()
					archive := filepath.Join(directory, "archive")
					if err := os.WriteFile(archive, makeArchive(t, system == "windows", sample.entries), 0o600); err != nil {
						t.Fatal(err)
					}
					s := Service{goos: system, goarch: "amd64"}
					err := s.extract(archive, filepath.Join(directory, "staged"), folder+extension)
					if (err == nil) != sample.valid {
						t.Fatalf("unexpected extraction result: %v", err)
					}
				})
			}
		})
	}
	hash := strings.Repeat("a", 64)
	for _, contents := range []string{hash + "  other.zip\n", "bad  archive.zip\n", hash + "  archive.zip\n" + hash + "  archive.zip\n"} {
		if _, err := checksum([]byte(contents), "archive.zip"); err == nil {
			t.Fatal("missing, malformed or duplicate checksum accepted")
		}
	}
	if got, err := checksum([]byte(hash+"  archive.zip\n"), "archive.zip"); err != nil || got != hash {
		t.Fatalf("valid checksum failed: %s %v", got, err)
	}
}

// TestVerifiedInstallKeepsOriginalOnFailure 通过真实原生程序覆盖下载到替换的完整链路。
// 摘要、版本及并发目标变更失败不得覆盖旧文件；成功保留旧备份并安装已验证的字节。
func TestVerifiedInstallKeepsOriginalOnFailure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("Native release execution is validated on the supported macOS and Windows CI runners.")
	}
	payload, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	name, extension := "sotea", ".tar.gz"
	if runtime.GOOS == "windows" {
		name, extension = "sotea.exe", ".zip"
	}
	folder := "sotea-v9.0.0-" + runtime.GOOS + "-" + runtime.GOARCH
	archiveContents := makeArchive(t, runtime.GOOS == "windows", []archiveEntry{{name: folder + "/" + name, body: payload}})
	for _, scenario := range []string{"success", "bad-checksum", "truncated-download", "changed-target", "wrong-version", "draft-after-check", "cleanup-failure"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, name)
			original := []byte("previous executable")
			if err := os.WriteFile(target, original, 0o755); err != nil {
				t.Fatal(err)
			}
			tag := "v9.0.0"
			if scenario == "wrong-version" {
				tag = "v9.0.1"
			}
			assetName := "sotea-" + tag + "-" + runtime.GOOS + "-" + runtime.GOARCH + extension
			body := archiveContents
			if scenario == "wrong-version" {
				body = makeArchive(t, runtime.GOOS == "windows", []archiveEntry{{name: name, body: payload}})
			}
			sum := fmt.Sprintf("%x", sha256.Sum256(body))
			if scenario == "bad-checksum" {
				sum = strings.Repeat("0", 64)
			}
			sums := []byte(sum + "  " + assetName + "\n")
			assetURL := "https://github.com/" + Repository + "/releases/download/" + tag + "/"
			release := Release{Tag: tag, Assets: []Asset{
				{Name: assetName, URL: assetURL + assetName, Size: int64(len(body))},
				{Name: "SHA256SUMS", URL: assetURL + "SHA256SUMS", Size: int64(len(sums))},
			}}
			now := time.Now()
			s := fixtureService(t, &now, func(request *http.Request) (*http.Response, error) {
				switch {
				case strings.Contains(request.URL.Path, "/releases/tags/"):
					if scenario == "cleanup-failure" {
						if err := os.Remove(target + ".update.lock"); err != nil {
							return nil, err
						}
					}
					published := release
					if scenario == "draft-after-check" {
						published.Draft = true
					}
					if scenario == "changed-target" {
						if err := os.WriteFile(target, []byte("external change"), 0o755); err != nil {
							return nil, err
						}
					}
					contents, err := json.Marshal(published)
					if err != nil {
						return nil, err
					}
					return fixtureResponse(contents, 200), nil
				case strings.HasSuffix(request.URL.Path, "/SHA256SUMS"):
					return fixtureResponse(sums, 200), nil
				default:
					contents := body
					if scenario == "truncated-download" {
						contents = body[:len(body)/2]
					}
					return fixtureResponse(contents, 200), nil
				}
			})
			s.goos, s.goarch = runtime.GOOS, runtime.GOARCH
			checked, err := s.selectRelease("v8.0.0", false, []Release{release})
			if err != nil || !checked.Installable {
				t.Fatalf("invalid fixture selection: %+v %v", checked, err)
			}
			installed, installErr := s.installAt(context.Background(), checked, target)
			actual, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "success" || scenario == "cleanup-failure" {
				if !bytes.Equal(actual, payload) || !installed.RestartRequired {
					t.Fatalf("verified installation failed: %+v %v", installed, installErr)
				}
				if scenario == "success" && installErr != nil {
					t.Fatalf("successful installation returned an error: %v", installErr)
				}
				if scenario == "cleanup-failure" && (installErr == nil || app.Failure(installErr).Code != app.PartialFailure) {
					t.Fatalf("committed installation cleanup failure lost its partial result: %v", installErr)
				}
				backup, err := os.ReadFile(installed.Backup)
				if err != nil || !bytes.Equal(backup, original) {
					t.Fatalf("previous executable was not preserved: %v", err)
				}
			} else {
				if scenario == "changed-target" {
					original = []byte("external change")
				}
				if installErr == nil || !bytes.Equal(actual, original) {
					t.Fatalf("a failed update replaced the installed program: %v", installErr)
				}
			}
			if _, err := os.Stat(target + ".update.lock"); !os.IsNotExist(err) {
				t.Fatal("installation lock was not released")
			}
		})
	}
}

// TestRunningExecutableReplacementAndRecovery 保护正在运行的文件替换和失败恢复。
// 子进程运行临时副本，Windows 原生 CI 必须真正完成改名；失败路径保留原字节。
func TestRunningExecutableReplacementAndRecovery(t *testing.T) {
	directory := t.TempDir()
	contents, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "sotea")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	staged, backup := filepath.Join(directory, "staged"), filepath.Join(directory, "previous")
	if err := os.WriteFile(target, contents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, target, "update-replace-fixture", staged, target, backup)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("running executable replacement failed: %v %s", err, output)
	}
	installed, err := os.ReadFile(target)
	if err != nil || string(installed) != "new executable" {
		t.Fatalf("new program missing after replacement: %v", err)
	}
	if previous, err := os.ReadFile(backup); err != nil || !bytes.Equal(previous, contents) {
		t.Fatalf("running program backup missing: %v", err)
	}
	missing := filepath.Join(directory, "missing")
	if err := replaceExecutable(missing, target, backup+"-failure"); err == nil {
		t.Fatal("missing staging file did not fail replacement")
	}
	if after, err := os.ReadFile(target); err != nil || !bytes.Equal(after, installed) {
		t.Fatalf("failed replacement did not preserve or restore the old program: %v", err)
	}
}
