package write

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Service 为 PR 和 Release 共享固定目标、认证、版本及配置修订边界。
// 不读取终端；Locks 必须来自同一入口的共享实例，未知版本只能读取。
type Service struct {
	Auth     auth.Service
	Target   target.Target
	Version  compat.Info
	Revision string
	Locks    *Locks
}

// Connection 持有当前固定仓库的客户端与已验证目标。
// 使用者必须调用 Close；写连接的 Close 同时释放进程内仓库锁。
type Connection struct {
	Client  *gitea.Client
	Target  target.Target
	Base    string
	release func()
}

// Open 按允许匿名读取的选项打开固定仓库，不改变账号或请求目标。
// 返回连接归调用者关闭；写入准备时必须传 false 并先验证精确版本。
func (s Service) Open(ctx context.Context, anonymous bool) (*Connection, error) {
	if !target.ValidRepository(s.Target.Repository) {
		return nil, app.Wrap(app.ContextRequired, "A valid owner/repo is required.", nil)
	}
	client, resolved, err := s.Auth.Open(ctx, s.Target, anonymous)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(s.Target.Repository, "/")
	return &Connection{
		Client: client,
		Target: resolved,
		Base:   "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]),
	}, nil
}

// Begin 重核已预览上下文和配置修订，再加锁并验证同一账号。
// 成功返回的连接必须关闭；等待可取消，任何失败均释放已取得的锁。
func (s Service) Begin(ctx context.Context, expected app.Context, revision string) (*Connection, error) {
	if s.Target.Name != expected.Instance || s.Target.Repository != expected.Repository || s.Locks == nil {
		return nil, app.Wrap(app.PreconditionChanged, "The prepared context or write lock is no longer available.", nil)
	}
	release, err := s.Locks.Acquire(ctx, strings.Join([]string{s.Target.Instance.URL, expected.Account, expected.Repository}, "\x00"))
	if err != nil {
		return nil, err
	}
	snapshot, err := s.Auth.Store.Load(ctx)
	if err != nil {
		release()
		return nil, err
	}
	if snapshot.RevisionKey() != revision {
		release()
		return nil, app.Wrap(app.ConfigConflict, "Configuration changed after preview. Prepare and confirm again.", nil)
	}
	s.Target.Account = expected.Account
	connection, err := s.Open(ctx, false)
	if err != nil {
		release()
		return nil, err
	}
	connection.release = release
	return connection, nil
}

// Close 释放连接池及可选写入锁，不取消其他独立调用。
// 调用者还应传递 context 以中止在途 HTTP；不能以关闭表示远端事务回滚。
func (connection *Connection) Close() {
	connection.Client.Close()
	if connection.release != nil {
		connection.release()
	}
}

// Read 读取一个完整有界 JSON 响应并关闭响应体。
// 保留服务端状态类别，目标必须是当前连接内的 API 相对路径。
func Read(ctx context.Context, client *gitea.Client, path string, destination any) error {
	response, err := client.Do(ctx, gitea.Request{Path: path})
	if err != nil {
		return err
	}
	return gitea.Decode(response, destination)
}

// Send 只发送一次 JSON 写入，允许无 JSON 的成功响应供后续读回核对。
// 接受后的响应截断或解码失败保留 unknown；不变换字段重试，也不重复创建资源。
func Send(ctx context.Context, client *gitea.Client, method, path string, payload, destination any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return app.Wrap(app.InvalidArgument, "Unable to encode the requested changes.", err)
	}
	response, err := client.Do(ctx, gitea.Request{
		Method:  method,
		Path:    path,
		Headers: http.Header{"Content-Type": {"application/json"}},
		Body:    bytes.NewReader(encoded),
	})
	if err != nil {
		return err
	}
	if err := gitea.StatusError(response.StatusCode); err != nil {
		return errors.Join(err, response.Body.Close())
	}
	if destination != nil && response.StatusCode != http.StatusNoContent {
		if err := gitea.Decode(response, destination); err != nil {
			return app.Wrap(app.ResultUnknown, "The write was accepted, but its response could not be verified. Check the server before retrying.", err)
		}
		return nil
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return app.Wrap(app.ResultUnknown, "The accepted write response was interrupted. Check the server before retrying.", err)
	}
	return nil
}

// Execution 保留最后可确认资源及每个已执行步骤，供菜单与 MCP 复用。
// Resource 不含凭证；失败不会抹去已创建对象，未知步骤不代表可安全重试。
type Execution struct {
	Resource any        `json:"resource,omitempty"`
	Steps    []app.Item `json:"steps"`
}

// Step 追加已执行步骤并汇总成功、部分完成、失败或未知状态。
// 不生成补偿操作；未执行步骤由各业务服务按确定的执行计划明确补充。
func Step(execution Execution, name string, err error) (Execution, app.Status, error) {
	status := app.Success
	if err != nil {
		status = app.Failed
		switch app.Failure(err).Code {
		case app.ResultUnknown:
			status = app.Unknown
		case app.Cancelled:
			status = app.Canceled
		}
	}
	execution.Steps = append(execution.Steps, app.Item{
		Target: name,
		Status: status,
		Error:  app.Failure(err),
	})
	return execution, app.NewResult(app.Context{}, execution.Steps, nil).Status, err
}

// Same 比较完整可观察快照，供确认后写前的前置状态检查。
// 此检查无法消除读写之间的远端竞争，合并仍必须发送服务端 head SHA 条件。
func Same(left, right any) bool {
	a, firstErr := json.Marshal(left)
	b, secondErr := json.Marshal(right)
	return firstErr == nil && secondErr == nil && sha256.Sum256(a) == sha256.Sum256(b)
}

// Systemic 判断是否应停止剩余批量，避免扩大身份或未知写入故障。
// 单目标权限、缺失和冲突留给逐项结果，不误判为服务器版本不支持。
func Systemic(err error) bool {
	if err == nil {
		return false
	}
	switch app.Failure(err).Code {
	case app.AuthRequired, app.IdentityMismatch, app.NetworkError, app.TLSError, app.ConfigError, app.ConfigConflict, app.Cancelled, app.ResultUnknown:
		return true
	}
	return false
}
