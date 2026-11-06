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
	if got, want := out.String(), "sotea v1.2.3\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestHelpUsesSoteaCommandName(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCommand("dev")
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sotea [command]") {
		t.Fatalf("help does not identify the sotea command: %q", out.String())
	}
}

func TestUnknownCommandReturnsError(t *testing.T) {
	cmd := NewRootCommand("dev")
	cmd.SetArgs([]string{"not-a-command"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}
