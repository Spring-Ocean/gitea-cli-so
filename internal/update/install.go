package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

const (
	maxArchiveBytes = 128 << 20
	maxBinaryBytes  = 192 << 20
)

// Installation 返回已安装文件及保留的旧版本位置。
// 当前进程和已有 MCP 进程仍运行旧版本，调用方应提示重启而非自动重放业务。
type Installation struct {
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version"`
	Executable      string `json:"executable"`
	Backup          string `json:"backup"`
	RestartRequired bool   `json:"restart_required"`
}

// Install 为当前正在运行的程序安装指定检查结果对应的新版本。
// 只接受较新发布；重新读取固定 GitHub 标签并核对包摘要、平台和程序版本。
// 下载或验证失败保留旧程序；替换保留备份并在可恢复失败时回滚，不提权、不修改账号配置。
func (s *Service) Install(ctx context.Context, checked Result) (Installation, error) {
	executable, err := os.Executable()
	if err != nil {
		return Installation{}, app.Wrap(app.IOError, "Unable to locate the running executable.", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return Installation{}, app.Wrap(app.IOError, "Unable to resolve the installed executable.", err)
	}
	if manager := installationManager(executable); manager != "" {
		return Installation{}, app.Wrap(app.WriteDisabled, manager, nil)
	}
	return s.installAt(ctx, checked, executable)
}

// installationManager 识别由包管理器控制的路径及临时 go run 产物。
// 这些路径不做自替换，避免破坏 Homebrew／npm 的版本管理或更新临时程序。
func installationManager(executable string) string {
	path := strings.ToLower(filepath.ToSlash(executable))
	if strings.Contains(path, "/cellar/") || strings.Contains(path, "/caskroom/") {
		return "This installation is managed by Homebrew. Update it with brew upgrade sotea."
	}
	if strings.Contains(path, "/node_modules/") {
		return "This installation is managed by npm. Update the installed sotea npm package with npm install -g <package>@<channel>."
	}
	if strings.Contains(path, "/go-build") {
		return "This is a temporary go run executable. Install a prebuilt release or build sotea into a persistent directory first."
	}
	return ""
}

// installAt 在明确目标位置串行安装，供公开入口和隔离回归复用。
// 锁与 staging 均在目标同目录；重新检查原文件身份及摘要，防止覆盖并发安装或外部替换。
func (s *Service) installAt(ctx context.Context, checked Result, target string) (installation Installation, err error) {
	current, currentOK := parseVersion(checked.Current)
	latest, latestOK := parseVersion(checked.Latest)
	if !checked.Available || !currentOK || !latestOK || !strings.HasPrefix(checked.Latest, "v") || compare(latest, current) <= 0 {
		return installation, app.Wrap(app.InvalidArgument, "Check for a newer release before installing an update.", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	unlock, err := acquireLock(target + ".update.lock")
	if err != nil {
		return installation, app.Wrap(app.IOError, "Unable to lock this installation. Check directory permissions or another update process; a crashed process may leave a .update.lock file.", err)
	}
	defer func() {
		cleanupErr := unlock()
		if cleanupErr != nil && installation.Version != "" {
			err = app.Wrap(app.PartialFailure, "The update was installed, but its lock could not be removed. Restart sotea; the previous executable is at "+installation.Backup+".", errors.Join(err, cleanupErr))
		} else {
			err = errors.Join(err, cleanupErr)
		}
	}()
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return installation, app.Wrap(app.IOError, "The installed executable must remain a regular file.", err)
	}
	originalHash, err := hashFile(target)
	if err != nil {
		return installation, err
	}
	directory, err := os.MkdirTemp(filepath.Dir(target), ".sotea-update-")
	if err != nil {
		return installation, app.Wrap(app.PermissionDenied, "The installation directory is not writable. Reinstall into a user-writable directory or use your package manager.", err)
	}
	defer func() {
		cleanupErr := os.RemoveAll(directory)
		if cleanupErr != nil && installation.Version != "" {
			err = app.Wrap(app.PartialFailure, "The update was installed, but temporary files could not be removed. Restart sotea; the previous executable is at "+installation.Backup+".", errors.Join(err, cleanupErr))
		} else {
			err = errors.Join(err, cleanupErr)
		}
	}()
	filename, err := s.prepare(ctx, checked, directory)
	if err != nil {
		return installation, err
	}
	if err := ctx.Err(); err != nil {
		return installation, err
	}
	currentInfo, err := os.Lstat(target)
	if err != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(info, currentInfo) {
		return installation, app.Wrap(app.PreconditionChanged, "The installed executable changed during download. No update was applied.", err)
	}
	currentHash, err := hashFile(target)
	if err != nil || currentHash != originalHash {
		return installation, app.Wrap(app.PreconditionChanged, "The installed executable content changed during download. No update was applied.", err)
	}
	if err := os.Chmod(filename, info.Mode().Perm()|0o100); err != nil {
		return installation, app.Wrap(app.IOError, "Unable to set the staged executable permissions.", err)
	}
	backup := filepath.Join(filepath.Dir(target), filepath.Base(directory)+".previous")
	if err := ctx.Err(); err != nil {
		return installation, err
	}
	if err := replaceExecutable(filename, target, backup); err != nil {
		return installation, err
	}
	return Installation{
		Version:         checked.Latest,
		PreviousVersion: checked.Current,
		Executable:      target,
		Backup:          backup,
		RestartRequired: true,
	}, nil
}

// prepare 重新解析固定标签并准备已验证的原生可执行程序。
// 远端发布在确认后变成草稿／更名或缺少摘要时停止；缓存中的附件 URL 不用于安装。
func (s *Service) prepare(ctx context.Context, checked Result, directory string) (string, error) {
	response, err := s.get(ctx, "https://api.github.com/repos/"+Repository+"/releases/tags/"+checked.Latest, 2<<20)
	if err != nil {
		return "", err
	}
	contents, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(contents) > 2<<20 {
		return "", app.Wrap(app.NetworkError, "Unable to read the complete release metadata.", errors.Join(readErr, closeErr))
	}
	var release Release
	if err := json.Unmarshal(contents, &release); err != nil || release.Tag != checked.Latest {
		return "", app.Wrap(app.PreconditionChanged, "The confirmed release metadata no longer matches the selected tag.", err)
	}
	selected, err := s.selectRelease(checked.Current, checked.Prerelease, []Release{release})
	if err != nil || !selected.Available || !selected.Installable {
		return "", app.Wrap(app.PreconditionChanged, "The confirmed release is no longer eligible for automatic installation.", err)
	}
	archive, sums, err := s.assets(release)
	if err != nil {
		return "", err
	}
	sumsPath := filepath.Join(directory, "SHA256SUMS")
	if err := s.download(ctx, sums, sumsPath, 1<<20); err != nil {
		return "", err
	}
	sumContents, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", app.Wrap(app.IOError, "Unable to read the downloaded checksums.", err)
	}
	expected, err := checksum(sumContents, archive.Name)
	if err != nil {
		return "", err
	}
	archivePath := filepath.Join(directory, "archive")
	if err := s.download(ctx, archive, archivePath, maxArchiveBytes); err != nil {
		return "", err
	}
	actual, err := hashFile(archivePath)
	if err != nil || actual != expected {
		return "", app.Wrap(app.PreconditionChanged, "The downloaded archive does not match SHA256SUMS. The installed program was not changed.", err)
	}
	filename := filepath.Join(directory, "sotea")
	if s.goos == "windows" {
		filename += ".exe"
	}
	if err := s.extract(archivePath, filename, archive.Name); err != nil {
		return "", err
	}
	if err := s.verifyExecutable(ctx, filename, checked.Latest); err != nil {
		return "", err
	}
	return filename, nil
}

// download 保存单个已验证来源附件，严格匹配声明长度与可选 GitHub 摘要。
// 文件以排他创建写入，网络、摘要及落盘失败不能进入替换阶段。
func (s *Service) download(ctx context.Context, asset Asset, destination string, limit int64) error {
	if asset.Size <= 0 || asset.Size > limit {
		return app.Wrap(app.ResponseTooLarge, "The release asset exceeds its download limit.", nil)
	}
	response, err := s.get(ctx, asset.URL, limit)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return app.Wrap(app.IOError, "Unable to stage the release download.", errors.Join(err, response.Body.Close()))
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, limit+1))
	bodyErr := response.Body.Close()
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(copyErr, bodyErr, syncErr, closeErr); err != nil || written != asset.Size || written > limit {
		return app.Wrap(app.NetworkError, "The release download was incomplete or could not be saved.", err)
	}
	if asset.Digest != "" && asset.Digest != "sha256:"+hex.EncodeToString(hash.Sum(nil)) {
		return app.Wrap(app.PreconditionChanged, "The release download does not match the GitHub asset digest.", nil)
	}
	return nil
}

// checksum 读取精确附件对应的唯一 SHA-256 项。
// 重复、缺失、无效长度或十六进制均拒绝，不按后缀或文件基名模糊匹配。
func checksum(contents []byte, filename string) (string, error) {
	result := ""
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != filename {
			continue
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size || result != "" {
			return "", app.Wrap(app.PreconditionChanged, "SHA256SUMS contains an invalid or duplicate entry for this archive.", err)
		}
		result = strings.ToLower(fields[0])
	}
	if result == "" {
		return "", app.Wrap(app.NotFound, "SHA256SUMS does not contain this platform archive.", nil)
	}
	return result, nil
}

// hashFile 对有界普通文件计算 SHA-256，并明确传播读写关闭错误。
// 原程序和下载附件共用此边界，不把部分读取的摘要当作完整文件摘要。
func hashFile(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", app.Wrap(app.IOError, "Unable to open the update file for verification.", err)
	}
	hash := sha256.New()
	size, readErr := io.Copy(hash, io.LimitReader(file, maxBinaryBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || size > maxBinaryBytes {
		return "", app.Wrap(app.IOError, "Unable to verify the complete bounded update file.", errors.Join(readErr, closeErr))
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
