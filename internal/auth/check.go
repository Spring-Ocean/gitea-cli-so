package auth

import (
	"context"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
)

// Check 显式重新验证当前凭证，成功后更新已有账号的验证元信息。
// 允许重新检查失效记录；失败不会换用其他账号或认证方案。
func (s Service) Check(ctx context.Context, t target.Target, snapshot config.Snapshot) (Status, error) {
	account, exists := t.Instance.Accounts[t.Account]
	account.Invalid = false
	if exists {
		t.Instance.Accounts = cloneAccounts(t.Instance.Accounts)
		t.Instance.Accounts[t.Account] = account
	}
	material, err := s.Credentials(ctx, t, false)
	if err != nil {
		return s.Status(t), err
	}
	login, err := s.Verify(ctx, t, material)
	if err != nil {
		status := s.Status(t)
		status.Invalid = app.Failure(err).HTTPStatus == 401 || status.Invalid
		return status, err
	}
	if exists {
		account.VerifiedAt = time.Now().UTC().Format(time.RFC3339)
		t.Instance.Accounts[t.Account] = account
		snapshot.Config.Instances[t.Name] = t.Instance
		if _, err := s.Store.Save(ctx, snapshot, snapshot.Config); err != nil {
			return s.Status(t), err
		}
	}
	t.Account = login
	status := s.Status(t)
	status.Invalid = false
	status.VerifiedAt = time.Now().UTC().Format(time.RFC3339)
	return status, nil
}

// cloneAccounts 复制账号映射，使临时复核不修改原始快照的失效状态。
// 字段均为值类型，不复制任何密钥材料。
func cloneAccounts(accounts map[string]config.Account) map[string]config.Account {
	copy := make(map[string]config.Account, len(accounts))
	for name, value := range accounts {
		copy[name] = value
	}
	return copy
}
