package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCommand("v1.2.3")
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "gitea-cli v1.2.3\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestUnknownCommandReturnsError(t *testing.T) {
	cmd := NewRootCommand("dev")
	cmd.SetArgs([]string{"not-a-command"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}
