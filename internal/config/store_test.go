package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// TestStoreConflictAndCancellation 保护快照比较与不覆盖较新配置的行为。
// 使用真实临时文件，同时核对取消后原配置内容未被替换。
func TestStoreConflictAndCancellation(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "中文 path", "config.json")}
	ctx := context.Background()
	initial, err := store.Load(ctx)
	if err != nil || initial.Exists {
		t.Fatalf("missing file was not distinguished: %v", err)
	}
	next := initial.Config
	next.Instances["team"] = Instance{URL: "https://gitea.invalid/prefix"}
	saved, err := store.Save(ctx, initial, next)
	if err != nil || saved.Config.Revision != 1 {
		t.Fatalf("save failed: %v", err)
	}
	before, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, initial, next); err == nil || app.Failure(err).Code != app.ConfigConflict {
		t.Fatalf("stale snapshot was not rejected: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Save(canceled, saved, next); err == nil {
		t.Fatal("canceled save succeeded")
	}
	after, err := os.ReadFile(store.Path)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed save changed the configuration")
	}
	if err := os.WriteFile(store.Path+".lock", []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, saved, next); err == nil {
		t.Fatal("held lock was ignored")
	}
}

// TestStoreRejectsInvalidFiles 保护损坏配置与未知格式不会被静默重置。
// 同时拒绝明文 token 字段和 URL 内凭证，读取不修改文件。
func TestStoreRejectsInvalidFiles(t *testing.T) {
	cases := []string{
		"{",
		`{"version":2}`,
		`{"version":1,"token":"synthetic"}`,
		`{"version":1} {}`,
		`{"version":1,"instances":{"team":{"url":"https://name:secret@gitea.invalid"}}}`,
	}
	for _, contents := range cases {
		store := Store{Path: filepath.Join(t.TempDir(), "config.json")}
		if err := os.WriteFile(store.Path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(context.Background()); err == nil {
			t.Fatal("invalid configuration was accepted")
		}
		after, err := os.ReadFile(store.Path)
		if err != nil || string(after) != contents {
			t.Fatal("read changed the invalid file")
		}
	}
}
