package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"runtime"
	"testing"
	"time"
)

// TestSystemVaultRoundTrip 仅在显式启用时验证一个全新合成系统凭证。
// 默认测试不访问真实密钥库，启用后也只操作随机 sotea 引用并在结束时清理。
func TestSystemVaultRoundTrip(t *testing.T) {
	if os.Getenv("SOTEA_TEST_SYSTEM_VAULT") != "1" {
		t.Skip("Set SOTEA_TEST_SYSTEM_VAULT=1 to test an isolated system credential.")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("The P2 system credential target is macOS or Windows.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		t.Fatal("unable to generate an isolated credential reference")
	}
	ref := "p2-test-" + hex.EncodeToString(random)
	vault := SystemVault{}
	secret := "sotea-synthetic-p2-vault"
	if err := vault.Set(ctx, ref, secret); err != nil {
		t.Fatal("isolated system credential save failed")
	}
	t.Cleanup(func() {
		if err := vault.Delete(context.Background(), ref); err != nil {
			t.Error("isolated system credential cleanup failed")
		}
	})
	value, err := vault.Get(ctx, ref)
	if err != nil || value != secret {
		t.Fatal("isolated system credential read did not match")
	}
	if err := vault.Delete(ctx, ref); err != nil {
		t.Fatal("isolated system credential delete failed")
	}
	if _, err := vault.Get(ctx, ref); err == nil {
		t.Fatal("deleted system credential remained accessible")
	}
}
