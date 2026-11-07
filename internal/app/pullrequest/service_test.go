package pullrequest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/issue"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
)

type prFixture struct {
	mu         sync.Mutex
	value      PullRequest
	writes     []string
	payload    map[string]any
	mode       string
	unreadable bool
	repository Repository
}

// newPRFixture 提供固定身份、仓库和真实 HTTP 边界，不使用系统凭证。
// 故障由服务端在实际接受写入后注入，可证明断连时不会重复提交。
func newPRFixture(t *testing.T, version string) (*prFixture, Service) {
	t.Helper()
	repository := Repository{
		ID:            1,
		FullName:      "team/repo",
		DefaultBranch: "main",
		AllowMerge:    true,
	}
	repository.Permissions.Push = true
	fixture := &prFixture{
		repository: repository,
		value: PullRequest{
			Issue: issue.Issue{
				Number: 1,
				Title:  "PR",
				State:  "open",
			},
			Head: Branch{
				Ref:  "feature",
				SHA:  strings.Repeat("a", 40),
				Repo: repository,
			},
			Base: Branch{
				Ref:  "main",
				SHA:  strings.Repeat("b", 40),
				Repo: repository,
			},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(fixture.handle))
	t.Cleanup(server.Close)
	store := config.Store{Path: filepath.Join(t.TempDir(), "config.json")}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	instance := config.Instance{
		URL:           server.URL,
		Proxy:         "direct",
		ActiveAccount: "alice",
		Accounts: map[string]config.Account{"alice": {
			InstanceURL: server.URL,
			Source:      "environment",
		}},
	}
	snapshot.Config.Instances["team"] = instance
	snapshot, err = store.Save(context.Background(), snapshot, snapshot.Config)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Service: write.Service{
		Auth: auth.Service{
			Store: store,
			Getenv: func(name string) string {
				if name == "SOTEA_TOKEN" {
					return "synthetic-pr-token"
				}
				return ""
			},
		},
		Target: target.Target{
			Name:       "team",
			Instance:   instance,
			Repository: "team/repo",
			Account:    "alice",
			Registered: true,
		},
		Version:  compat.Parse(version),
		Revision: snapshot.RevisionKey(),
		Locks:    &write.Locks{},
	}}
	return fixture, service
}

// handle 只模拟当前风险测试所需的仓库、PR、分支和审查端点。
// 所有共享状态受锁保护，不把网络 EOF 当作服务端处理完成的同步信号。
func (fixture *prFixture) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/user" {
		json.NewEncoder(w).Encode(map[string]string{"login": "alice"})
		return
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/team/repo")
	if r.Method == http.MethodGet {
		switch path {
		case "":
			json.NewEncoder(w).Encode(fixture.repository)
		case "/branches/feature", "/branches/main":
			sha := fixture.value.Head.SHA
			if strings.HasSuffix(path, "main") {
				sha = fixture.value.Base.SHA
			}
			json.NewEncoder(w).Encode(map[string]any{"name": strings.TrimPrefix(path, "/branches/"), "commit": map[string]string{"id": sha}})
		case "/pulls/1":
			if fixture.unreadable {
				w.WriteHeader(503)
			} else {
				json.NewEncoder(w).Encode(fixture.value)
			}
		case "/pulls/1/reviews":
			json.NewEncoder(w).Encode([]Review{})
		default:
			w.WriteHeader(404)
		}
		return
	}
	fixture.writes = append(fixture.writes, r.Method+" "+path)
	if err := json.NewDecoder(r.Body).Decode(&fixture.payload); err != nil {
		w.WriteHeader(400)
		return
	}
	switch path {
	case "/pulls":
		fixture.value.Title = fixture.payload["title"].(string)
		json.NewEncoder(w).Encode(fixture.value)
	case "/pulls/1/requested_reviewers":
		w.WriteHeader(403)
	case "/pulls/1/merge":
		if fixture.mode == "denied" {
			w.WriteHeader(403)
			return
		}
		fixture.value.Merged = true
		fixture.value.State = "closed"
		if fixture.mode == "disconnect" {
			fixture.unreadable = true
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
			return
		}
		w.WriteHeader(200)
	default:
		w.WriteHeader(404)
	}
}

