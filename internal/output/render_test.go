package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// TestJSONRedactsSecretsAndKeepsNumbers 保护脱敏、结构完整性和大整数精度。
// 底层错误原因及敏感字段不会出现在输出，普通数据仍可机器解析。
func TestJSONRedactsSecretsAndKeepsNumbers(t *testing.T) {
	var out, diagnostic bytes.Buffer
	render := Renderer{Out: &out, Diagnostic: &diagnostic, Redactor: NewRedactor("registered-secret")}
	result := app.NewResult(app.Context{}, []app.Item{{
		Target: "sample",
		Status: app.Success,
		Data: map[string]any{
			"password": "field-secret",
			"url":      "https://user:url-secret@host.invalid/path?token=query-secret",
			"note":     "registered-secret",
			"large":    int64(9007199254740993),
		},
	}}, app.Wrap(app.IOError, "Safe error.", errors.New("cause-secret")))
	if err := render.JSON(result); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"registered-secret", "field-secret", "url-secret", "query-secret", "cause-secret"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("a secret reached JSON output")
		}
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "9007199254740993") || diagnostic.Len() != 0 {
		t.Fatal("JSON contract or integer precision was lost")
	}
	if !strings.Contains(out.String(), "https://[REDACTED]@host.invalid/path?token=[REDACTED]") ||
		render.Redactor.Text("Authorization: Bearer synthetic") != "Authorization: [REDACTED]" {
		t.Fatal("redaction lost safe URL or header context")
	}
	result.Items[0].Target = "unsafe\x1b]title\x07"
	out.Reset()
	if err := render.Text(result); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Fatal("terminal control characters reached text output")
	}
	out.Reset()
	render.Redactor = NewRedactor("a")
	failed := app.NewResult(app.Context{}, nil, app.Wrap(app.InvalidArgument, "Invalid arguments.", nil))
	if err := render.JSON(failed); err != nil {
		t.Fatal(err)
	}
	var decoded app.Result
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded.Status != app.Failed || decoded.Error.Code != app.InvalidArgument {
		t.Fatal("redaction changed structural status or error codes")
	}
}
