// Package auth 管理来源有界的凭证、API 身份及显式账号生命周期。
// 密钥只在认证与 HTTP 边界传递，不进入普通配置、公共结果或日志。
package auth

import (
	"context"
	"errors"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/zalando/go-keyring"
)

// Vault 是当前系统凭证存储的最小边界。
// 实现不得静默回退明文；ref 是随机引用，不是 token 本身。
type Vault interface {
	Get(context.Context, string) (string, error)
	Set(context.Context, string, string) error
	Delete(context.Context, string) error
}

// SystemVault 使用 go-keyring 访问当前用户的系统安全存储。
// macOS 为 Keychain，Windows 为 Credential Manager；底层系统对话框须由用户处理。
type SystemVault struct{}

// Get 读取 sotea 专用服务中的一个随机引用，不扫描其他应用账号。
// 底层同步调用不能强行中止，前后均检查 context；失败不返回替代凭证。
func (SystemVault) Get(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", app.Failure(err)
	}
	secret, err := keyring.Get("sotea", ref)
	if ctx.Err() != nil {
		return "", app.Failure(ctx.Err())
	}
	if err != nil {
		return "", app.Wrap(app.AuthRequired, "System credential storage is unavailable or the credential is missing. Sign in again.", err)
	}
	return secret, nil
}

// Set 将有限长度 token 保存到独立随机引用，避免覆盖当前有效记录。
// macOS 底层通过 security 交互 stdin 传密钥；限制长度以适应各平台存储边界。
func (SystemVault) Set(ctx context.Context, ref, secret string) error {
	if err := ctx.Err(); err != nil {
		return app.Failure(err)
	}
	if secret == "" || len(secret) > 1024 {
		return app.Wrap(app.InvalidArgument, "A stored token must contain between 1 and 1024 bytes.", nil)
	}
	if err := keyring.Set("sotea", ref, secret); err != nil {
		return app.Wrap(app.IOError, "Unable to save the credential in system storage. No plaintext fallback was used.", err)
	}
	return nil
}

// Delete 只删除 sotea 当前引用，缺失视为已清理。
// 不撤销服务器 token，也不接触 Git 或其他应用的密钥。
func (SystemVault) Delete(ctx context.Context, ref string) error {
	if err := ctx.Err(); err != nil {
		return app.Failure(err)
	}
	if err := keyring.Delete("sotea", ref); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return app.Wrap(app.IOError, "Unable to remove the sotea credential from system storage.", err)
	}
	return nil
}
