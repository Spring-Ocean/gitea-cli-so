package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/fileinput"
)

// Request 保存 Release 的明确字段和单个附件上传意图。
// 布尔指针区分未提供和 false，create 默认草稿；Tag 永远是标签名。
type Request struct {
	Operation  string  `json:"operation"`
	Tag        string  `json:"tag"`
	Title      *string `json:"title,omitempty"`
	Body       *string `json:"body,omitempty"`
	Draft      *bool   `json:"draft,omitempty"`
	Prerelease *bool   `json:"prerelease,omitempty"`
	Target     string  `json:"target,omitempty"`
	File       string  `json:"file,omitempty"`
	Name       string  `json:"name,omitempty"`
}

// FileSnapshot 保存本次上传的字节数、摘要和修改时间供预览及提交重核。
// 系统文件身份仅保留在计划私有字段，不暴露文件句柄或读取内容。
type FileSnapshot struct {
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Modified string `json:"modified"`
}

// Preview 展示明确标签、已解析提交、草稿／发布意图和附件摘要。
// Before 是最后读取对象；修改预览副本不会改变提交计划。
type Preview struct {
	Context        app.Context   `json:"context"`
	Request        Request       `json:"request"`
	Before         *Release      `json:"before,omitempty"`
	ExistingTagSHA string        `json:"existing_tag_sha,omitempty"`
	ResolvedTarget string        `json:"resolved_target,omitempty"`
	File           *FileSnapshot `json:"file,omitempty"`
	Steps          []string      `json:"steps"`
}

// Plan 持有一次 Release 意图、对象／标签前置状态及配置修订。
// 不能重复 Commit，文件在准备结束关闭，提交时重新打开并核对身份与内容。
type Plan struct {
	request   Request
	before    *Release
	context   app.Context
	revision  string
	tagSHA    string
	resolved  string
	targetRef string
	file      *FileSnapshot
	fileInfo  os.FileInfo
	consumed  atomic.Bool
}

// Preview 返回不含凭证或文件内容的深拷贝，供入口最终确认。
// 编码错误明确返回，调用者不得在无法显示完整计划时继续提交。
func (plan *Plan) Preview() (Preview, error) {
	steps := []string{plan.request.Operation}
	if plan.request.Operation == "release.create" || (plan.request.Draft != nil && !*plan.request.Draft) {
		steps = append(steps, "verify_release_fields_and_actual_tag_side_effects")
	}
	value := Preview{
		Context:        plan.context,
		Request:        plan.request,
		Before:         plan.before,
		ExistingTagSHA: plan.tagSHA,
		ResolvedTarget: plan.resolved,
		File:           plan.file,
		Steps:          steps,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return Preview{}, app.Wrap(app.IOError, "Unable to encode the release preview.", err)
	}
	var copy Preview
	if err := json.Unmarshal(encoded, &copy); err != nil {
		return Preview{}, app.Wrap(app.IOError, "Unable to copy the release preview.", err)
	}
	return copy, nil
}

// Prepare 验证单一意图、身份、Release 重复、标签和目标提交或附件文件。
// 只读操作不会创建标签；新标签必须明确 target，上传必须是单个可读普通文件。
func (s Service) Prepare(ctx context.Context, input Request) (*Plan, error) {
	if !s.Version.ExactMatch {
		return nil, app.Wrap(app.CompatibilityError, "Release writes require an exact researched server version.", nil)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, app.Wrap(app.InvalidArgument, "Unable to isolate release input.", err)
	}
	var request Request
	if err := json.Unmarshal(encoded, &request); err != nil {
		return nil, err
	}
	if err := validate(&request); err != nil {
		return nil, err
	}
	connection, err := s.Open(ctx, false)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	plan := &Plan{
		request:  request,
		context:  connection.Target.Context(),
		revision: s.Revision,
	}
	current, readErr := get(ctx, connection, request.Tag)
	if request.Operation == "release.create" {
		if readErr == nil {
			return nil, app.Wrap(app.Conflict, "A release already exists for this tag. Use release edit explicitly.", nil)
		}
		if app.Failure(readErr).HTTPStatus != 404 {
			return nil, readErr
		}
	} else {
		if readErr != nil {
			return nil, readErr
		}
		plan.before = &current
	}
	plan.tagSHA, err = tagSHA(ctx, connection, request.Tag)
	if err != nil {
		return nil, err
	}
	if request.Operation == "release.asset.upload" {
		if err := assetConflict(ctx, connection, current.ID, request.Name); err != nil {
			return nil, err
		}
		file, err := fileinput.Open(request.File)
		if err != nil {
			return nil, err
		}
		snapshot, info, inspectErr := inspectFile(ctx, file)
		closeErr := file.Close()
		if err := errors.Join(inspectErr, closeErr); err != nil {
			return nil, err
		}
		plan.file, plan.fileInfo = &snapshot, info
		return plan, nil
	}
	needsTarget := request.Operation == "release.create" || request.Target != "" || (request.Draft != nil && !*request.Draft)
	if needsTarget {
		ref := request.Target
		if ref == "" && plan.tagSHA != "" {
			ref = plan.tagSHA
		}
		if ref == "" && plan.before != nil {
			ref = plan.before.Target
		}
		if ref == "" {
			return nil, app.Wrap(app.InvalidArgument, "A new release tag requires an explicit --target commit or branch.", nil)
		}
		plan.targetRef = ref
		plan.resolved, err = commitSHA(ctx, connection, ref)
		if err != nil {
			return nil, err
		}
		if plan.tagSHA != "" && plan.tagSHA != plan.resolved {
			return nil, app.Wrap(app.Conflict, "The explicit target does not match the existing tag. Existing tags are never moved automatically.", nil)
		}
	}
	return plan, nil
}

