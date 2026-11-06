package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Service 组合配置、系统密钥库和 Git，供 CLI 与 MCP 共享认证规则。
// Getenv 可注入隔离环境；每次获取身份重新读取配置的责任由调用入口承担。
type Service struct {
	HTTPOptions gitea.Options
	Store       config.Store
	Vault       Vault
	Git         git.Runner
	Getenv      func(string) string
	OnSecret    func(string)
}

// Material 持有一次调用需要的认证材料，secret 不可序列化。
// Source 与 Type 可用于诊断，helper 用户名不得充当 API login。
type Material struct {
	Source    string
	Type      string
	Username  string
	secret    string
	reference string
	helper    git.Credential
	directory string
}

// Authenticate 只在固定实例客户端内注入已选择的认证类型。
// 不尝试其他认证方案，不允许 token 或 password 进入 URL。
func (m *Material) Authenticate(request *http.Request) error {
	if m == nil {
		return nil
	}
	if m.Type == "basic" {
		request.SetBasicAuth(m.Username, m.secret)
	} else {
		request.Header.Set("Authorization", "token "+m.secret)
	}
	return nil
}

// LoginOptions 明确指定来源及必要密钥，禁止猜测 helper 密码类型。
// Secret 仅用于显式 token 登录；SaveName 是用户选择的实例别名，不自动覆盖其他实例。
type LoginOptions struct {
	Source   string
	AuthType string
	Secret   string
	SaveName string
}

// Status 是不包含密钥的身份诊断结果。
// EnvironmentPresent 表示外部变量仍存在，退出不能清除父进程环境。
type Status struct {
	Instance           string `json:"instance"`
	URL                string `json:"url"`
	Account            string `json:"account,omitempty"`
	Source             string `json:"source"`
	VerifiedAt         string `json:"verified_at,omitempty"`
	Invalid            bool   `json:"invalid"`
	LoggedOut          bool   `json:"logged_out"`
	EnvironmentPresent bool   `json:"environment_present"`
}

// environment 读取单个明确变量，不遍历系统或客户端配置。
// 测试可提供完全隔离的环境映射，生产默认读取当前进程环境。
func (s Service) environment(name string) string {
	if s.Getenv != nil {
		return s.Getenv(name)
	}
	return os.Getenv(name)
}

// client 为目标实例创建有界且保持 TLS 校验的认证客户端。
// 认证只通过回调注入，调用者负责 Close 并关闭所有响应体。
func (s Service) client(t target.Target, m *Material) (*gitea.Client, error) {
	options := s.HTTPOptions
	options.BaseURL = t.Instance.URL
	options.CAFile = t.Instance.CAPath
	options.ProxyURL = t.Instance.Proxy
	options.Authenticate = m.Authenticate
	return gitea.New(options)
}

// Credentials 按环境、自有引用、已映射 HTTPS helper 顺序选择一次来源。
// 未确认地址一律拒绝，退出抑制阻止自动 helper 与账号接替；无凭证时可匿名读取。
func (s Service) Credentials(ctx context.Context, t target.Target, anonymous bool) (*Material, error) {
	if !t.Registered {
		return nil, app.Wrap(app.AuthRequired, "Confirm the instance before reading or sending credentials. Run sotea auth login.", nil)
	}
	if token := s.environment("SOTEA_TOKEN"); token != "" {
		if limit := s.environment("SOTEA_TOKEN_INSTANCE"); limit != "" {
			canonical, err := target.NormalizeURL(limit)
			if limit != t.Name && (err != nil || canonical != t.Instance.URL) {
				return nil, app.Wrap(app.IdentityMismatch, "SOTEA_TOKEN_INSTANCE does not match the selected instance.", nil)
			}
		}
		s.observe(token)
		return &Material{
			Source: "environment",
			Type:   "token",
			secret: token,
		}, nil
	}
	saved, exists := t.Instance.Accounts[t.Account]
	if t.Instance.LoggedOut[t.Account] || (t.Account == "" && len(t.Instance.LoggedOut) > 0) {
		if anonymous {
			return nil, nil
		}
		return nil, app.Wrap(app.AuthRequired, "This identity is logged out. Run sotea auth login explicitly.", nil)
	}
	if exists {
		if saved.InstanceURL != t.Instance.URL {
			return nil, app.Wrap(app.AuthRequired, "The saved identity belongs to a different instance root. Sign in again.", nil)
		}
		if saved.Invalid {
			return nil, app.Wrap(app.AuthRequired, "The saved credential is marked invalid. Run sotea auth refresh.", nil)
		}
		switch saved.Source {
		case "token":
			secret, err := s.Vault.Get(ctx, saved.CredentialRef)
			if err != nil {
				return nil, err
			}
			s.observe(secret)
			return &Material{
				Source:    "token",
				Type:      "token",
				secret:    secret,
				reference: saved.CredentialRef,
			}, nil
		case "git":
			return s.helper(ctx, saved.HelperDirectory, git.Credential{
				Protocol: saved.HelperProtocol,
				Host:     saved.HelperHost,
				Path:     saved.HelperPath,
				Username: saved.CredentialUsername,
			}, saved.AuthType)
		case "environment":
			return nil, app.Wrap(app.AuthRequired, "This account uses an external environment token, but SOTEA_TOKEN is absent.", nil)
		default:
			return nil, app.Wrap(app.ConfigError, "Unsupported saved credential source.", nil)
		}
	}
	if t.Account != "" {
		return nil, app.Wrap(app.AuthRequired, "The selected account has no saved credentials. Sign in explicitly.", nil)
	}
	if t.Credential.Protocol != "" && len(t.Instance.LoggedOut) == 0 {
		material, err := s.helper(ctx, t.Workdir, t.Credential, "basic")
		if err == nil || !anonymous || app.Failure(err).Code != app.AuthRequired {
			return material, err
		}
	}
	if anonymous {
		return nil, nil
	}
	return nil, app.Wrap(app.AuthRequired, "No credentials are available. Run sotea auth login.", nil)
}

