package update

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// cacheRecord 持久记录检查尝试及所选发布，不写入凭证或本地仓库上下文。
// 请求前保存 Attempt，网络失败、取消及其他进程启动均遵守相同冷却时间。
type cacheRecord struct {
	Attempt    time.Time `json:"attempt"`
	Prerelease bool      `json:"prerelease"`
	Release    Release   `json:"release"`
	Error      string    `json:"error,omitempty"`
}

// Check 查询公开 Releases，或在自动模式下复用五小时内的缓存。
// force 仅供用户显式检查／安装；失败也写入冷却，跨进程排他锁防止重复请求。
// 网络最多八秒；错误不修改程序或 Gitea 配置，入口决定是否展示后台错误。
func (s *Service) Check(ctx context.Context, current string, prerelease, force bool) (result Result, err error) {
	result, err = s.selectRelease(current, prerelease, nil)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(s.cachePath), 0o700); err != nil {
		return result, app.Wrap(app.IOError, "Unable to create the update cache directory.", err)
	}
	unlock, lockErr := acquireLock(s.cachePath + ".lock")
	if lockErr != nil {
		if !force && errors.Is(lockErr, os.ErrExist) {
			return s.cachedResult(current, prerelease)
		}
		return result, app.Wrap(app.Conflict, "Another update check is running. Try again later; if a previous process crashed, remove the stale updates.json.lock from the sotea cache directory.", lockErr)
	}
	defer func() {
		err = errors.Join(err, unlock())
	}()
	record, err := s.loadCache()
	if err != nil {
		return result, err
	}
	if !force && !record.Attempt.IsZero() && s.now().Sub(record.Attempt) < CheckInterval {
		return s.resultFromCache(current, prerelease, record)
	}
	record = cacheRecord{Attempt: s.now().UTC(), Prerelease: result.Prerelease}
	if err := s.saveCache(record); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	releases, checkErr := s.releases(ctx)
	if checkErr == nil {
		result, checkErr = s.selectRelease(current, prerelease, releases)
		record.Release = result.release
	} else {
		record.Error = app.Failure(checkErr).Message
	}
	if s.credentialRejected != nil && s.credentialRejected.Load() {
		result.Warning = "GitHub credential was rejected; this check retried anonymously once. Refresh your GitHub credential or select anonymous authentication."
	}
	result.CheckedAt = record.Attempt
	return result, errors.Join(checkErr, s.saveCache(record))
}

// cachedResult 在另一个进程持锁时只读已提交的缓存，不发起网络请求。
// 首次检查尚未保存时返回尚无结果，不阻塞 Home。
func (s *Service) cachedResult(current string, prerelease bool) (Result, error) {
	record, err := s.loadCache()
	if err != nil {
		return Result{}, err
	}
	return s.resultFromCache(current, prerelease, record)
}

// resultFromCache 根据当前版本重算提示，不复用旧程序的 Available 布尔值。
// 通道变化仍遵守共享冷却；上次失败保留具体错误，不能误报已是最新版。
func (s *Service) resultFromCache(current string, prerelease bool, record cacheRecord) (Result, error) {
	result, err := s.selectRelease(current, prerelease, []Release{record.Release})
	if err != nil {
		return result, err
	}
	result.Cached = true
	result.CheckedAt = record.Attempt
	if record.Error != "" {
		return result, app.Wrap(app.NetworkError, record.Error, nil)
	}
	if result.Prerelease != record.Prerelease && !record.Attempt.IsZero() {
		result.Message = "The update channel changed. Automatic checks resume after the five-hour interval; run sotea update check to check now."
	}
	return result, nil
}

// loadCache 有界读取普通缓存文件，缺失返回零值。
// 损坏缓存保守报错而不继续网络，避免多个进程绕过冷却。
func (s *Service) loadCache() (cacheRecord, error) {
	info, err := os.Lstat(s.cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return cacheRecord{}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return cacheRecord{}, app.Wrap(app.IOError, "The update cache is not a readable bounded regular file. Remove the sotea updates.json cache and try again.", err)
	}
	file, err := os.Open(s.cachePath)
	if err != nil {
		return cacheRecord{}, app.Wrap(app.IOError, "Unable to read the update cache.", err)
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > 2<<20 {
		return cacheRecord{}, app.Wrap(app.IOError, "Unable to read the complete update cache.", errors.Join(readErr, closeErr))
	}
	var record cacheRecord
	if err := json.Unmarshal(contents, &record); err != nil {
		return record, app.Wrap(app.IOError, "The update cache contains invalid JSON. Remove the sotea updates.json cache and try again.", err)
	}
	return record, nil
}

// saveCache 先同步同目录临时文件，再替换缓存目录项。
// 失败保留已提交的记录并清理临时文件，不直接截断原缓存。
func (s *Service) saveCache(record cacheRecord) (err error) {
	contents, err := json.Marshal(record)
	if err != nil {
		return app.Wrap(app.IOError, "Unable to encode the update cache.", err)
	}
	file, err := os.CreateTemp(filepath.Dir(s.cachePath), ".updates-*")
	if err != nil {
		return app.Wrap(app.IOError, "Unable to stage the update cache.", err)
	}
	defer func() {
		if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := file.Write(contents)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return app.Wrap(app.IOError, "Unable to save the update cache.", err)
	}
	if err := os.Rename(file.Name(), s.cachePath); err != nil {
		return app.Wrap(app.IOError, "Unable to replace the update cache.", err)
	}
	return nil
}

// acquireLock 为缓存或安装取得同目录排他锁，返回只删除自身锁的释放函数。
// 不接管遗留锁；崩溃后的手动清理提示避免错误删除仍在使用的并发锁。
func acquireLock(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	return func() error {
		return os.Remove(path)
	}, nil
}
