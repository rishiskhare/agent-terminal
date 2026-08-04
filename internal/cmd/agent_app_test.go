package cmd

import (
	"strings"
	"testing"
)

func TestRenderAgentAppScript(t *testing.T) {
	got := renderAgentAppScript("/opt/bin/claude", "/bin/zsh", []string{"-l"})
	if !agentAppScriptCurrent(got) {
		t.Fatal("missing version marker")
	}
	if !strings.Contains(got, "trap '' INT") {
		t.Fatal("missing INT trap")
	}
	if !strings.Contains(got, "'/opt/bin/claude' \"$@\"") {
		t.Fatalf("agent line: %q", got)
	}
	if strings.Contains(got, "exec '/opt/bin/claude'") {
		t.Fatal("must not exec the agent")
	}
	if !strings.Contains(got, "exec '/bin/zsh' '-l'") {
		t.Fatalf("shell exec: %q", got)
	}
}

func TestExtractQuotedCommand(t *testing.T) {
	old := "#!/bin/sh\nexec '/Users/me/.local/bin/hermes' \"$@\"\n"
	got := extractQuotedCommand(old)
	if got != "/Users/me/.local/bin/hermes" {
		t.Fatalf("got %q", got)
	}
}
