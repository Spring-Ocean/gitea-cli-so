// Package session 组合无终端依赖的逐调用上下文、认证和版本服务。
// CLI 与 MCP 共享此边界；配置每次重读，缓存与写锁由入口进程持有。
package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Environment 保存当前进程的共享适配器，不保存终端或协议对象。
// Authentication 的注入对象须可并发调用；Cache 和 Locks 必须在进程内复用。
type Environment struct {
	Authentication auth.Service
	Cache          *compat.Cache
	Locks          *write.Locks
	CAFile         string
	Proxy          string
}

// State 是一次配置解析的非敏感快照及固定目标。
// 不能跨用户调用复用；后续写提交仍须重核配置与远端身份。
type State struct {
	Target   target.Target
	Snapshot config.Snapshot
	Auth     auth.Service
}

// Resolve 重读配置并解析显式目标及允许的本地上下文。
// 只解析来源，不取凭证、不显示选择器、不保存配置；歧义和取消原样返回。
func (e *Environment) Resolve(ctx context.Context, input target.Input) (State, error) {
	state := State{Auth: e.Authentication}
	snapshot, err := e.Authentication.Store.Load(ctx)
	state.Snapshot = snapshot
	if err != nil {
		return state, err
	}
	state.Target, err = target.Resolve(ctx, input, snapshot.Config, e.Authentication.Git, e.Authentication.Getenv)
	if err != nil {
		return state, err
	}
	if e.CAFile != "" {
		state.Target.Instance.CAPath = e.CAFile
	}
	if e.Proxy != "" {
		state.Target.Instance.Proxy = e.Proxy
	}
	return state, nil
}

// Binding 返回仅供内存比较的身份／配置摘要，不返回凭证原文。
// 本地状态查询不联网；完整实例、账号、仓库、配置修订和环境来源均绑定。
func (e *Environment) Binding(state State) string {
	getenv := e.Authentication.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	caFingerprint := ""
	if state.Target.Instance.CAPath != "" {
		contents, err := os.ReadFile(state.Target.Instance.CAPath)
		if err != nil {
			caFingerprint = "unreadable"
		} else {
			digest := sha256.Sum256(contents)
			caFingerprint = hex.EncodeToString(digest[:])
		}
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		state.Target.Instance.URL,
		state.Target.Account,
		state.Target.Repository,
		state.Snapshot.RevisionKey(),
		getenv("SOTEA_TOKEN"),
		getenv("SOTEA_TOKEN_INSTANCE"),
		getenv("SOTEA_ACCOUNT"),
		state.Target.Instance.CAPath,
		caFingerprint,
		state.Target.Instance.Proxy,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

// Version 通过当前认证边界读取或刷新进程内版本证据。
// 返回值不表示整个 API 兼容；调用者不持有客户端资源，取消会中止检测。
func (e *Environment) Version(ctx context.Context, state State, refresh bool) (compat.Info, error) {
	t := state.Target
	var client *gitea.Client
	var err error
	if t.Registered {
		client, t, err = state.Auth.Open(ctx, t, true)
	} else {
		client, err = gitea.New(gitea.Options{
			BaseURL:  t.Instance.URL,
			CAFile:   t.Instance.CAPath,
			ProxyURL: t.Instance.Proxy,
		})
	}
	if err != nil {
		return compat.Info{}, err
	}
	defer client.Close()
	var ca []byte
	if t.Instance.CAPath != "" {
		ca, err = os.ReadFile(t.Instance.CAPath)
		if err != nil {
			return compat.Info{}, app.Wrap(app.TLSError, "Unable to read the configured CA file.", err)
		}
	}
	caDigest := sha256.Sum256(ca)
	state.Target = t
	key := e.Binding(state) + "\x00" + hex.EncodeToString(caDigest[:])
	if e.Cache == nil {
		return compat.Detect(ctx, client, t.Instance.URL)
	}
	return e.Cache.Get(ctx, key, refresh, func(ctx context.Context) (compat.Info, error) {
		return compat.Detect(ctx, client, t.Instance.URL)
	})
}
