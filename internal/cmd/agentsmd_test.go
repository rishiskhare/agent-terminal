package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceManagedBlock(t *testing.T) {
	old := "before\n" + agentsMDManagedStart + "\nold\n" + agentsMDManagedEnd + "\nafter\n"
	neu := agentsMDManagedStart + "\nnew\n" + agentsMDManagedEnd + "\n"
	got, ok := replaceManagedBlock(old, neu)
	if !ok {
		t.Fatal("expected ok")
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("lost surrounding content: %q", got)
	}
	if !strings.Contains(got, "new") || strings.Contains(got, "old\n") {
		t.Fatalf("block not replaced: %q", got)
	}
}

func TestClaudeImportsAgentsMD(t *testing.T) {
	if !claudeImportsAgentsMD("@AGENTS.md\n") {
		t.Fatal("expected import")
	}
	if claudeImportsAgentsMD("see `@AGENTS.md` in docs\n") {
		t.Fatal("code span should not count")
	}
	if claudeImportsAgentsMD("```\n@AGENTS.md\n```\n") {
		t.Fatal("fenced block should not count")
	}
}

func TestHasManagedBlock(t *testing.T) {
	body, err := managedAgentsMDBody()
	if err != nil {
		t.Fatal(err)
	}
	if !hasManagedBlock(body) {
		t.Fatal("managed body should have markers")
	}
	shim, err := managedClaudeMDShim()
	if err != nil {
		t.Fatal(err)
	}
	if !hasManagedBlock(shim) || !claudeImportsAgentsMD(shim) {
		t.Fatal("CLAUDE.md shim should import AGENTS.md")
	}
	if hasManagedBlock("# hello\n") {
		t.Fatal("plain file should not")
	}
}

func TestManagedAgentsMDRouting(t *testing.T) {
	body, err := managedAgentsMDBody()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"side-panel PTY",
		"Web Search",
		"search for …",
		"AGENT_TERMINAL_BROWSER",
		"cannot attach to your live Chrome",
		"Auto-launch failed",
		"Do not open Chrome",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("managed AGENTS.md missing routing phrase %q", want)
		}
	}
	if strings.Contains(strings.ToLower(body), "banana") {
		t.Fatal("managed AGENTS.md must not contain joke food examples")
	}
}

func TestUpsertHomeAgentsMD(t *testing.T) {
	body, err := managedAgentsMDBody()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	c, err := upsertHomeAgentsMD(path, body)
	if err != nil || c.Status != "ok" {
		t.Fatalf("create: %+v %v", c, err)
	}
	c, err = upsertHomeAgentsMD(path, body)
	if err != nil || c.Status != "ok" || !strings.Contains(c.Detail, "up to date") {
		t.Fatalf("idempotent: %+v %v", c, err)
	}

	userOwned := "# my rules\n"
	if err := os.WriteFile(path, []byte(userOwned), 0644); err != nil {
		t.Fatal(err)
	}
	c, err = upsertHomeAgentsMD(path, body)
	if err != nil || c.Status != "warn" {
		t.Fatalf("should not clobber user file: %+v %v", c, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != userOwned {
		t.Fatalf("user file changed: %q", got)
	}
}
