package fileinput

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Scope 持有显式授权目录的系统句柄以及当前进程的临时快照预算。
// 不扩大到 symlink 根外目标；使用者须在所有调用结束后 Close，不能并发关闭在用作用域。
type Scope struct {
	Base       string
	root       *os.Root
	scratch    string
	maxFile    int64
	maxScratch int64
	mu         sync.Mutex
	used       int64
}

// NewScope 校验绝对目录并打开防路径逃逸的根句柄。
// 每个文件和全部临时快照均受正数预算限制；失败不返回可用作用域。
func NewScope(directory string, maxFile, maxScratch int64) (*Scope, error) {
	if !filepath.IsAbs(directory) || maxFile <= 0 || maxScratch <= 0 {
		return nil, app.Wrap(app.InvalidArgument, "Specify an absolute working directory and positive file limits.", nil)
	}
	actual, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, app.Wrap(app.PathOutsideRoot, "Unable to resolve the authorized directory.", err)
	}
	root, err := os.OpenRoot(actual)
	if err != nil {
		return nil, app.Wrap(app.PathOutsideRoot, "Unable to open the authorized directory.", err)
	}
	scratch, err := os.MkdirTemp("", "sotea-mcp-")
	if err != nil {
		return nil, app.Wrap(app.IOError, "Unable to create private snapshot storage.", errors.Join(err, root.Close()))
	}
	return &Scope{
		Base:       actual,
		root:       root,
		scratch:    scratch,
		maxFile:    maxFile,
		maxScratch: maxScratch,
	}, nil
}

// relative 将相对根目录或根内绝对路径转成根句柄可接受的路径。
// 不按进程 cwd 解析，不接受含 .. 的输入，也不允许不同卷或根目录逃逸。
func (s *Scope) relative(name string) (string, error) {
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return "", app.Wrap(app.PathOutsideRoot, "Parent traversal is outside the authorized path contract.", nil)
		}
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(s.Base, name)
	}
	relative, err := filepath.Rel(s.Base, name)
	if err != nil || !filepath.IsLocal(relative) {
		return "", app.Wrap(app.PathOutsideRoot, "The path must stay inside the authorized working directory.", err)
	}
	return relative, nil
}

// Directory 返回仍在授权根内的真实目录，仅用于固定 Git 查询。
// 根外 symlink 或非目录拒绝；文件动作必须使用 Open 而不是按此字符串重新打开。
func (s *Scope) Directory(name string) (string, error) {
	if name == "" {
		name = "."
	}
	relative, err := s.relative(name)
	if err != nil {
		return "", err
	}
	actual, err := filepath.EvalSymlinks(filepath.Join(s.Base, relative))
	if err != nil {
		return "", app.Wrap(app.PathOutsideRoot, "Unable to resolve the authorized directory.", err)
	}
	relative, err = s.relative(actual)
	if err != nil {
		return "", err
	}
	info, err := s.root.Stat(relative)
	if err != nil || !info.IsDir() {
		return "", app.Wrap(app.PathOutsideRoot, "The authorized path is not an accessible directory.", err)
	}
	return actual, nil
}

// Open 在固定授权根句柄内打开普通文件，防止检查后路径替换逃逸。
// 文件大小不超过当前上限；成功返回的句柄由调用者关闭，取消由读取方处理。
func (s *Scope) Open(name string) (*os.File, error) {
	relative, err := s.relative(name)
	if err != nil {
		return nil, err
	}
	info, err := s.root.Stat(relative)
	if err != nil || !info.Mode().IsRegular() {
		return nil, app.Wrap(app.PathOutsideRoot, "Input must be an accessible regular file inside the authorized root.", err)
	}
	file, err := openRootFile(s.root, relative)
	if err != nil {
		return nil, app.Wrap(app.PathOutsideRoot, "Unable to open the file inside the authorized root.", err)
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > s.maxFile {
		return nil, app.Wrap(app.ResourceLimit, "Input must be a regular file within the configured file budget.", errors.Join(err, file.Close()))
	}
	return file, nil
}

// Freeze 将已受检普通文件复制到私有快照并返回只读句柄和清理函数。
// 复制有界且可取消；原文件仍归调用者关闭，快照须调用 cleanup 释放配额与磁盘资源。
func (s *Scope) Freeze(ctx context.Context, source *os.File) (*os.File, func() error, error) {
	nothing := func() error {
		return nil
	}
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > s.maxFile {
		return nil, nothing, app.Wrap(app.ResourceLimit, "The source exceeds the snapshot file budget.", err)
	}
	size := info.Size()
	s.mu.Lock()
	if size > s.maxScratch-s.used {
		s.mu.Unlock()
		return nil, nothing, app.Wrap(app.ResourceLimit, "Snapshot storage is full. Wait for active uploads to finish.", nil)
	}
	s.used += size
	s.mu.Unlock()
	file, err := os.CreateTemp(s.scratch, "snapshot-")
	if err != nil {
		s.mu.Lock()
		s.used -= size
		s.mu.Unlock()
		return nil, nothing, app.Wrap(app.IOError, "Unable to create a private file snapshot.", err)
	}
	var once sync.Once
	var cleanupErr error
	cleanup := func() error {
		once.Do(func() {
			closeErr := file.Close()
			if errors.Is(closeErr, os.ErrClosed) {
				closeErr = nil
			}
			cleanupErr = errors.Join(closeErr, os.Remove(file.Name()))
			s.mu.Lock()
			s.used -= size
			s.mu.Unlock()
		})
		return cleanupErr
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, nothing, errors.Join(err, cleanup())
	}
	copied, copyErr := io.Copy(file, &cancelReader{
		ctx:    ctx,
		reader: io.LimitReader(source, size+1),
	})
	if copyErr != nil || copied != size {
		return nil, nothing, app.Wrap(app.PreconditionChanged, "The input changed while creating its immutable snapshot.", errors.Join(copyErr, cleanup()))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, nothing, errors.Join(err, cleanup())
	}
	return file, cleanup, nil
}

// Close 释放根句柄并删除本进程独占的临时目录。
// 不扫描其他进程的文件；调用者须先取消并等待活动请求，关闭失败须上报。
func (s *Scope) Close() error {
	return errors.Join(s.root.Close(), os.RemoveAll(s.scratch))
}

// cancelReader 让普通文件复制遵守调用取消，而不接受无界输入。
// 只在同步读取边界检查 context，底层必须已验证为普通文件。
type cancelReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read 在每段复制前检查取消，保留底层短读及 EOF。
// 不关闭调用者拥有的原始文件或内部快照。
func (r *cancelReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
