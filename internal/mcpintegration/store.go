package mcpintegration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

const maxConfigBytes = 4 << 20

// ownership 记录条目上次及下一次写入的摘要，作为小型本地写入日志。
// 先持久化日志再替换客户端文件；崩溃后旧条目与新条目都能识别，不接管其他配置。
type ownership struct {
	Version  int    `json:"version"`
	Client   Client `json:"client"`
	Path     string `json:"path"`
	Previous string `json:"previous,omitempty"`
	Next     string `json:"next,omitempty"`
}

// Snapshot 保存一个目标配置和托管记录的只读快照。
// State 区分 absent、managed、unmanaged 和 changed；只有 managed 可被更新、检查或移除。
type Snapshot struct {
	Client   Client
	Path     string
	State    string
	Launch   Launch
	doc      document
	raw      []byte
	exists   bool
	metadata []byte
	owned    ownership
}

// Change 保存具体且不可变的安装、更新或移除预览。
// 调用者必须展示 Preview 并取得明确选择后才 Apply；本对象不执行终端交互。
type Change struct {
	Before    Snapshot
	Action    string
	Preview   string
	next      []byte
	entryHash string
}

// Load 有界读取客户端配置与旁路托管记录，不读取或显示其中其他设置。
// 最终文件及托管记录不允许符号链接；同名非托管项只报告状态，不能自动覆盖。
func Load(ctx context.Context, client Client, path string) (Snapshot, error) {
	snapshot := Snapshot{Client: client, Path: path, State: "absent"}
	if err := ctx.Err(); err != nil {
		return snapshot, app.Failure(err)
	}
	if !filepath.IsAbs(path) {
		return snapshot, app.Wrap(app.InvalidArgument, "Choose an absolute client configuration path.", nil)
	}
	raw, exists, err := readRegular(path)
	if err != nil {
		return snapshot, err
	}
	if exists && client != Codex && len(bytes.TrimSpace(raw)) == 0 {
		return snapshot, app.Wrap(app.ConfigError, "The existing client configuration is empty. Fix it before installing sotea.", nil)
	}
	snapshot.raw, snapshot.exists = raw, exists
	snapshot.doc, err = parseDocument(client, raw)
	if err != nil {
		return snapshot, err
	}
	metadata, _, err := readRegular(path + ".sotea-mcp.json")
	if err != nil {
		return snapshot, err
	}
	snapshot.metadata = metadata
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &snapshot.owned); err != nil || snapshot.owned.Version != 1 || snapshot.owned.Client != client || snapshot.owned.Path != path {
			return snapshot, app.Wrap(app.ConfigConflict, "The sotea ownership record is invalid or belongs to another client. Restore its matching backup before continuing.", nil)
		}
	}
	if snapshot.doc.entry == nil {
		return snapshot, nil
	}
	snapshot.State = "unmanaged"
	if len(metadata) == 0 {
		return snapshot, nil
	}
	snapshot.State = "changed"
	entry := snapshot.doc.entryBytes()
	hash := digest(entry)
	if len(entry) == 0 || (hash != snapshot.owned.Previous && hash != snapshot.owned.Next) {
		return snapshot, nil
	}
	snapshot.Launch, err = launchFromEntry(snapshot.doc.entry)
	if err != nil {
		return snapshot, err
	}
	snapshot.State = "managed"
	return snapshot, nil
}

