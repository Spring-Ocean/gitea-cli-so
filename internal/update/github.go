package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Repository 是唯一允许的发布来源，不接受 Gitea 实例或工具参数覆盖。
// 不使用用户 GitHub／Gitea token，公开发布查询与安装均通过 HTTPS。
const Repository = "Spring-Ocean/gitea-cli-so"

// CheckInterval 限制 Home 自动检查的频率，失败尝试也计入间隔。
// 显式 update check／install 可跳过此间隔，但仍受跨进程检查锁保护。
const CheckInterval = 5 * time.Hour

// Asset 保存 GitHub 附件的精确名称、来源、长度与可选摘要。
// URL 仅在固定发布路径下有效，下载前还会检查重定向和长度上限。
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest,omitempty"`
}

// Release 保存选择通道所需的公开元数据，不读取发布正文或远端代码。
// Draft 永远不可选择，Prerelease 与语义标签共同决定通道。
type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Result 表示当前版本与最新合格发布的比较结果。
// Available 表示有较新标签；Installable 还要求当前平台附件和 SHA256SUMS 齐备。
type Result struct {
	Current     string    `json:"current_version"`
	Latest      string    `json:"latest_version,omitempty"`
	Available   bool      `json:"update_available"`
	Installable bool      `json:"installable"`
	Prerelease  bool      `json:"prerelease_channel"`
	CheckedAt   time.Time `json:"checked_at,omitempty"`
	Cached      bool      `json:"cached"`
	ReleaseURL  string    `json:"release_url,omitempty"`
	Message     string    `json:"message"`
	release     Release
}

// Service 提供不依赖终端的检查、下载与安装服务。
// New 创建固定 HTTPS 客户端；同一服务的公开设置只在使用前确定，不能与调用并发修改。
type Service struct {
	client    *http.Client
	cachePath string
	now       func() time.Time
	goos      string
	goarch    string
}

// New 创建独立于 Gitea 认证的更新客户端及用户级缓存位置。
// 缓存定位失败时返回 IO_ERROR；调用者拥有服务，无后台进程或持久连接关闭责任。
func New() (*Service, error) {
	directory, err := os.UserCacheDir()
	if err != nil {
		return nil, app.Wrap(app.IOError, "Unable to locate the update cache directory.", err)
	}
	return &Service{
		client: &http.Client{
			Timeout:       2 * time.Minute,
			CheckRedirect: checkRedirect,
		},
		cachePath: filepath.Join(directory, "sotea", "updates.json"),
		now:       time.Now,
		goos:      runtime.GOOS,
		goarch:    runtime.GOARCH,
	}, nil
}

// checkRedirect 限制 GitHub 及其发布附件存储的 HTTPS 跳转。
// 永不接受 HTTP、用户信息或任意第三方主机，也不在跳转请求中携带认证。
func checkRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 5 || request.URL.Scheme != "https" || request.URL.User != nil || request.URL.Port() != "" {
		return errors.New("Update download redirect is not allowed.")
	}
	switch request.URL.Hostname() {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		return nil
	default:
		return errors.New("Update download left the trusted release hosts.")
	}
}

// get 创建不含凭证的 GET 请求并核对有限响应长度。
// 成功后的响应体必须由调用者关闭；失败在本函数释放响应体，不回显远端正文。
func (s *Service) get(ctx context.Context, address string, limit int64) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, app.Wrap(app.InvalidArgument, "Invalid update resource address.", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "sotea-update")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, app.Wrap(app.NetworkError, "Unable to reach GitHub Releases. Check your network and try again.", err)
	}
	if response.StatusCode != http.StatusOK || response.ContentLength > limit {
		closeErr := response.Body.Close()
		if response.ContentLength > limit {
			return nil, app.Wrap(app.ResponseTooLarge, "The update response exceeds its size limit.", closeErr)
		}
		failure := app.Wrap(app.NetworkError, fmt.Sprintf("GitHub Releases returned HTTP %d. Retry later or download the release manually.", response.StatusCode), closeErr)
		failure.HTTPStatus = response.StatusCode
		return nil, failure
	}
	return response, nil
}

