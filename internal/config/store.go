package config

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

// Snapshot 保存内容及其读取时的文件标识。
// Exists 区分缺失与空配置；保存时必须传回同一 Store 读取的快照。
type Snapshot struct {
	Config Config
	Exists bool
	digest [32]byte
	path   string
}

// RevisionKey 返回用于长驻进程检测配置内容变化的不可变标识。
// 缺失文件返回空字符串；已有文件按完整内容计算，不依赖外部编辑器递增 revision。
func (snapshot Snapshot) RevisionKey() string {
	if !snapshot.Exists {
		return ""
	}
	return hex.EncodeToString(snapshot.digest[:])
}

// Store 读写 Path 指定的非敏感配置文件。
// 同一文件的 sotea 写入使用排他锁协作；外部编辑器不参与该锁协议。
type Store struct {
	Path string
}

// Load 有界读取并验证配置，缺失文件返回 Exists=false 的首版空配置。
// 损坏、未知字段或未知格式均报 CONFIG_ERROR，不创建或重置任何文件。
func (s Store) Load(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, app.Failure(err)
	}
	absolute, err := filepath.Abs(s.Path)
	if err != nil || s.Path == "" {
		return Snapshot{}, app.Wrap(app.ConfigError, "The configuration path is empty or invalid.", err)
	}
	snapshot := Snapshot{
		Config: Config{
			Version:      1,
			Instances:    map[string]Instance{},
			Repositories: []Binding{},
		},
		path: absolute,
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return Snapshot{}, app.Wrap(app.ConfigError, "Configuration must be a readable regular file.", err)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return Snapshot{}, app.Wrap(app.ConfigError, "Unable to open the configuration file.", err)
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > 1<<20 {
		return Snapshot{}, app.Wrap(app.ConfigError, "Configuration could not be read or exceeds 1 MiB.", errors.Join(readErr, closeErr))
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Snapshot{}, app.Wrap(app.ConfigError, "Configuration contains invalid JSON or unknown fields.", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Snapshot{}, app.Wrap(app.ConfigError, "Configuration contains trailing JSON content.", err)
	}
	if err := validate(config); err != nil {
		return Snapshot{}, err
	}
	snapshot.Config = config
	snapshot.Exists = true
	snapshot.digest = sha256.Sum256(contents)
	return snapshot, nil
}

// Save 在排他锁内比较 expected 并替换为 next，返回实际写入后的快照。
// 冲突、锁占用或准备失败保持原文件；不自动删除遗留锁，Windows 替换需实机验收。
func (s Store) Save(ctx context.Context, expected Snapshot, next Config) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, app.Failure(err)
	}
	if err := validate(next); err != nil {
		return Snapshot{}, err
	}
	absolute, err := filepath.Abs(s.Path)
	if err != nil || expected.path != absolute || s.Path == "" {
		return Snapshot{}, app.Wrap(app.ConfigError, "Save requires a snapshot loaded from this configuration file.", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return Snapshot{}, app.Wrap(app.ConfigError, "Unable to create the configuration directory.", err)
	}
	lockPath := absolute + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Snapshot{}, app.Wrap(app.ConfigConflict, "Configuration lock is unavailable. Check for another writer or a stale lock.", err)
	}
	defer os.Remove(lockPath)
	if err := lock.Close(); err != nil {
		return Snapshot{}, app.Wrap(app.ConfigError, "Unable to close the configuration lock file.", err)
	}
	current, err := s.Load(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if current.Exists != expected.Exists || current.digest != expected.digest {
		return Snapshot{}, app.Wrap(app.ConfigConflict, "Configuration changed. Reload it before saving.", nil)
	}
	next.Revision = current.Config.Revision + 1
	if next.Revision == 0 {
		return Snapshot{}, app.Wrap(app.ConfigError, "Configuration revision is exhausted.", nil)
	}
	contents, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return Snapshot{}, app.Wrap(app.ConfigError, "Unable to encode configuration.", err)
	}
	contents = append(contents, '\n')
	if len(contents) > 1<<20 {
		return Snapshot{}, app.Wrap(app.ConfigError, "Configuration must not exceed 1 MiB.", nil)
	}
	if err := writeTemporary(ctx, absolute, contents); err != nil {
		if errors.Is(err, context.Canceled) {
			return Snapshot{}, app.Failure(err)
		}
		return Snapshot{}, app.Wrap(app.ConfigError, "Configuration write failed before replacement completed.", err)
	}
	return Snapshot{
		Config: next,
		Exists: true,
		digest: sha256.Sum256(contents),
		path:   absolute,
	}, nil
}

// writeTemporary 在目标目录完成写入及同步后替换配置。
// 不先删除旧文件，失败时清理临时文件；替换前再次检查取消。
func writeTemporary(ctx context.Context, target string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".sotea-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(contents)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return replaceFile(temporary.Name(), target)
}