// Prepare 生成客户端条目的具体变更，未调用 Apply 前不创建文件或目录。
// remove 仅接受托管项；安装或更新验证二进制、路径及完整序列化结果，不保存密钥。
func (s Snapshot) Prepare(launch Launch, remove bool) (Change, error) {
	change := Change{Before: s, Action: "Install"}
	if s.State == "unmanaged" || s.State == "changed" {
		return change, app.Wrap(app.ConfigConflict, "An existing sotea entry is not managed by this wizard or was edited externally. Keep it unchanged; restore the installed entry or choose another scope.", nil)
	}
	if remove && s.State != "managed" {
		return change, app.Wrap(app.InvalidArgument, "There is no managed sotea integration to remove.", nil)
	}
	if s.State == "managed" {
		change.Action = "Update"
	}
	if remove {
		change.Action = "Remove"
	}
	var entry map[string]any
	var err error
	if !remove {
		if err := validateExecutable(launch.Command); err != nil {
			return change, err
		}
		if launch.Workdir != "" {
			info, err := os.Stat(launch.Workdir)
			if err != nil || !info.IsDir() {
				return change, app.Wrap(app.InvalidArgument, "The authorized working directory does not exist.", err)
			}
		}
		entry, err = launch.Entry(s.Client)
		if err != nil {
			return change, err
		}
	}
	change.next, err = s.doc.change(entry, remove)
	if err != nil {
		return change, err
	}
	if len(change.next) > maxConfigBytes {
		return change, app.Wrap(app.ConfigError, "The resulting client configuration exceeds 4 MiB.", nil)
	}
	if !remove {
		doc, err := parseDocument(s.Client, change.next)
		if err != nil {
			return change, err
		}
		if len(doc.entryBytes()) == 0 {
			return change, app.Wrap(app.ConfigConflict, "Existing managed markers prevent safe installation. Restore the original configuration first.", nil)
		}
		change.entryHash = digest(doc.entryBytes())
		encoded, err := encodeEntry(s.Client, entry)
		if err != nil {
			return change, err
		}
		change.Preview = string(encoded)
	} else {
		change.Preview = "Remove only the managed sotea server entry. Other servers and settings remain in place."
	}
	if s.State == "managed" {
		previous, err := s.Launch.Entry(s.Client)
		if err != nil {
			return change, err
		}
		encoded, err := encodeEntry(s.Client, previous)
		if err != nil {
			return change, err
		}
		change.Preview = "Current entry\n" + string(encoded) + "\n\nProposed change\n" + change.Preview
	}
	return change, nil
}

// Apply 比较预览快照、保存受限备份和托管日志，再原子替换客户端文件。
// 返回原文件备份路径；锁或并发冲突不写客户端文件。外部编辑器不参与协作锁，最终比较与替换间仍有极小竞争窗口。
func (c Change) Apply(ctx context.Context) (backupPath string, resultErr error) {
	if err := ctx.Err(); err != nil {
		return "", app.Failure(err)
	}
	if c.next == nil || c.Before.Path == "" {
		return "", app.Wrap(app.InvalidArgument, "Prepare and review an integration change before applying it.", nil)
	}
	path := c.Before.Path
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", app.Wrap(app.IOError, "Unable to create the client configuration directory.", err)
	}
	lockPath := path + ".sotea-mcp.lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", app.Wrap(app.ConfigConflict, "The integration is locked by another writer. Check for an active writer or a stale lock.", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, os.Remove(lockPath))
	}()
	if err := lock.Close(); err != nil {
		return "", app.Wrap(app.IOError, "Unable to close the integration lock.", err)
	}
	if err := c.unchanged(ctx); err != nil {
		return "", err
	}
	if c.Before.exists {
		backupPath, err = writeBackup(ctx, path, c.Before.raw)
		if err != nil {
			return "", err
		}
	}
	owned := ownership{Version: 1, Client: c.Before.Client, Path: path, Next: c.entryHash}
	if len(c.Before.doc.entryBytes()) > 0 {
		owned.Previous = digest(c.Before.doc.entryBytes())
	}
	metadata, err := json.MarshalIndent(owned, "", "  ")
	if err != nil {
		return backupPath, app.Wrap(app.ConfigError, "Unable to encode the ownership record.", err)
	}
	if err := c.unchanged(ctx); err != nil {
		return backupPath, err
	}
	if err := replaceContents(ctx, path+".sotea-mcp.json", metadata); err != nil {
		return backupPath, err
	}
	current, exists, err := readRegular(path)
	if err != nil {
		return backupPath, err
	}
	if exists != c.Before.exists || !bytes.Equal(current, c.Before.raw) {
		return backupPath, app.Wrap(app.ConfigConflict, "The client configuration changed while saving. Reload and review a new change; the external edit was preserved.", nil)
	}
	// 日志先落盘，崩溃或替换失败时仍可识别之前及之后的自有条目。
	if err := replaceContents(ctx, path, c.next); err != nil {
		return backupPath, app.Wrap(app.IOError, "The client configuration could not be replaced. Reload the integration before trying again; the ownership journal accepts either installed version.", err)
	}
	return backupPath, nil
}