// releases 读取最近一百个发布用于完整语义版本排序。
// 404 视为尚无公开发布；其他网络或限流错误不能冒充没有更新。
func (s *Service) releases(ctx context.Context) ([]Release, error) {
	response, err := s.get(ctx, "https://api.github.com/repos/"+Repository+"/releases?per_page=100", 2<<20)
	if err != nil {
		if failure := app.Failure(err); failure.HTTPStatus == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	contents, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(contents) > 2<<20 {
		return nil, app.Wrap(app.NetworkError, "Unable to read a complete bounded release list.", errors.Join(readErr, closeErr))
	}
	var releases []Release
	if err := json.Unmarshal(contents, &releases); err != nil {
		return nil, app.Wrap(app.NetworkError, "GitHub returned an invalid release list.", err)
	}
	return releases, nil
}

// selectRelease 选择通道内语义版本最高的非草稿发布，不依赖 API 返回顺序。
// 仅大于当前版本时提示更新；缺少平台文件的发布仍可提示并引导手动查看。
func (s *Service) selectRelease(current string, prerelease bool, releases []Release) (Result, error) {
	parsed, valid := parseVersion(current)
	if !valid {
		return Result{}, app.Wrap(app.VersionUnknown, "This development version cannot be compared with releases. Install a versioned sotea build.", nil)
	}
	result := Result{Current: current, Prerelease: prerelease || len(parsed.pre) != 0, Message: "No published releases are available."}
	var highest version
	found := false
	for _, release := range releases {
		candidate, valid := parseVersion(release.Tag)
		if !valid || !strings.HasPrefix(release.Tag, "v") || release.Draft || (!result.Prerelease && (release.Prerelease || len(candidate.pre) != 0)) {
			continue
		}
		if !found || compare(candidate, highest) > 0 {
			result.release = release
			highest = candidate
			found = true
		}
	}
	if !found {
		return result, nil
	}
	result.Latest = result.release.Tag
	result.ReleaseURL = "https://github.com/" + Repository + "/releases/tag/" + url.PathEscape(result.Latest)
	result.Available = compare(highest, parsed) > 0
	_, _, assetErr := s.assets(result.release)
	result.Installable = result.Available && assetErr == nil
	result.Message = "You are up to date."
	if result.Available {
		result.Message = "Update available. Run sotea update install or open Update in Home."
		if !result.Installable {
			result.Message = "Update available, but this platform has no complete installable assets. Open the release page."
		}
	}
	return result, nil
}

// assets 匹配当前平台的固定包名及同一发布的 SHA256SUMS。
// 兼容既有 pkgN 内部包名，但多个匹配项视为歧义，不猜测用户应安装哪一份。
func (s *Service) assets(release Release) (Asset, Asset, error) {
	if (s.goos != "darwin" && s.goos != "windows") || (s.goarch != "amd64" && !(s.goos == "darwin" && s.goarch == "arm64")) {
		return Asset{}, Asset{}, app.Wrap(app.CompatibilityError, "Automatic installation is supported on macOS arm64/amd64 and Windows amd64 only.", nil)
	}
	extension := ".tar.gz"
	if s.goos == "windows" {
		extension = ".zip"
	}
	pattern := regexp.MustCompile("^sotea-" + regexp.QuoteMeta(release.Tag) + "(?:-pkg[1-9][0-9]*)?-" + regexp.QuoteMeta(s.goos+"-"+s.goarch+extension) + "$")
	var archive, sums Asset
	for _, asset := range release.Assets {
		if asset.Name != "SHA256SUMS" && !pattern.MatchString(asset.Name) {
			continue
		}
		expected := "https://github.com/" + Repository + "/releases/download/" + url.PathEscape(release.Tag) + "/" + asset.Name
		if asset.URL != expected || asset.Size <= 0 {
			return Asset{}, Asset{}, app.Wrap(app.CompatibilityError, "The release asset has an invalid source or size.", nil)
		}
		if asset.Name == "SHA256SUMS" {
			if sums.Name != "" {
				return Asset{}, Asset{}, app.Wrap(app.Conflict, "The release contains duplicate checksum assets.", nil)
			}
			sums = asset
		} else {
			if archive.Name != "" {
				return Asset{}, Asset{}, app.Wrap(app.Conflict, "The release contains ambiguous platform archives.", nil)
			}
			archive = asset
		}
	}
	if archive.Name == "" || sums.Name == "" {
		return Asset{}, Asset{}, app.Wrap(app.NotFound, "This release needs a matching platform archive and SHA256SUMS before automatic installation is available.", nil)
	}
	return archive, sums, nil
}
