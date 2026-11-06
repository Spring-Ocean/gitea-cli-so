// Package config 管理带版本的非敏感配置。
// 密钥由认证层存储，本包只保存引用、上下文绑定及退出抑制标记。
package config

import (
	"net/url"
	"os"
	"path/filepath"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Config 是首版配置格式，Revision 用于长驻进程检测修改。
// Instances 以别名索引，所有账号数据只包含密钥引用。
type Config struct {
	Version         int                 `json:"version"`
	Revision        uint64              `json:"revision"`
	DefaultInstance string              `json:"default_instance,omitempty"`
	Instances       map[string]Instance `json:"instances"`
	Repositories    []Binding           `json:"repositories"`
}

// Instance 保存完整服务根地址及账号引用。
// LoggedOut 按该实例内的已验证账号记录抑制状态，不能据此删除共享 Git 凭证。
type Instance struct {
	URL           string             `json:"url"`
	CAPath        string             `json:"ca_path,omitempty"`
	Proxy         string             `json:"proxy,omitempty"`
	SSHMappings   map[string]string  `json:"ssh_mappings,omitempty"`
	ActiveAccount string             `json:"active_account,omitempty"`
	Accounts      map[string]Account `json:"accounts,omitempty"`
	LoggedOut     map[string]bool    `json:"logged_out,omitempty"`
}

// Account 关联 API 身份与凭证来源，不保存密钥值。
// Helper 字段保留原始 Git 查询上下文，占位用户名不等于 API 账号。
type Account struct {
	CredentialRef      string `json:"credential_ref,omitempty"`
	Source             string `json:"source"`
	CredentialUsername string `json:"credential_username,omitempty"`
	HelperProtocol     string `json:"helper_protocol,omitempty"`
	HelperHost         string `json:"helper_host,omitempty"`
	HelperPath         string `json:"helper_path,omitempty"`
}

// Binding 将本地路径及 remote 与实例关联。
// Account 是显式绑定，实际身份仍须由认证服务验证。
type Binding struct {
	Path     string `json:"path"`
	Remote   string `json:"remote,omitempty"`
	Instance string `json:"instance"`
	Account  string `json:"account,omitempty"`
}

// ResolvePath 按显式参数、SOTEA_CONFIG、系统用户配置目录的顺序选择位置。
// getenv 可注入隔离环境；空值使用 os.Getenv，最终返回绝对路径且不创建文件。
func ResolvePath(explicit string, getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	path := explicit
	if path == "" {
		path = getenv("SOTEA_CONFIG")
	}
	if path == "" {
		directory, err := os.UserConfigDir()
		if err != nil {
			return "", app.Wrap(app.ConfigError, "Unable to locate the user configuration directory.", err)
		}
		path = filepath.Join(directory, "sotea", "config.json")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", app.Wrap(app.ConfigError, "Invalid configuration path.", err)
	}
	return absolute, nil
}

// validate 检查格式版本、实例地址及引用完整性。
// 拒绝含 URL 密钥或不存在绑定的配置，避免错误来源和敏感值被持久化。
func validate(config Config) error {
	if config.Version != 1 {
		return app.Wrap(app.ConfigError, "Unsupported configuration version. Use a compatible release or migrate the configuration.", nil)
	}
	for name, instance := range config.Instances {
		address, err := url.Parse(instance.URL)
		if err != nil || name == "" || address.Hostname() == "" ||
			(address.Scheme != "https" && address.Scheme != "http") ||
			address.User != nil || address.RawQuery != "" || address.Fragment != "" {
			return app.Wrap(app.ConfigError, "Invalid instance name or base URL. Credentials are not allowed in instance URLs.", nil)
		}
		if instance.Proxy != "" && instance.Proxy != "direct" {
			proxy, err := url.Parse(instance.Proxy)
			if err != nil || proxy.User != nil || proxy.Hostname() == "" ||
				(proxy.Scheme != "http" && proxy.Scheme != "https") {
				return app.Wrap(app.ConfigError, "Proxy URLs must be valid HTTP URLs without embedded credentials.", nil)
			}
		}
		if instance.ActiveAccount != "" {
			if _, ok := instance.Accounts[instance.ActiveAccount]; !ok || instance.LoggedOut[instance.ActiveAccount] {
				return app.Wrap(app.ConfigError, "The active account is missing or logged out.", nil)
			}
		}
	}
	if config.DefaultInstance != "" {
		if _, ok := config.Instances[config.DefaultInstance]; !ok {
			return app.Wrap(app.ConfigError, "The default instance does not exist.", nil)
		}
	}
	for _, binding := range config.Repositories {
		if _, ok := config.Instances[binding.Instance]; !ok || !filepath.IsAbs(binding.Path) {
			return app.Wrap(app.ConfigError, "Repository bindings require an absolute path and a registered instance.", nil)
		}
	}
	return nil
}
