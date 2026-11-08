package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/fileinput"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Assets 按明确标签解析 Release ID 后列出独立附件资源。
// 不把标签作为 ID，不从 Release 内嵌 assets 推测完整列表。
func (s Service) Assets(ctx context.Context, tag string, page, limit int, all bool) (app.Result, error) {
	connection, err := s.Open(ctx, true)
	if err != nil {
		return app.Result{}, err
	}
	defer connection.Close()
	value, err := get(ctx, connection, tag)
	if err != nil {
		return app.Result{}, err
	}
	found, err := gitea.Pages[Asset](ctx, connection.Client, connection.Base+"/releases/"+strconv.FormatInt(value.ID, 10)+"/assets", nil, page, limit, all)
	if err != nil {
		return app.Result{}, err
	}
	return app.NewResult(connection.Target.Context(), []app.Item{{
		Target: tag,
		Status: app.Success,
		Data:   found,
	}}, nil), nil
}

// assetConflict 在完整有界附件集合中拒绝同名，提交前必须再次调用。
// 不删除、覆盖或静默改名；服务端与其他进程的竞争仍可能需要结果核对。
func assetConflict(ctx context.Context, connection *write.Connection, id int64, name string) error {
	page, err := gitea.Pages[Asset](ctx, connection.Client, connection.Base+"/releases/"+strconv.FormatInt(id, 10)+"/assets", nil, 1, 100, true)
	if err != nil {
		return err
	}
	for _, asset := range page.Items {
		if asset.Name == name {
			return app.Wrap(app.Conflict, "An attachment with this name already exists. Choose a different name; existing assets are never replaced.", nil)
		}
	}
	return nil
}

// upload 重开同一普通文件并复核摘要，再流式发送单个 multipart 附件。
// 上传期间计算实际发送摘要，接受后文件变化保留附件证据并报告部分完成。
func (s Service) upload(ctx context.Context, connection *write.Connection, plan *Plan, value Release, execution write.Execution) (write.Execution, app.Status, error) {
	opener := s.OpenInput
	if opener == nil {
		opener = fileinput.Open
	}
	file, err := opener(plan.request.File)
	if err != nil {
		return write.Step(execution, "file", err)
	}
	snapshot, info, err := inspectFile(ctx, file)
	if err != nil || !os.SameFile(info, plan.fileInfo) || !write.Same(snapshot, plan.file) {
		closeErr := file.Close()
		return write.Step(execution, "file", app.Wrap(app.PreconditionChanged, "The attachment file changed after preview. Confirm the file again.", errors.Join(err, closeErr)))
	}
	if err := assetConflict(ctx, connection, value.ID, plan.request.Name); err != nil {
		return write.Step(execution, "asset_name", errors.Join(err, file.Close()))
	}
	cleanupFrozen := func() error {
		return nil
	}
	if s.FreezeInput != nil {
		frozen, cleanup, freezeErr := s.FreezeInput(ctx, file)
		sourceCloseErr := file.Close()
		if freezeErr != nil || sourceCloseErr != nil {
			cleanupErr := cleanup()
			return write.Step(execution, "snapshot", errors.Join(freezeErr, sourceCloseErr, cleanupErr))
		}
		cleanupFrozen = cleanup
		file = frozen
		frozenSnapshot, _, inspectErr := inspectFile(ctx, file)
		if inspectErr != nil || frozenSnapshot.Size != plan.file.Size || frozenSnapshot.SHA256 != plan.file.SHA256 {
			return write.Step(execution, "snapshot", app.Wrap(app.PreconditionChanged, "The frozen content differs from the confirmed file.", errors.Join(inspectErr, cleanupFrozen())))
		}
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()
	done := make(chan error, 1)
	digest := sha256.New()
	go func() {
		part, partErr := multipartWriter.CreateFormFile("attachment", plan.request.Name)
		var count int64
		if partErr == nil {
			count, partErr = io.Copy(part, io.TeeReader(&contextReader{
				ctx:    ctx,
				reader: io.LimitReader(file, plan.file.Size+1),
			}, digest))
		}
		if partErr == nil && (count != plan.file.Size || hex.EncodeToString(digest.Sum(nil)) != plan.file.SHA256) {
			partErr = app.Wrap(app.PreconditionChanged, "The attachment changed while uploading. Check remote assets before retrying.", nil)
		}
		if partErr == nil {
			partErr = multipartWriter.Close()
		}
		done <- errors.Join(partErr, writer.CloseWithError(partErr))
	}()
	response, sendErr := connection.Client.Do(ctx, gitea.Request{
		Method:  http.MethodPost,
		Path:    connection.Base + "/releases/" + strconv.FormatInt(value.ID, 10) + "/assets",
		Query:   url.Values{"name": {plan.request.Name}},
		Headers: http.Header{"Content-Type": {contentType}},
		Body:    reader,
	})
	var asset Asset
	accepted := response != nil && response.StatusCode >= 200 && response.StatusCode < 300
	if sendErr == nil {
		sendErr = gitea.Decode(response, &asset)
		if accepted && sendErr != nil {
			sendErr = app.Wrap(app.ResultUnknown, "The upload was accepted, but its response could not be verified. Inspect assets before retrying.", sendErr)
		}
	}
	pipeErr := reader.Close()
	producerErr := <-done
	finalSnapshot, _, inspectErr := inspectFile(ctx, file)
	closeErr := errors.Join(file.Close(), cleanupFrozen())
	fileErr := errors.Join(pipeErr, producerErr, inspectErr, closeErr)
	if sendErr != nil {
		return write.Step(execution, "upload", sendErr)
	}
	if asset.ID < 1 || asset.Name != plan.request.Name || asset.Size != plan.file.Size {
		return write.Step(execution, "upload", app.Wrap(app.ResultUnknown, "The uploaded attachment response does not match the requested file. Inspect assets before retrying.", fileErr))
	}
	execution.Resource = struct {
		Release Release `json:"release"`
		Asset   Asset   `json:"asset"`
	}{value, asset}
	execution, _, err = write.Step(execution, "upload", nil)
	if err != nil {
		return write.Step(execution, "result", err)
	}
	sameContent := write.Same(finalSnapshot, plan.file)
	if s.FreezeInput != nil {
		sameContent = finalSnapshot.Size == plan.file.Size && finalSnapshot.SHA256 == plan.file.SHA256
	}
	if fileErr != nil || !sameContent {
		return write.Step(execution, "verify_file", app.Wrap(app.PartialFailure, "The attachment was created, but the source file changed or could not be verified. Inspect the returned attachment; do not upload again automatically.", fileErr))
	}
	var verified Asset
	err = write.Read(ctx, connection.Client, connection.Base+"/releases/"+strconv.FormatInt(value.ID, 10)+"/assets/"+strconv.FormatInt(asset.ID, 10), &verified)
	if err != nil || verified.ID != asset.ID || verified.Name != asset.Name || verified.Size != asset.Size {
		return write.Step(execution, "verify_asset", app.Wrap(app.ResultUnknown, "The attachment was created, but read-back verification failed. Inspect assets before retrying.", err))
	}
	return write.Step(execution, "verify_asset", nil)
}
