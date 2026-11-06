package app

import (
	"context"
	"errors"
)

// Status 表示整体或逐项结果，未知不代表可以安全重试。
// skipped 和 not_executed 仅用于逐项记录。
type Status string

// 结果状态供三种入口共同使用。
// 整体状态不得把未执行或未确认的写入误报为成功。
const (
	Success     Status = "success"
	Partial     Status = "partial"
	Failed      Status = "failed"
	Canceled    Status = "cancelled"
	Unknown     Status = "unknown"
	Skipped     Status = "skipped"
	NotExecuted Status = "not_executed"
)

// Context 是可输出的目标标识，不包含认证材料。
// Repository 使用 owner/repo，Instance 使用实例名称或已确认根地址。
type Context struct {
	Instance   string `json:"instance,omitempty"`
	Repository string `json:"repo,omitempty"`
	Account    string `json:"account,omitempty"`
}

// Item 保存单个明确目标的执行状态。
// Data 只接受业务数据，认证对象必须保留在输出边界之外。
type Item struct {
	Target string `json:"target"`
	Status Status `json:"status"`
	Data   any    `json:"data,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Page 保存一页结果及继续读取信息。
// HasMore 不表示已知整个仓库的资源总数。
type Page[T any] struct {
	Items   []T  `json:"items"`
	Page    int  `json:"page"`
	Limit   int  `json:"limit"`
	HasMore bool `json:"has_more"`
}

// Result 是 schema_version=1 的业务结果 envelope。
// 原始 API、Git 及 MCP 传输不由该类型改变原始流规则。
type Result struct {
	SchemaVersion int     `json:"schema_version"`
	Status        Status  `json:"status"`
	Context       Context `json:"context"`
	Items         []Item  `json:"items"`
	Error         *Error  `json:"error"`
}

// NewResult 汇总逐项状态并保留顶层故障。
// 取消优先；存在成功或 skipped 且仍有失败等项时为 partial。
func NewResult(target Context, items []Item, err error) Result {
	if items == nil {
		items = []Item{}
	}
	result := Result{
		SchemaVersion: 1,
		Status:        Success,
		Context:       target,
		Items:         items,
		Error:         Failure(err),
	}
	good, bad, unknown, canceled := false, err != nil, false, errors.Is(err, context.Canceled)
	for _, item := range items {
		switch item.Status {
		case Success, Skipped:
			good = true
		case Partial:
			good, bad = true, true
		case Unknown:
			bad, unknown = true, true
		case Canceled:
			bad, canceled = true, true
		default:
			bad = true
		}
	}
	if result.Error != nil {
		if result.Error.Code == PartialFailure {
			good, bad = true, true
		}
		unknown = unknown || result.Error.Code == ResultUnknown
		canceled = canceled || result.Error.Code == Cancelled
	}
	switch {
	case canceled:
		result.Status = Canceled
	case good && bad:
		result.Status = Partial
	case unknown:
		result.Status = Unknown
	case bad:
		result.Status = Failed
	}
	return result
}

// ExitCode 将整体结果映射为 CLI 状态。
// 取消为 130，部分完成为 3；读取空列表和全部 skipped 均成功。
func (r Result) ExitCode() int {
	switch r.Status {
	case Success:
		return 0
	case Canceled:
		return 130
	case Partial:
		return 3
	case Unknown:
		return 4
	default:
		if r.Error != nil {
			return ExitCode(r.Error)
		}
		if len(r.Items) == 1 && r.Items[0].Error != nil {
			return ExitCode(r.Items[0].Error)
		}
		return 1
	}
}
