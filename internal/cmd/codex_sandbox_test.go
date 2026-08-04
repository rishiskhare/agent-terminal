package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withTempAgentDirs(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	origConfig, origData, origApp, origShim := configDir, dataDir, appDir, shimDir
	configDir = filepath.Join(tmp, "config")
	dataDir = filepath.Join(tmp, "data")
	appDir = filepath.Join(configDir, "apps")
	shimDir = filepath.Join(dataDir, "bin")
	t.Cleanup(func() {
		configDir, dataDir, appDir, shimDir = origConfig, origData, origApp, origShim
	})
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(shimDir, 0755); err != nil {
		t.Fatal(err)
	}
}

func TestRenderCodexShim(t *testing.T) {
	got := renderCodexShim("/opt/homebrew/bin/codex")
	if !strings.Contains(got, codexShimMarker) || !strings.Contains(got, codexNetworkOverride) {
		t.Fatalf("missing marker/override: %q", got)
	}
	if strings.Contains(got, "-c sandbox_mode") || strings.Contains(got, "sandbox_mode=") {
		t.Fatal("must not force sandbox_mode")
	}
	if !strings.Contains(got, "AT_CODEX_SHIM") || !strings.Contains(got, "'/opt/homebrew/bin/codex'") {
		t.Fatalf("missing guard or path: %q", got)
	}
}

func TestApplyCodexSandboxDefaultsInstallOptOutNoMutate(t *testing.T) {
	withTempAgentDirs(t)
	codexHome := t.TempDir()
	cfg := filepath.Join(codexHome, "config.toml")
	original := "[projects.\"/tmp\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(cfg, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)

	real := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(real)+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := applyCodexSandboxDefaults([]string{"codex"}); err != nil {
		t.Fatal(err)
	}
	if !codexShimInstalled() {
		t.Fatal("expected shim")
	}
	launcher, err := os.ReadFile(filepath.Join(appDir, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	if extractQuotedCommand(string(launcher)) != codexShimPath() {
		t.Fatalf("launcher: %q", launcher)
	}

	// Opt-out path: remove shim then point launcher at real binary.
	if err := removeCodexShim(); err != nil {
		t.Fatal(err)
	}
	if err := writeAgentAppTo("codex", real); err != nil {
		t.Fatal(err)
	}
	if codexShimInstalled() {
		t.Fatal("shim should be gone")
	}
	launcher, err = os.ReadFile(filepath.Join(appDir, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	if extractQuotedCommand(string(launcher)) != real {
		t.Fatalf("opt-out launcher: %q", launcher)
	}

	after, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("CODEX_HOME mutated:\n%s", after)
	}
}

func TestResolveRealCodexRejectsShimDir(t *testing.T) {
	withTempAgentDirs(t)
	real := filepath.Join(t.TempDir(), "codex-real")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexRealSidecarPath(), []byte(real+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexShimPath(), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := resolveRealCodex()
	if err != nil {
		t.Fatal(err)
	}
	if got != real || filepath.Dir(got) == shimDir {
		t.Fatalf("got %q want %q", got, real)
	}
}

func TestEnsureAgentAppScriptPointsCodexAtShim(t *testing.T) {
	withTempAgentDirs(t)
	real := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeCodexShim(real); err != nil {
		t.Fatal(err)
	}
	entrypoint := filepath.Join(appDir, "codex")
	legacy := "#!/bin/sh\n# agent-terminal-app:1\ntrap '' INT\n'" + real + "' \"$@\"\ntrap - INT\nexec '/bin/zsh'\n"
	if err := os.WriteFile(entrypoint, []byte(legacy), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ensureAgentAppScript(entrypoint, "codex"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(entrypoint)
	if err != nil {
		t.Fatal(err)
	}
	if extractQuotedCommand(string(got)) != codexShimPath() {
		t.Fatalf("expected shim, got %q", extractQuotedCommand(string(got)))
	}
}

func TestCodexConfigUsesPermissionsSystem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cases := []struct {
		body string
		want bool
	}{
		{"[projects.\"/tmp\"]\ntrust_level = \"trusted\"\n", false},
		{"# default_permissions = \":workspace\"\n", false},
		{"default_permissions = \":workspace\"\n", true},
		{"[permissions.workspace]\nnetwork = true\n", true},
		{"[sandbox_workspace_write]\nnetwork_access = true\n", false},
	}
	for _, tc := range cases {
		if err := os.WriteFile(path, []byte(tc.body), 0644); err != nil {
			t.Fatal(err)
		}
		if got := codexConfigUsesPermissionsSystem(path); got != tc.want {
			t.Fatalf("body %q: got %v want %v", tc.body, got, tc.want)
		}
	}
}

func TestLiveChromeAttachMessageCodex(t *testing.T) {
	t.Setenv("CODEX_SANDBOX_NETWORK_DISABLED", "")
	if strings.Contains(liveChromeAttachMessage(), "CODEX_SANDBOX_NETWORK_DISABLED") {
		t.Fatal("base message must not mention Codex env")
	}
	t.Setenv("CODEX_SANDBOX_NETWORK_DISABLED", "1")
	got := liveChromeAttachMessage()
	for _, want := range []string{
		"CODEX_SANDBOX_NETWORK_DISABLED",
		"codexSandboxNetwork",
		"doctor --fix",
		"Close this Codex tab",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}
