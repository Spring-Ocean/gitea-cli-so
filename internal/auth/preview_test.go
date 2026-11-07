package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
)

// TestPreviewLoginDoesNotSaveAndPinsConfirmedIdentity 保护向导的预览／保存边界。
// 预览不创建配置或密钥引用；凭证在确认前换账号时，最终保存必须拒绝且不能残留引用。
func TestPreviewLoginDoesNotSaveAndPinsConfirmedIdentity(t *testing.T) {
	var changed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/user" || request.Header.Get("Authorization") != "token synthetic-preview" {
			http.Error(writer, "unexpected fixture request", 400)
			return
		}
		login := "alice"
		if changed.Load() {
			login = "bob"
		}
		if err := json.NewEncoder(writer).Encode(map[string]string{"login": login}); err != nil {
			t.Errorf("write fixture: %v", err)
		}
	}))
	defer server.Close()
	vault := &memoryVault{values: map[string]string{}}
	service := Service{Store: config.Store{Path: filepath.Join(t.TempDir(), "config.json")}, Vault: vault}
	snapshot, err := service.Store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	value := target.Target{Name: "isolated", Instance: config.Instance{URL: server.URL, Proxy: "direct"}, Registered: true}
	options := LoginOptions{Source: "token", Secret: "synthetic-preview"}
	profile, err := service.PreviewLogin(context.Background(), value, options)
	if err != nil || profile.Login != "alice" {
		t.Fatalf("preview did not verify the API identity: %v", err)
	}
	if _, err := os.Stat(service.Store.Path); !os.IsNotExist(err) || len(vault.values) != 0 {
		t.Fatal("preview persisted the connection or credential")
	}
	changed.Store(true)
	options.ExpectedAccount = profile.Login
	if _, err := service.Login(context.Background(), value, snapshot, options); app.Failure(err).Code != app.IdentityMismatch {
		t.Fatalf("a changed identity was saved: %v", err)
	}
	if _, err := os.Stat(service.Store.Path); !os.IsNotExist(err) || len(vault.values) != 0 {
		t.Fatal("rejected identity left persisted material")
	}
	value.Registered = false
	if _, err := service.PreviewLogin(context.Background(), value, options); app.Failure(err).Code != app.AuthRequired {
		t.Fatal("preview accepted an unconfirmed instance")
	}
}