// validate 区分创建默认值、编辑存在性与附件独立参数。
// 旧版不能清空 Release 名称或说明，显式空编辑直接拒绝，不能静默忽略。
func validate(request *Request) error {
	if !validTag(request.Tag) {
		return app.Wrap(app.InvalidArgument, "Provide an explicit valid release tag.", nil)
	}
	if request.Body != nil && len(*request.Body) > 256<<10 {
		return app.Wrap(app.InvalidArgument, "Release notes exceed the 256 KiB business limit.", nil)
	}
	switch request.Operation {
	case "release.create":
		if request.Draft == nil {
			value := true
			request.Draft = &value
		}
		if request.Prerelease == nil {
			value := false
			request.Prerelease = &value
		}
	case "release.edit":
		if request.Title == nil && request.Body == nil && request.Draft == nil && request.Prerelease == nil && request.Target == "" {
			return app.Wrap(app.InvalidArgument, "Provide at least one explicit release edit or state option.", nil)
		}
		if (request.Title != nil && *request.Title == "") || (request.Body != nil && *request.Body == "") {
			return app.Wrap(app.InvalidArgument, "Empty release titles or notes cannot be edited consistently across supported versions.", nil)
		}
	case "release.asset.upload":
		if request.Title != nil || request.Body != nil || request.Draft != nil || request.Prerelease != nil || request.Target != "" || request.File == "" {
			return app.Wrap(app.InvalidArgument, "Asset upload requires a single file without release edits.", nil)
		}
		if request.Name == "" {
			request.Name = filepath.Base(request.File)
		}
		if request.Name == "." || request.Name == ".." || strings.TrimSpace(request.Name) == "" || strings.ContainsAny(request.Name, "/\\") || strings.ContainsFunc(request.Name, unicode.IsControl) {
			return app.Wrap(app.InvalidArgument, "Provide a valid single attachment filename.", nil)
		}
		return nil
	default:
		return app.Wrap(app.InvalidArgument, "Unknown release operation.", nil)
	}
	if request.File != "" || request.Name != "" {
		return app.Wrap(app.InvalidArgument, "File options belong to asset upload.", nil)
	}
	return nil
}

// validTag 拒绝空名称、Git 非法引用片段及终端控制字符。
// 支持合法斜杠及 Unicode 标签，HTTP 端点仍按完整路径段编码。
func validTag(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " ~^:?*[\\") || strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".lock") {
			return false
		}
	}
	return true
}

// tagSHA 查询实际 Git 标签指向提交；缺失返回空字符串，其他错误保留。
// 不把不存在 Release 当作不存在标签，也不依赖草稿一定创建标签的假设。
func tagSHA(ctx context.Context, connection *write.Connection, tag string) (string, error) {
	var value struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	err := write.Read(ctx, connection.Client, connection.Base+"/tags/"+url.PathEscape(tag), &value)
	if err != nil {
		if app.Failure(err).HTTPStatus == 404 {
			return "", nil
		}
		return "", err
	}
	if value.Commit.SHA == "" {
		return "", app.Wrap(app.ServerError, "The tag response contains no commit SHA.", nil)
	}
	return value.Commit.SHA, nil
}

// commitSHA 将明确 target 解析成完整远端提交，避免分支在确认后移动改变发布目标。
// 不从本地 HEAD 或默认 main 猜测，失败不创建标签。
func commitSHA(ctx context.Context, connection *write.Connection, ref string) (string, error) {
	var value struct {
		SHA string `json:"sha"`
	}
	err := write.Read(ctx, connection.Client, connection.Base+"/git/commits/"+url.PathEscape(ref), &value)
	if err != nil {
		return "", err
	}
	if value.SHA == "" {
		return "", app.Wrap(app.ServerError, "The target response contains no commit SHA.", nil)
	}
	return value.SHA, nil
}

// inspectFile 读取普通文件摘要并核对读取前后元信息，支持主动取消。
// 完成后偏移回到开头；调用者拥有句柄并负责关闭，文件变化时拒绝提交。
func inspectFile(ctx context.Context, file *os.File) (FileSnapshot, os.FileInfo, error) {
	before, err := file.Stat()
	if err != nil {
		return FileSnapshot{}, nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return FileSnapshot{}, nil, err
	}
	digest := sha256.New()
	reader := &contextReader{
		ctx:    ctx,
		reader: file,
	}
	count, readErr := io.Copy(digest, reader)
	after, statErr := file.Stat()
	_, seekErr := file.Seek(0, io.SeekStart)
	if err := errors.Join(readErr, statErr, seekErr); err != nil {
		return FileSnapshot{}, nil, app.Failure(err)
	}
	if count != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return FileSnapshot{}, nil, app.Wrap(app.PreconditionChanged, "The attachment changed while it was being inspected.", nil)
	}
	return FileSnapshot{
		Size:     count,
		SHA256:   hex.EncodeToString(digest.Sum(nil)),
		Modified: after.ModTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}, after, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read 在每次有界读取前传播取消，不启动无法回收的文件读取 goroutine。
// 普通磁盘文件由操作系统完成单次读取，不承诺强制中断内核 I/O。
func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
