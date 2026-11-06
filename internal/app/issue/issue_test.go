package issue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
)

type issueFixture struct {
	mu                 sync.Mutex
	values             map[int64]Issue
	writes             []string
	rejectLabels       bool
	disconnect         bool
	cancelWrite        chan struct{}
	repeatPage         bool
	largeScan          bool
	dropCreated        string
	readFailure        bool
	failReadAfterWrite bool
}

// inspect 在同步边界下复制服务端可观察状态和写入记录。
// HTTP 断连可能早于 handler 返回，不能把客户端 EOF 当作共享内存同步。
func (fixture *issueFixture) inspect(index int64) (Issue, []string) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.values[index], append([]string{}, fixture.writes...)
}

// newIssueFixture 创建隔离 HTTP／配置和固定版本，不查询系统凭证。
// 默认包含一个 PR 编号，写故障在服务端接收后注入以验证未知结果。
func newIssueFixture(t *testing.T, version string) (*issueFixture, Service) {
	t.Helper()
	fixture := &issueFixture{values: map[int64]Issue{
		1: {
			Number: 1,
			Title:  "first",
			State:  "open",
			Labels: []Label{{
				ID:   1,
				Name: "bug",
			}},
			Assignees: []User{{Login: "alice"}},
			UpdatedAt: "initial",
		},
		2: {
			Number:      2,
			Title:       "pull",
			State:       "open",
			PullRequest: json.RawMessage(`{"merged":false}`),
		},
		3: {
			Number:    3,
			Title:     "third",
			State:     "open",
			UpdatedAt: "initial",
		},
	}}
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
	authentication := auth.Service{
		Store: store,
		Getenv: func(name string) string {
			if name == "SOTEA_TOKEN" {
				return "synthetic-issue-token"
			}
			return ""
		},
	}
	service := Service{
		Auth: authentication,
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
	}
	return fixture, service
}

