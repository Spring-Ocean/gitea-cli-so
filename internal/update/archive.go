package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// extract 只取包根或单个同名目录下的 sotea 可执行文件。
// 所有条目都检查路径、类型和展开预算；不向磁盘展开文档、链接或远端指定的路径。
func (s *Service) extract(archive, destination, assetName string) error {
	binary := "sotea"
	folder := strings.TrimSuffix(assetName, ".tar.gz")
	if s.goos == "windows" {
		binary = "sotea.exe"
		folder = strings.TrimSuffix(assetName, ".zip")
	}
	found := false
	var expanded uint64
	consume := func(name string, size uint64, mode os.FileMode, source io.Reader) error {
		clean := strings.TrimSuffix(name, "/")
		if clean == "" || path.IsAbs(clean) || path.Clean(clean) != clean || strings.ContainsAny(clean, "\\:\x00") || clean == ".." || strings.HasPrefix(clean, "../") {
			return app.Wrap(app.PathOutsideRoot, "The update archive contains an unsafe entry path.", nil)
		}
		if !mode.IsRegular() && !mode.IsDir() {
			return app.Wrap(app.PathOutsideRoot, "The update archive contains links or special files.", nil)
		}
		if size > 512<<20 || expanded > (512<<20)-size {
			return app.Wrap(app.ResponseTooLarge, "The expanded update archive exceeds its size limit.", nil)
		}
		expanded += size
		if clean != binary && clean != folder+"/"+binary {
			return nil
		}
		if found || !mode.IsRegular() || size == 0 || size > maxBinaryBytes {
			return app.Wrap(app.CompatibilityError, "The archive needs exactly one bounded regular sotea executable.", nil)
		}
		found = true
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return app.Wrap(app.IOError, "Unable to create the staged executable.", err)
		}
		written, copyErr := io.Copy(file, io.LimitReader(source, int64(size)+1))
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(copyErr, syncErr, closeErr); err != nil || written != int64(size) {
			return app.Wrap(app.IOError, "Unable to extract the complete executable.", err)
		}
		return nil
	}
	if s.goos == "windows" {
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return app.Wrap(app.CompatibilityError, "Unable to open the update ZIP archive.", err)
		}
		var extractErr error
		if len(reader.File) > 4096 {
			extractErr = app.Wrap(app.ResponseTooLarge, "The update archive contains too many entries.", nil)
		} else {
			for _, file := range reader.File {
				entry, err := file.Open()
				if err != nil {
					extractErr = err
					break
				}
				err = consume(file.Name, file.UncompressedSize64, file.Mode(), entry)
				extractErr = errors.Join(err, entry.Close())
				if extractErr != nil {
					break
				}
			}
		}
		if err := errors.Join(extractErr, reader.Close()); err != nil {
			return app.Wrap(app.CompatibilityError, "The update ZIP archive is invalid or unsafe.", err)
		}
	} else {
		if err := extractTar(archive, consume); err != nil {
			return err
		}
	}
	if !found {
		return app.Wrap(app.NotFound, "The archive does not contain the expected sotea executable.", nil)
	}
	return nil
}

// extractTar 顺序校验 tar.gz 条目并限制压缩展开读取。
// 原始文件与 gzip reader 在所有路径关闭，CRC／截断错误不会视为可安装文件。
func extractTar(filename string, consume func(string, uint64, os.FileMode, io.Reader) error) (err error) {
	file, err := os.Open(filename)
	if err != nil {
		return app.Wrap(app.IOError, "Unable to read the update archive.", err)
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return app.Wrap(app.CompatibilityError, "The update gzip archive is invalid.", err)
	}
	defer func() {
		err = errors.Join(err, compressed.Close())
	}()
	limited := &io.LimitedReader{R: compressed, N: (513 << 20) + 1}
	reader := tar.NewReader(limited)
	for count := 0; ; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 4096 || header.Size < 0 {
			return app.Wrap(app.CompatibilityError, "The update tar archive is invalid or too large.", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != tar.TypeDir {
			return app.Wrap(app.PathOutsideRoot, "The update archive contains unsupported entries or links.", nil)
		}
		if err := consume(header.Name, uint64(header.Size), header.FileInfo().Mode(), reader); err != nil {
			return err
		}
	}
	_, err = io.Copy(io.Discard, limited)
	if err != nil || limited.N <= 0 {
		return app.Wrap(app.CompatibilityError, "The update gzip stream is incomplete or exceeds its expansion limit.", err)
	}
	return nil
}