// TestMergeHeadVersionAndUnknown 保护版本化字段、head 前置条件与单次合并请求。
// 覆盖服务器权限拒绝、已接受后断连及确认后 head 变化，不允许降级重试。
func TestMergeHeadVersionAndUnknown(t *testing.T) {
	for _, version := range []string{"1.22.6", "1.26.4", "28.3.0"} {
		for _, mode := range []string{"success", "denied", "disconnect", "head_changed", "metadata_changed"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				fixture, service := newPRFixture(t, version)
				fixture.mode = mode
				plan, err := service.Prepare(context.Background(), Request{
					Operation: "pr.merge",
					Numbers:   []int64{1},
					Method:    "merge",
				})
				if err != nil {
					t.Fatal(err)
				}
				if mode == "head_changed" {
					fixture.mu.Lock()
					fixture.value.Head.SHA = strings.Repeat("c", 40)
					fixture.mu.Unlock()
				}

				if mode == "metadata_changed" {
					fixture.mu.Lock()
					fixture.value.Mergeable = true
					fixture.value.UpdatedAt = "background refresh"
					fixture.mu.Unlock()
				}
				result, err := service.Commit(context.Background(), plan)
				if err != nil {
					t.Fatal(err)
				}
				fixture.mu.Lock()
				writes := append([]string{}, fixture.writes...)
				payload := fixture.payload
				fixture.mu.Unlock()
				expected := app.Success
				if mode == "head_changed" || mode == "denied" {
					expected = app.Failed
				} else if mode == "disconnect" {
					expected = app.Unknown
				}
				if result.Status != expected {
					t.Fatalf("unexpected merge outcome: %s", result.Status)
				}
				if mode == "head_changed" {
					if len(writes) != 0 || result.Items[0].Error.Code != app.PreconditionChanged {
						t.Fatal("changed head reached a merge request")
					}
					return
				}
				key := "do"
				if version == "1.22.6" {
					key = "Do"
				}
				if len(writes) != 1 || payload[key] != "merge" || payload["head_commit_id"] != strings.Repeat("a", 40) || payload["delete_branch_after_merge"] != false {
					t.Fatal("merge fields changed, head was unbound, or write was replayed")
				}
				if _, err := service.Commit(context.Background(), plan); err == nil {
					t.Fatal("consumed merge plan was accepted")
				}
			})
		}
	}
}

// TestLegacyCreateReviewerFailureRetainsPR 保护旧版创建后补充审查请求的部分完成结果。
// 预览明确包含补充步骤，失败后仍返回已创建 PR，不能为了审查者再创建一次。
func TestLegacyCreateReviewerFailureRetainsPR(t *testing.T) {
	fixture, service := newPRFixture(t, "1.22.6")
	title := "created PR"
	plan, err := service.Prepare(context.Background(), Request{
		Operation: "pr.create",
		Changes:   issue.Changes{Title: &title},
		Base:      "main",
		Head:      "feature",
		Users:     []string{"bob"},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := plan.Preview()
	if err != nil || len(preview.Steps) != 2 {
		t.Fatal("legacy follow-up was hidden from preview")
	}
	*preview.Request.Changes.Title = "mutated preview"
	result, err := service.Commit(context.Background(), plan)
	if err != nil || result.Status != app.Partial || result.ExitCode() != 3 {
		t.Fatal("legacy follow-up failure erased partial success")
	}
	execution := result.Items[0].Data.(write.Execution)
	value := execution.Resource.(PullRequest)
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if value.Number != 1 || value.Title != title || len(fixture.writes) != 2 || fixture.writes[0] != "POST /pulls" || fixture.writes[1] != "POST /pulls/1/requested_reviewers" {
		t.Fatal("created PR was lost, preview mutated intent, or creation was repeated")
	}
}

// TestMergedPRStateContract 保护已合并对象不能重开，以及关闭已达状态时安全跳过。
// 两种路径都不能向服务器发送状态 PATCH，不把已合并资源当作普通可变 PR。
func TestMergedPRStateContract(t *testing.T) {
	fixture, service := newPRFixture(t, "28.3.0")
	fixture.value.Merged = true
	fixture.value.State = "closed"
	for _, operation := range []string{"pr.close", "pr.reopen"} {
		plan, err := service.Prepare(context.Background(), Request{
			Operation: operation,
			Numbers:   []int64{1},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Commit(context.Background(), plan)
		if err != nil {
			t.Fatal(err)
		}
		if operation == "pr.close" && result.Items[0].Status != app.Skipped {
			t.Fatal("already-closed merged PR was not skipped")
		}
		if operation == "pr.reopen" && result.Items[0].Status != app.Failed {
			t.Fatal("merged PR was allowed to reopen")
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.writes) != 0 {
		t.Fatal("merged PR state handling reached a write request")
	}
}