// helper 在原始仓库上下文调用 Git，并保留实际返回的匹配范围。
// 查询不允许提示，认证类型必须是 basic 或明确选择的 token，不轮流尝试。
func (s Service) helper(ctx context.Context, directory string, query git.Credential, kind string) (*Material, error) {
	if kind == "" {
		kind = "basic"
	}
	if kind != "basic" && kind != "token" {
		return nil, app.Wrap(app.InvalidArgument, "Git credential authentication must be basic or token.", nil)
	}
	credential, err := s.Git.Credential(ctx, directory, "fill", query, false)
	if err != nil {
		return nil, err
	}
	if credential.Protocol != query.Protocol || !strings.EqualFold(credential.Host, query.Host) || credential.Username == "" {
		return nil, app.Wrap(app.AuthRequired, "Git returned an incompatible credential context.", nil)
	}
	if query.Path != "" && credential.Path != "" && credential.Path != query.Path {
		return nil, app.Wrap(app.AuthRequired, "Git returned credentials for a different repository path.", nil)
	}
	s.observe(credential.Password)
	return &Material{
		Source:    "git",
		Type:      kind,
		Username:  credential.Username,
		secret:    credential.Password,
		helper:    credential,
		directory: directory,
	}, nil
}

// Verify 调用 GET /user 核对实际身份，明确账号不一致时停止后续业务。
// 401 标记已有记录失效；403、网络与 TLS 错误不删除或 reject 凭证。
func (s Service) Verify(ctx context.Context, t target.Target, m *Material) (string, error) {
	if m == nil {
		return "", app.Wrap(app.AuthRequired, "An authenticated identity is required.", nil)
	}
	verification := s
	verification.HTTPOptions = gitea.Options{Timeout: 30 * time.Second, MaxResponseBytes: 1 << 20}
	client, err := verification.client(t, m)
	if err != nil {
		return "", err
	}
	defer client.Close()
	response, err := client.Do(ctx, gitea.Request{Path: "/user"})
	if err != nil {
		return "", err
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := gitea.Decode(response, &user); err != nil {
		if app.Failure(err).HTTPStatus == 401 && s.matchesSaved(t, m) {
			if recordErr := s.markInvalid(ctx, t); recordErr != nil {
				return "", app.Wrap(app.AuthRequired, "Authentication failed and its invalid state could not be saved. Sign in again.", recordErr)
			}
		}
		return "", err
	}
	if user.Login == "" || len(user.Login) > 255 || strings.ContainsAny(user.Login, "/\\") {
		return "", app.Wrap(app.ServerError, "The identity response does not contain a login.", nil)
	}
	for _, character := range user.Login {
		if unicode.IsControl(character) {
			return "", app.Wrap(app.ServerError, "The identity response contains invalid account characters.", nil)
		}
	}
	if t.Account != "" && !strings.EqualFold(t.Account, user.Login) {
		return "", app.Wrap(app.IdentityMismatch, "The credential belongs to a different API account. Select that account explicitly before continuing.", nil)
	}
	return user.Login, nil
}

// markInvalid 仅更新同实例同账号的失效标记，不更换认证来源。
// 配置并发变化时保留冲突，不覆盖其他进程的新记录。
func (s Service) markInvalid(ctx context.Context, t target.Target) error {
	snapshot, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	instance, ok := snapshot.Config.Instances[t.Name]
	if !ok || instance.URL != t.Instance.URL {
		return nil
	}
	account, ok := instance.Accounts[t.Account]
	previous := t.Instance.Accounts[t.Account]
	previous.Invalid = account.Invalid
	if !ok || account != previous {
		return nil
	}
	account.Invalid = true
	instance.Accounts[t.Account] = account
	snapshot.Config.Instances[t.Name] = instance
	_, err = s.Store.Save(ctx, snapshot, snapshot.Config)
	return err
}

// Open 验证所选凭证后创建业务客户端，允许完全无凭证的公开读取。
// 每次调用重新验证身份，不把历史登录当作永久授权；调用者负责关闭客户端。
func (s Service) Open(ctx context.Context, t target.Target, anonymous bool) (*gitea.Client, target.Target, error) {
	m, err := s.Credentials(ctx, t, anonymous)
	if err != nil {
		return nil, t, err
	}
	if m != nil {
		login, verifyErr := s.Verify(ctx, t, m)
		if verifyErr != nil {
			return nil, t, verifyErr
		}
		t.Account = login
	}
	client, err := s.client(t, m)
	return client, t, err
}

// Login 显式验证身份和目标仓库，之后才保存新引用、绑定与活动账号。
// 新 token 使用独立引用，配置保存失败会清理该引用；旧凭证不被提前覆盖。
func (s Service) Login(ctx context.Context, t target.Target, snapshot config.Snapshot, opts LoginOptions) (Status, error) {
	if !t.Registered {
		return Status{}, app.Wrap(app.AuthRequired, "Confirm the instance source before signing in.", nil)
	}
	var material *Material
	var err error
	switch opts.Source {
	case "git":
		if opts.AuthType == "" {
			opts.AuthType = t.Instance.Accounts[t.Account].AuthType
		}
		query, directory := t.Credential, t.Workdir
		if query.Protocol == "" {
			account := t.Instance.Accounts[t.Account]
			query = git.Credential{
				Protocol: account.HelperProtocol,
				Host:     account.HelperHost,
				Path:     account.HelperPath,
				Username: account.CredentialUsername,
			}
			directory = account.HelperDirectory
		}
		material, err = s.helper(ctx, directory, query, opts.AuthType)
	case "token":
		if opts.Secret == "" || len(opts.Secret) > 1024 || strings.ContainsAny(opts.Secret, "\r\n\x00") {
			err = app.Wrap(app.InvalidArgument, "Provide one token of at most 1024 bytes with --with-token or secure interactive input.", nil)
		} else {
			material = &Material{
				Source: "token",
				Type:   "token",
				secret: opts.Secret,
			}
		}
	case "environment":
		if s.environment("SOTEA_TOKEN") == "" {
			err = app.Wrap(app.AuthRequired, "SOTEA_TOKEN is not set.", nil)
		} else {
			material, err = s.Credentials(ctx, t, false)
		}
	default:
		err = app.Wrap(app.InvalidArgument, "Choose credential source git, token, or environment.", nil)
	}
	if err != nil {
		return Status{}, err
	}
	s.observe(material.secret)
	login, err := s.Verify(ctx, t, material)
	if err != nil {
		return Status{}, err
	}
	if t.Repository != "" {
		client, clientErr := s.client(t, material)
		if clientErr != nil {
			return Status{}, clientErr
		}
		var repository map[string]any
		response, requestErr := client.Do(ctx, gitea.Request{Path: repositoryPath(t.Repository)})
		if requestErr == nil {
			requestErr = gitea.Decode(response, &repository)
		}
		client.Close()
		if requestErr != nil {
			return Status{}, requestErr
		}
	}
	if opts.SaveName != "" {
		if _, exists := snapshot.Config.Instances[t.Name]; exists && opts.SaveName != t.Name {
			return Status{}, app.Wrap(app.InvalidArgument, "--name only assigns a name to a new instance; it does not rename a registered instance.", nil)
		}
		if previous, exists := snapshot.Config.Instances[opts.SaveName]; exists && previous.URL != t.Instance.URL {
			return Status{}, app.Wrap(app.ConfigConflict, "The instance name is already bound to another root URL.", nil)
		}
		t.Name = opts.SaveName
	}
	instance := t.Instance
	if instance.Accounts == nil {
		instance.Accounts = map[string]config.Account{}
	}
	if instance.LoggedOut == nil {
		instance.LoggedOut = map[string]bool{}
	}
	account := config.Account{
		InstanceURL: t.Instance.URL,
		Source:      material.Source,
		AuthType:    material.Type,
		VerifiedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	old := instance.Accounts[login]
	if material.Source == "token" {
		value := make([]byte, 24)
		if _, err := rand.Read(value); err != nil {
			return Status{}, app.Wrap(app.IOError, "Unable to generate a credential reference.", err)
		}
		account.CredentialRef = hex.EncodeToString(value)
		if err := s.Vault.Set(ctx, account.CredentialRef, material.secret); err != nil {
			return Status{}, err
		}
	} else if material.Source == "git" {
		account.CredentialUsername = material.Username
		account.HelperProtocol = material.helper.Protocol
		account.HelperHost = material.helper.Host
		account.HelperPath = material.helper.Path
		account.HelperDirectory = material.directory
	}
	instance.Accounts[login] = account
	instance.ActiveAccount = login
	delete(instance.LoggedOut, login)
	if snapshot.Config.Instances == nil {
		snapshot.Config.Instances = map[string]config.Instance{}
	}
	snapshot.Config.Instances[t.Name] = instance
	if t.Local != nil && t.LocalMatches {
		bindings := snapshot.Config.Repositories
		found := false
		for i := range bindings {
			if bindings[i].Path == t.Local.Root {
				bindings[i] = config.Binding{
					Path:       t.Local.Root,
					Remote:     t.Remote,
					Instance:   t.Name,
					Repository: t.Repository,
				}
				found = true
			}
		}
		if !found {
			bindings = append(bindings, config.Binding{
				Path:       t.Local.Root,
				Remote:     t.Remote,
				Instance:   t.Name,
				Repository: t.Repository,
			})
		}
		snapshot.Config.Repositories = bindings
	}
	if _, err := s.Store.Save(ctx, snapshot, snapshot.Config); err != nil {
		if account.CredentialRef != "" {
			if cleanupErr := s.Vault.Delete(context.WithoutCancel(ctx), account.CredentialRef); cleanupErr != nil {
				return Status{}, app.Wrap(app.PartialFailure, "Configuration was not saved and the new credential reference could not be removed.", cleanupErr)
			}
		}
		return Status{}, err
	}
	t.Account, t.Instance = login, instance
	if old.CredentialRef != "" && old.CredentialRef != account.CredentialRef {
		if err := s.Vault.Delete(ctx, old.CredentialRef); err != nil {
			return s.Status(t), app.Wrap(app.PartialFailure, "The new credential is active, but the previous stored credential could not be removed.", err)
		}
	}
	return s.Status(t), nil
}

// Status 返回本地来源与验证元信息，不访问密钥库、不启动 helper。
// 环境限定是否匹配在实际读取凭证时验证；不把环境存在误报为验证成功。
func (s Service) Status(t target.Target) Status {
	account := t.Instance.Accounts[t.Account]
	source := account.Source
	environment := s.environment("SOTEA_TOKEN") != ""
	if environment {
		source = "environment"
	}
	if source == "" {
		source = "none"
	}
	return Status{
		Instance:           t.Name,
		URL:                t.Instance.URL,
		Account:            t.Account,
		Source:             source,
		VerifiedAt:         account.VerifiedAt,
		Invalid:            account.Invalid,
		LoggedOut:          t.Instance.LoggedOut[t.Account] || (t.Account == "" && len(t.Instance.LoggedOut) > 0),
		EnvironmentPresent: environment,
	}
}

// Switch 验证一个明确保存的账号，再原子更新实例活动身份。
// 不修改 Git 作者、SSH 配置或其他账号，失败保留当前活动身份。
func (s Service) Switch(ctx context.Context, t target.Target, snapshot config.Snapshot) (Status, error) {
	if _, exists := t.Instance.Accounts[t.Account]; !exists || t.Instance.LoggedOut[t.Account] {
		return Status{}, app.Wrap(app.AuthRequired, "Select a saved account that has not been logged out.", nil)
	}
	m, err := s.Credentials(ctx, t, false)
	if err != nil {
		return Status{}, err
	}
	if _, err := s.Verify(ctx, t, m); err != nil {
		return Status{}, err
	}
	instance := t.Instance
	instance.ActiveAccount = t.Account
	snapshot.Config.Instances[t.Name] = instance
	if _, err := s.Store.Save(ctx, snapshot, snapshot.Config); err != nil {
		return Status{}, err
	}
	t.Instance = instance
	return s.Status(t), nil
}

// Logout 先持久化退出抑制，再清理自有引用并移除账号绑定。
// 清理失败保留被抑制的记录供再次执行，不撤销服务器 token 或父进程环境。
func (s Service) Logout(ctx context.Context, t target.Target, snapshot config.Snapshot) (Status, error) {
	if !t.Registered {
		return Status{}, app.Wrap(app.AuthRequired, "Select a registered instance before signing out.", nil)
	}
	if t.Account == "" {
		return Status{}, app.Wrap(app.InvalidArgument, "Specify --account for logout.", nil)
	}
	if _, exists := t.Instance.Accounts[t.Account]; !exists && !t.Instance.LoggedOut[t.Account] {
		m, err := s.Credentials(ctx, t, false)
		if err != nil {
			return Status{}, err
		}
		if _, err := s.Verify(ctx, t, m); err != nil {
			return Status{}, err
		}
	}
	instance := t.Instance
	if instance.LoggedOut == nil {
		instance.LoggedOut = map[string]bool{}
	}
	account := instance.Accounts[t.Account]
	instance.LoggedOut[t.Account] = true
	if instance.ActiveAccount == t.Account {
		instance.ActiveAccount = ""
	}
	snapshot.Config.Instances[t.Name] = instance
	for i := range snapshot.Config.Repositories {
		binding := &snapshot.Config.Repositories[i]
		if binding.Instance == t.Name && binding.Account == t.Account {
			binding.Account = ""
		}
	}
	suppressed, err := s.Store.Save(ctx, snapshot, snapshot.Config)
	if err != nil {
		return Status{}, err
	}
	t.Instance = instance
	if account.CredentialRef != "" {
		if err := s.Vault.Delete(ctx, account.CredentialRef); err != nil {
			return s.Status(t), app.Wrap(app.PartialFailure, "Automatic sign-in is suppressed, but the stored credential could not be removed. Retry logout.", err)
		}
	}
	delete(instance.Accounts, t.Account)
	suppressed.Config.Instances[t.Name] = instance
	if _, err := s.Store.Save(ctx, suppressed, suppressed.Config); err != nil {
		return s.Status(t), err
	}
	return s.Status(t), nil
}

// ForgetScope 返回已保存 Git 来源的有效匹配范围，供入口单独展示与确认。
// 空 path 表示 Git 没有按路径限定；不声称 reject 只影响当前仓库。
func (s Service) ForgetScope(t target.Target) (git.Credential, string, error) {
	account, ok := t.Instance.Accounts[t.Account]
	if !ok || account.Source != "git" {
		return git.Credential{}, "", app.Wrap(app.InvalidArgument, "--forget-git requires a saved Git credential source with a verified matching scope.", nil)
	}
	return git.Credential{
		Protocol: account.HelperProtocol,
		Host:     account.HelperHost,
		Path:     account.HelperPath,
		Username: account.CredentialUsername,
	}, account.HelperDirectory, nil
}

// ForgetGit 仅在调用入口已独立确认实际范围后执行 reject。
// 不能删除 sotea 退出抑制，也不读取或输出 password。
func (s Service) ForgetGit(ctx context.Context, query git.Credential, directory string) error {
	_, err := s.Git.Credential(ctx, directory, "reject", query, false)
	return err
}

// repositoryPath 将已校验仓库名按路径段编码。
// helper 始终使用原始 remote path，不使用本 API 路径。
func repositoryPath(repository string) string {
	parts := strings.Split(repository, "/")
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}

// observe 将当前密钥交给入口的脱敏登记，不记录或持久化原文。
// 回调可为空；MCP 与 CLI 入口使用并发安全登记器覆盖自有和 helper 来源。
func (s Service) observe(secret string) {
	if s.OnSecret != nil {
		s.OnSecret(secret)
	}
}

// matchesSaved 只把实际使用的已保存凭证对应到失效记录。
// 新 token 验证失败或环境覆盖失败不能把未使用的旧系统凭证误标为失效。
func (s Service) matchesSaved(t target.Target, m *Material) bool {
	saved, exists := t.Instance.Accounts[t.Account]
	if !exists || m == nil || saved.Source != m.Source {
		return false
	}
	switch m.Source {
	case "token":
		return m.reference != "" && m.reference == saved.CredentialRef
	case "git":
		return m.directory == saved.HelperDirectory && m.Type == saved.AuthType &&
			m.helper.Protocol == saved.HelperProtocol && m.helper.Host == saved.HelperHost &&
			m.helper.Path == saved.HelperPath && m.Username == saved.CredentialUsername
	case "environment":
		return true
	}
	return false
}
