// Package output 统一业务文本、JSON 和诊断的脱敏边界。
// 原始 Git、API 文件流及 MCP 协议不经过业务渲染器。
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Redactor 保存当前身份需要遮蔽的具体密钥值。
// 创建后不可变，可并发使用；不得将该对象本身序列化到业务结果。
type Redactor struct {
	secrets []string
}

// NewRedactor 注册需要额外保护的密钥，通用敏感字段会始终遮蔽。
// 空值被忽略，较长值优先替换以避免前缀覆盖留下密钥尾部。
func NewRedactor(secrets ...string) *Redactor {
	values := []string{}
	for _, value := range secrets {
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		return len(values[i]) > len(values[j])
	})
	return &Redactor{secrets: values}
}

// Text 遮蔽注册值、URL 内凭证、认证头及常见 token 查询参数。
// 返回文本仍保留业务数据，终端控制字符由文本渲染路径另行清理。
func (r *Redactor) Text(text string) string {
	if r != nil {
		for _, secret := range r.secrets {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	text = urlCredential.ReplaceAllString(text, "${1}[REDACTED]@")
	text = queryCredential.ReplaceAllString(text, "${1}[REDACTED]")
	return headerCredential.ReplaceAllString(text, "${1}: [REDACTED]")
}

var (
	urlCredential    = regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`)
	queryCredential  = regexp.MustCompile(`(?i)([?&](?:access_token|token|refresh_token|client_secret|password)=)[^&\s]+`)
	headerCredential = regexp.MustCompile(`(?im)(authorization|proxy-authorization|cookie|set-cookie|password|access_token|refresh_token|client_secret)\s*[:=]\s*[^\r\n]+`)
)

// Renderer 将业务结果与诊断写入独立流。
// Out 用于最终结果，Diagnostic 用于错误；所有业务数据都先经过 Redactor。
type Renderer struct {
	Out        io.Writer
	Diagnostic io.Writer
	Redactor   *Redactor
}

// JSON 写出单个合法 JSON envelope，并递归遮蔽敏感字段。
// 不写颜色、帮助或进度；编码或输出失败交回调用方，不能伪装为业务成功。
func (r Renderer) JSON(result app.Result) error {
	result.Context.Instance = r.Redactor.Text(result.Context.Instance)
	result.Context.Repository = r.Redactor.Text(result.Context.Repository)
	result.Context.Account = r.Redactor.Text(result.Context.Account)
	result.Error = r.safeError(result.Error)
	items := make([]app.Item, len(result.Items))
	for i, item := range result.Items {
		item.Target = r.Redactor.Text(item.Target)
		item.Data = r.redactValue(item.Data)
		item.Error = r.safeError(item.Error)
		items[i] = item
	}
	result.Items = items
	if err := json.NewEncoder(r.Out).Encode(result); err != nil {
		return app.Wrap(app.IOError, "Unable to write the result.", err)
	}
	return nil
}

// safeError 复制错误并只遮蔽可变的消息，保留稳定错误类别。
// 避免短密钥恰好出现在状态或错误代码中时破坏机器契约。
func (r Renderer) safeError(failure *app.Error) *app.Error {
	if failure == nil {
		return nil
	}
	safe := *failure
	safe.Message = r.Redactor.Text(safe.Message)
	return &safe
}

// Text 写出基础可读结果，每个目标保留实际状态。
// 此阶段不渲染动态界面；字段数据经过脱敏和控制字符清理，完整样式在 P5 实现。
func (r Renderer) Text(result app.Result) error {
	for _, item := range result.Items {
		line := fmt.Sprintf("%s: %s", item.Target, item.Status)
		if item.Data != nil {
			contents, err := json.Marshal(r.redactValue(item.Data))
			if err != nil {
				return app.Wrap(app.IOError, "Unable to encode result data.", err)
			}
			line += " " + string(contents)
		}
		if _, err := fmt.Fprintln(r.Out, clean(r.Redactor.Text(line))); err != nil {
			return app.Wrap(app.IOError, "Unable to write the result.", err)
		}
		if item.Error != nil {
			if err := r.Error(item.Error); err != nil {
				return err
			}
		}
	}
	if result.Error != nil {
		return r.Error(result.Error)
	}
	return nil
}

// Error 输出带稳定代码的英文安全摘要，不输出底层 Cause。
// 目标流由调用方提供，错误不会混入 JSON 或原始 API 响应的 stdout。
func (r Renderer) Error(err error) error {
	failure := app.Failure(err)
	if failure == nil {
		return nil
	}
	_, writeErr := fmt.Fprintf(r.Diagnostic, "%s: %s\n", failure.Code, clean(r.Redactor.Text(failure.Message)))
	if writeErr != nil {
		return app.Wrap(app.IOError, "Unable to write diagnostics.", writeErr)
	}
	return nil
}

// redactValue 将类型化数据转换成可递归处理的 JSON 表示。
// 无法编码时保留原对象，让最终编码步骤明确返回错误。
func (r Renderer) redactValue(value any) any {
	contents, err := json.Marshal(value)
	if err != nil {
		return value
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return value
	}
	return r.redact(decoded)
}

// redact 递归遮蔽键名表达的认证材料及字符串内的具体密钥。
// 数字保留 json.Number，避免大整数在通用输出中丢失精度。
func (r Renderer) redact(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, field := range value {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			switch normalized {
			case "authorization", "proxy_authorization", "cookie", "set_cookie", "password", "passwd", "token", "access_token", "refresh_token", "secret", "client_secret":
				value[key] = "[REDACTED]"
			default:
				value[key] = r.redact(field)
			}
		}
		return value
	case []any:
		for i := range value {
			value[i] = r.redact(value[i])
		}
		return value
	case string:
		return r.Redactor.Text(value)
	default:
		return value
	}
}

// clean 将远端控制字符转换为替代字符，保留正常换行和制表。
// 文本不会执行 ANSI／OSC 控制序列，JSON 路径则交给编码器正确转义。
func clean(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return '\uFFFD'
		}
		return r
	}, text)
}