// unchanged 同时核对客户端内容、存在状态及托管记录。
// 预览之后的编辑、其他向导写入或取消均使本次变更失效，不覆盖新配置。
func (c Change) unchanged(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return app.Failure(err)
	}
	current, exists, err := readRegular(c.Before.Path)
	if err != nil {
		return err
	}
	metadata, _, err := readRegular(c.Before.Path + ".sotea-mcp.json")
	if err != nil {
		return err
	}
	if exists != c.Before.exists || !bytes.Equal(current, c.Before.raw) || !bytes.Equal(metadata, c.Before.metadata) {
		return app.Wrap(app.ConfigConflict, "The configuration changed after preview. Reload it and review a new change.", nil)
	}
	return nil
}

// readRegular 有界读取普通配置文件，并保留不存在与空文件的区别。
// 不跟随最终路径符号链接，不将无权限或损坏文件当作缺失配置。
func readRegular(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, false, app.Wrap(app.ConfigError, "The integration configuration must be a readable regular file, not a symbolic link.", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, app.Wrap(app.IOError, "Unable to open the integration configuration.", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) > maxConfigBytes {
		return nil, false, app.Wrap(app.ConfigError, "The integration configuration could not be read or exceeds 4 MiB.", errors.Join(readErr, closeErr))
	}
	return raw, true, nil
}

// digest 为本地条目生成不可逆内容标识，避免把客户端配置内容写进元数据。
// 摘要仅用于本机所有权和并发检测，不代表客户端信任或远端身份校验。
func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// writeBackup 在用户私有配置目录保存恢复副本，返回实际随机路径。
// 不把可能含旧密钥的备份放进项目仓库；备份正文不进入预览、日志或 MCP 结果。
func writeBackup(ctx context.Context, path string, raw []byte) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", app.Wrap(app.ConfigError, "Unable to locate the private backup directory.", err)
	}
	directory := filepath.Join(base, "sotea", "mcp-backups")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", app.Wrap(app.IOError, "Unable to create the private backup directory.", err)
	}
	file, err := os.CreateTemp(directory, ".sotea-mcp-backup-"+digest([]byte(path))[:12]+"-*")
	if err != nil {
		return "", app.Wrap(app.IOError, "Unable to create a private configuration backup.", err)
	}
	name := file.Name()
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr, ctx.Err()); err != nil {
		removeErr := os.Remove(name)
		return "", app.Wrap(app.IOError, "Unable to finish the configuration backup.", errors.Join(err, removeErr))
	}
	return name, nil
}

// replaceContents 在同目录临时文件完成同步后进行平台原子替换。
// 临时文件及目标均使用 0600，不先删除原文件；返回前清理失败也交回调用者。
func replaceContents(ctx context.Context, path string, raw []byte) (resultErr error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".sotea-mcp-write-*")
	if err != nil {
		return app.Wrap(app.IOError, "Unable to prepare a configuration write.", err)
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr, ctx.Err()); err != nil {
		return app.Wrap(app.IOError, "Unable to finish a configuration write.", err)
	}
	if _, _, err := readRegular(path); err != nil {
		return err
	}
	if err := replaceFile(file.Name(), path); err != nil {
		return app.Wrap(app.IOError, "Unable to atomically replace the configuration file.", err)
	}
	return nil
}
