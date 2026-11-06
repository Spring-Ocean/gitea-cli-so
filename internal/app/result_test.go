package app

import (
	"context"
	"testing"
)

// TestResultOutcomes 保护批量状态和退出码的共同契约。
// skipped 可成功，但未知、部分完成和取消不能被误聚合为成功。
func TestResultOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		items  []Item
		err    error
		status Status
		code   int
	}{
		{"empty", nil, nil, Success, 0},
		{"skipped", []Item{{Status: Skipped}}, nil, Success, 0},
		{"partial", []Item{{Status: Success}, {Status: Unknown}}, nil, Partial, 3},
		{"unknown", []Item{{Status: Unknown}}, nil, Unknown, 4},
		{"cancel", []Item{{Status: Success}, {Status: NotExecuted}}, context.Canceled, Canceled, 130},
		{"arguments", nil, Wrap(InvalidArgument, "Invalid input.", nil), Failed, 2},
		{"permission", nil, Wrap(PermissionDenied, "Access denied.", nil), Failed, 1},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			result := NewResult(Context{}, item.items, item.err)
			if result.Status != item.status || result.ExitCode() != item.code || result.Items == nil {
				t.Fatalf("unexpected result: status=%s exit=%d", result.Status, result.ExitCode())
			}
		})
	}
}