// handle 实现边界测试所需的最小 Issue API，不模拟客户端内部调用顺序。
// 写请求在字段实际应用后才注入断连，计数可证明客户端没有重放。
func (fixture *issueFixture) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/user" {
		json.NewEncoder(w).Encode(map[string]string{"login": "alice"})
		return
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if r.URL.Path == "/api/v1/repos/team/repo/labels" {
		json.NewEncoder(w).Encode([]Label{{
			ID:   1,
			Name: "bug",
		}, {
			ID:   2,
			Name: "help",
		}})
		return
	}
	if r.URL.Path == "/api/v1/repos/team/repo/issues" && r.Method == http.MethodPost {
		var changes Changes
		if err := json.NewDecoder(r.Body).Decode(&changes); err != nil || changes.Title == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.writes = append(fixture.writes, r.Method+" "+r.URL.Path)
		value := Issue{
			Number: 4,
			Title:  *changes.Title,
			State:  "open",
		}
		if changes.Body != nil && fixture.dropCreated != "body" {
			value.Body = *changes.Body
		}
		if changes.Assignees != nil && fixture.dropCreated != "assignees" {
			for _, login := range *changes.Assignees {
				value.Assignees = append(value.Assignees, User{Login: login})
			}
		}
		if changes.Labels != nil && fixture.dropCreated != "labels" {
			for _, id := range *changes.Labels {
				value.Labels = append(value.Labels, Label{ID: id})
			}
		}
		fixture.values[4] = value
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(value)
		return
	}
	if r.URL.Path == "/api/v1/repos/team/repo/issues" && r.Method == http.MethodGet {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 {
			w.WriteHeader(400)
			return
		}
		if fixture.repeatPage {
			page = 1
		}
		values := []Issue{}
		if fixture.largeScan {
			for i := 0; i < 100; i++ {
				number := int64((page-1)*100 + i + 1)
				values = append(values, Issue{
					Number: number,
					Title:  "scan",
					State:  "open",
				})
			}
			w.Header().Set("Link", `</next>; rel="next"`)
		} else {
			keys := []int{}
			for number, issue := range fixture.values {
				matched := true
				if references := r.URL.Query().Get("labels"); references != "" {
					for _, reference := range strings.Split(references, ",") {
						found := false
						for _, label := range issue.Labels {
							found = found || label.Name == reference
						}
						matched = matched && found
					}
				}
				if !matched {
					continue
				}
				keys = append(keys, int(number))
			}
			sort.Ints(keys)
			start := (page - 1) * 2
			end := start + 2
			if end > len(keys) {
				end = len(keys)
			}
			if start > len(keys) {
				start = len(keys)
			}
			for _, number := range keys[start:end] {
				values = append(values, fixture.values[int64(number)])
			}
			if end < len(keys) || fixture.repeatPage {
				w.Header().Set("Link", `</next>; rel="next"`)
			}
		}
		json.NewEncoder(w).Encode(values)
		return
	}
	prefix := "/api/v1/repos/team/repo/issues/"
	remainder := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(remainder, "/")
	index, err := strconv.ParseInt(parts[0], 10, 64)
	value, exists := fixture.values[index]
	if err != nil || !exists {
		w.WriteHeader(404)
		return
	}
	if r.Method == http.MethodGet {
		if fixture.readFailure || (fixture.failReadAfterWrite && len(fixture.writes) > 0) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(value)
		return
	}
	fixture.writes = append(fixture.writes, r.Method+" "+r.URL.Path)
	var payload struct {
		Body      *string   `json:"body"`
		State     *string   `json:"state"`
		Assignees *[]string `json:"assignees"`
		Labels    *[]int64  `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(400)
		return
	}
	if len(parts) > 1 && parts[1] == "labels" && fixture.rejectLabels {
		w.WriteHeader(403)
		return
	}
	if payload.Body != nil {
		value.Body = *payload.Body
	}
	if payload.State != nil {
		value.State = *payload.State
	}
	if payload.Assignees != nil {
		names := *payload.Assignees
		if len(parts) > 1 {
			current := usernames(value.Assignees)
			if r.Method == http.MethodPost {
				names = append(current, names...)
			} else {
				names = []string{}
				for _, name := range current {
					remove := false
					for _, requested := range *payload.Assignees {
						remove = remove || name == requested
					}
					if !remove {
						names = append(names, name)
					}
				}
			}
		}
		value.Assignees = []User{}
		for _, name := range names {
			value.Assignees = append(value.Assignees, User{Login: name})
		}
	}
	value.UpdatedAt = strconv.Itoa(len(fixture.writes))
	fixture.values[index] = value
	if fixture.cancelWrite != nil {
		close(fixture.cancelWrite)
		<-r.Context().Done()
		return
	}
	if fixture.disconnect {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			connection.Close()
		}
		return
	}
	json.NewEncoder(w).Encode(value)
}

// TestReadbackFailureRetainsLastObservedIssue 保护状态写入后读回失败仍保留可确认的资源编号。
// 旧快照不冒充写后状态，逐项 unknown 明确要求先查远端且不得重放写入。
func TestReadbackFailureRetainsLastObservedIssue(t *testing.T) {
	fixture, service := newIssueFixture(t, "28.3.0")
	fixture.failReadAfterWrite = true
	plan, err := service.Prepare(context.Background(), Request{
		Operation: "issue.close",
		Numbers:   []int64{1},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Commit(context.Background(), plan)
	if err != nil || result.Status != app.Unknown {
		t.Fatal("failed readback lost its unknown outcome")
	}
	execution := result.Items[0].Data.(Execution)
	_, writes := fixture.inspect(1)
	if execution.Issue == nil || execution.Issue.Number != 1 || execution.Issue.State != "open" || len(writes) != 1 {
		t.Fatal("last observed resource was erased or a write was replayed")
	}
}

// TestCreatedFieldsVerifiedWithoutRecreating 保护创建成功后字段遗漏或读回失败的部分完成语义。
// 返回必须保留已创建编号和成功步骤，不能静默成功或再次发送 POST。
func TestCreatedFieldsVerifiedWithoutRecreating(t *testing.T) {
	for _, scenario := range []string{"complete", "body", "assignees", "labels", "read_failure"} {
		t.Run(scenario, func(t *testing.T) {
			fixture, service := newIssueFixture(t, "1.22.6")
			fixture.dropCreated = scenario
			fixture.readFailure = scenario == "read_failure"
			title, body := "created", "explicit body"
			users := []string{"alice"}
			labels := []int64{1}
			plan, err := service.Prepare(context.Background(), Request{
				Operation: "issue.create",
				Changes: Changes{
					Title:     &title,
					Body:      &body,
					Assignees: &users,
					Labels:    &labels,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Commit(context.Background(), plan)
			_, writes := fixture.inspect(4)
			if err != nil || len(writes) != 1 {
				t.Fatalf("create failed or was repeated: %v", err)
			}
			execution := result.Items[0].Data.(Execution)
			if execution.Issue == nil || execution.Issue.Number != 4 || execution.Steps[0].Status != app.Success {
				t.Fatal("created resource evidence was lost")
			}
			if scenario == "complete" {
				if result.Status != app.Success {
					t.Fatal("complete creation was not accepted")
				}
			} else if result.Status != app.Partial || result.ExitCode() != 3 || len(execution.Steps) != 2 {
				t.Fatal("incomplete creation was not reported as partial")
			}
		})
	}
}

// TestAnyLabelSemanticsSurviveSearchAndVersionFallback 保护多标签任一匹配不因搜索或版本改变。
// 合成服务模拟全部标签匹配，客户端必须完整读取后计算自己的公开契约。
func TestAnyLabelSemanticsSurviveSearchAndVersionFallback(t *testing.T) {
	for _, version := range []string{"1.22.6", "28.3.0", "1.28.0"} {
		t.Run(version, func(t *testing.T) {
			fixture, service := newIssueFixture(t, version)
			fixture.values[1] = Issue{
				Number: 1,
				Title:  "needle",
				State:  "open",
				Labels: []Label{{
					ID:   1,
					Name: "bug",
				}},
			}
			fixture.values[3] = Issue{
				Number: 3,
				Title:  "needle",
				State:  "open",
				Labels: []Label{{
					ID:   2,
					Name: "help",
				}},
			}
			fixture.values[4] = Issue{
				Number: 4,
				Title:  "needle",
				State:  "open",
				Labels: []Label{{
					ID:   1,
					Name: "bug",
				}, {
					ID:   2,
					Name: "help",
				}},
			}
			for _, search := range []string{"", "needle"} {
				result, err := service.List(context.Background(), Filter{
					State:  "open",
					Search: search,
					Labels: []string{"bug", "help"},
					Page:   1,
					Limit:  30,
				})
				if err != nil {
					t.Fatal(err)
				}
				page := result.Items[0].Data.(app.Page[Issue])
				if len(page.Items) != 3 || page.Items[0].Number != 1 || page.Items[1].Number != 3 || page.Items[2].Number != 4 || page.HasMore {
					t.Fatal("any-label filtering changed across query paths")
				}
			}
		})
	}
}

// TestFallbackFiltersCompleteBeforePaging 保护未知版本完整扫描后再筛选和分页。
// 包含 PR 混入、服务端较小页上限、重复页与扫描上限，不能伪报完整结果。
func TestFallbackFiltersCompleteBeforePaging(t *testing.T) {
	fixture, service := newIssueFixture(t, "1.28.0")
	fixture.mu.Lock()
	third := fixture.values[3]
	third.Body = "needle"
	fixture.values[3] = third
	fixture.values[4] = Issue{
		Number: 4,
		Title:  "needle fourth",
		State:  "open",
	}
	fixture.mu.Unlock()
	ctx := context.Background()
	filter := Filter{
		State:  "all",
		Search: "needle",
		Page:   1,
		Limit:  1,
	}
	result, err := service.List(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	page := result.Items[0].Data.(app.Page[Issue])
	if len(page.Items) != 1 || page.Items[0].Number != 3 || !page.HasMore {
		t.Fatal("fallback filtered only the first server page")
	}
	filter.Page = 2
	result, err = service.List(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	page = result.Items[0].Data.(app.Page[Issue])
	if len(page.Items) != 1 || page.Items[0].Number != 4 || page.HasMore {
		t.Fatal("fallback pagination or PR exclusion failed")
	}
	fixture.mu.Lock()
	fixture.repeatPage = true
	fixture.mu.Unlock()
	if _, err := service.List(ctx, filter); err == nil || app.Failure(err).Code != app.FilterLimit {
		t.Fatal("repeated server pages were accepted")
	}
	fixture.mu.Lock()
	fixture.repeatPage = false
	fixture.largeScan = true
	fixture.mu.Unlock()
	if _, err := service.List(ctx, filter); err == nil || app.Failure(err).Code != app.FilterLimit {
		t.Fatal("scan limit was ignored")
	}
}

// TestTypePreconditionsAndVersionedAssignees 保护 PR 类型、单次计划、可见并发与旧版增量。
// 新旧端点均核对非目标负责人保留，预览副本不得改变实际请求。
func TestTypePreconditionsAndVersionedAssignees(t *testing.T) {
	for _, version := range []string{"1.22.6", "28.3.0"} {
		t.Run(version, func(t *testing.T) {
			fixture, service := newIssueFixture(t, version)
			ctx := context.Background()
			plan, err := service.Prepare(ctx, Request{
				Operation: "issue.assignee.add",
				Numbers:   []int64{1},
				Users:     []string{"bob"},
			})
			if err != nil {
				t.Fatal(err)
			}
			preview := plan.Preview()
			preview.Users[0] = "mallory"
			result, err := service.Commit(ctx, plan)
			current, writes := fixture.inspect(1)
			if err != nil || result.Status != app.Success || !equalUsers(usernames(current.Assignees), []string{"alice", "bob"}) {
				t.Fatalf("assignee delta was not preserved: %v", err)
			}
			expected := "PATCH"
			if version == "28.3.0" {
				expected = "POST"
			}
			if !strings.HasPrefix(writes[0], expected+" ") {
				t.Fatal("assignee strategy ignored the exact server version")
			}
			before := len(writes)
			_, repeatErr := service.Commit(ctx, plan)
			_, writes = fixture.inspect(1)
			if repeatErr == nil || len(writes) != before {
				t.Fatal("a consumed plan wrote twice")
			}
			plan, err = service.Prepare(ctx, Request{
				Operation: "issue.close",
				Numbers:   []int64{2, 1, 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err = service.Commit(ctx, plan)
			if err != nil || result.Status != app.Partial || len(result.Items) != 2 || result.Items[0].Error.Code != app.ResourceTypeMismatch || result.Items[1].Status != app.Success {
				t.Fatal("PR type rejection or batch result accounting failed")
			}
			plan, err = service.Prepare(ctx, Request{
				Operation: "issue.reopen",
				Numbers:   []int64{1},
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			value := fixture.values[1]
			value.Body = "concurrent update"
			fixture.values[1] = value
			fixture.mu.Unlock()
			_, writes = fixture.inspect(1)
			before = len(writes)
			result, err = service.Commit(ctx, plan)
			_, writes = fixture.inspect(1)
			if err != nil || result.Items[0].Error.Code != app.PreconditionChanged || len(writes) != before {
				t.Fatal("visible concurrent changes were overwritten")
			}
		})
	}
}

// TestMultiStepPartialAndUnknownWrite 保护多步骤部分成功与断连后停止剩余目标。
// 失败结果必须保留完成步骤，服务端收到的写入次数不能因未知状态增加。
func TestMultiStepPartialAndUnknownWrite(t *testing.T) {
	fixture, service := newIssueFixture(t, "1.22.6")
	body := "new body"
	ctx := context.Background()
	plan, err := service.Prepare(ctx, Request{
		Operation: "issue.edit",
		Numbers:   []int64{1},
		Changes:   Changes{Body: &body},
		LabelRefs: []string{"help"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.rejectLabels = true
	fixture.mu.Unlock()
	result, err := service.Commit(ctx, plan)
	current, writes := fixture.inspect(1)
	if err != nil || result.Status != app.Partial || result.ExitCode() != 3 || current.Body != "new body" {
		t.Fatal("completed edit step was lost after label failure")
	}
	data := result.Items[0].Data.(Execution)
	if len(data.Steps) != 2 || data.Steps[0].Status != app.Success || data.Steps[1].Status != app.Failed {
		t.Fatal("partial target did not preserve step statuses")
	}
	fixture.mu.Lock()
	fixture.disconnect = true
	fixture.mu.Unlock()
	plan, err = service.Prepare(ctx, Request{
		Operation: "issue.close",
		Numbers:   []int64{1, 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	before := len(writes)
	result, err = service.Commit(ctx, plan)
	_, writes = fixture.inspect(1)
	if err != nil || result.Status != app.Unknown || result.ExitCode() != 4 || result.Items[1].Status != app.NotExecuted || len(writes) != before+1 {
		t.Fatal("unknown write was replayed or the remaining batch continued")
	}
}

// TestCancellationRetainsUnknownAndUnexecuted 保护发送后的取消优先退出码及逐项结果。
// 通过服务端接收屏障取消，不能把真实已发送请求说成未执行或已回滚。
func TestCancellationRetainsUnknownAndUnexecuted(t *testing.T) {
	fixture, service := newIssueFixture(t, "28.3.0")
	received := make(chan struct{})
	fixture.cancelWrite = received
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	plan, err := service.Prepare(ctx, Request{
		Operation: "issue.close",
		Numbers:   []int64{1, 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan app.Result, 1)
	failures := make(chan error, 1)
	go func() {
		result, err := service.Commit(ctx, plan)
		failures <- err
		done <- result
	}()
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("write did not reach the cancellation barrier")
	}
	cancel()
	result := <-done
	if err := <-failures; err != nil || result.ExitCode() != 130 || result.Items[0].Status != app.Unknown || result.Items[1].Status != app.NotExecuted {
		t.Fatal("cancelled write lost unknown or not-executed evidence")
	}
}
