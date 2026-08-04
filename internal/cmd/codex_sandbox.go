package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	codexShimMarker      = "# agent-terminal-codex-shim:1"
	codexNetworkOverride = "sandbox_workspace_write.network_access=true"
)

func codexShimPath() string {
	return filepath.Join(shimDir, "codex")
}

func codexRealSidecarPath() string {
	return filepath.Join(shimDir, "codex.real")
}

// codexSandboxNetworkEnabled reports whether Agent Terminal should wrap Codex
// with -c sandbox_workspace_write.network_access=true. Default true when unset.
func codexSandboxNetworkEnabled() bool {
	if !k.Exists("codexSandboxNetwork") {
		return true
	}
	return k.Bool("codexSandboxNetwork")
}

func foundAgent(found []string, name string) bool {
	for _, a := range found {
		if a == name {
			return true
		}
	}
	return false
}

// resolveRealCodex returns the real Codex binary without EvalSymlinks (Homebrew
// Caskroom version pins break on upgrade). Never returns a path inside shimDir.
func resolveRealCodex() (string, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", err
	}
	if filepath.Dir(path) == shimDir {
		if sidecar := loadCodexRealPath(); sidecar != "" {
			if st, err := os.Stat(sidecar); err == nil && !st.IsDir() {
				return sidecar, nil
			}
		}
		return "", fmt.Errorf("codex on PATH is the Agent Terminal shim and no usable codex.real sidecar exists")
	}
	return path, nil
}

func loadCodexRealPath() string {
	data, err := os.ReadFile(codexRealSidecarPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func renderCodexShim(realCodex string) string {
	return fmt.Sprintf(`#!/bin/sh
%s
# Grants Codex tool sandbox network for live-Chrome CDP when the session is
# already workspace-write (trusted project). Does not change sandbox_mode and
# does not modify ~/.codex/config.toml.
target=%s
if [ -n "$AT_CODEX_SHIM" ]; then
  exec "$target" "$@"
fi
export AT_CODEX_SHIM=1
if [ ! -x "$target" ]; then
  echo "agent-terminal: Codex binary missing at $target — run: agent-terminal doctor --fix" >&2
  exit 127
fi
exec "$target" -c %s "$@"
`, codexShimMarker, shellQuote(realCodex), shellQuote(codexNetworkOverride))
}

func writeCodexShim(realCodex string) error {
	if err := os.MkdirAll(shimDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(codexRealSidecarPath(), []byte(realCodex+"\n"), 0644); err != nil {
		return err
	}
	script := renderCodexShim(realCodex)
	target := codexShimPath()
	if existing, err := os.ReadFile(target); err == nil && string(existing) == script {
		return os.Chmod(target, 0755)
	}
	return os.WriteFile(target, []byte(script), 0755)
}

func removeCodexShim() error {
	_ = os.Remove(codexShimPath())
	_ = os.Remove(codexRealSidecarPath())
	return nil
}

func codexShimInstalled() bool {
	data, err := os.ReadFile(codexShimPath())
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, codexShimMarker) && strings.Contains(s, codexNetworkOverride)
}

// applyCodexSandboxDefaults installs or removes the Codex network shim and
// points apps/codex at the right binary. Never mutates ~/.codex/.
// On Windows the shim is not installed; the normal apps/codex launcher is still written.
func applyCodexSandboxDefaults(foundAgents []string) error {
	if !foundAgent(foundAgents, "codex") {
		return nil
	}
	if runtime.GOOS == "windows" {
		return writeAgentApp("codex")
	}

	if !codexSandboxNetworkEnabled() {
		if err := removeCodexShim(); err != nil {
			return err
		}
		real, err := resolveRealCodex()
		if err != nil {
			return nil // codex gone from PATH after shim removal — ok
		}
		return writeAgentAppTo("codex", real)
	}

	real, err := resolveRealCodex()
	if err != nil {
		return fmt.Errorf("resolve codex: %w", err)
	}
	if err := writeCodexShim(real); err != nil {
		return err
	}
	return writeAgentAppTo("codex", codexShimPath())
}

func codexHomeConfigPath() string {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "config.toml")
	}
	return filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")
}

// codexConfigUsesPermissionsSystem reports whether ~/.codex/config.toml (or
// $CODEX_HOME) opts into the newer permissions profiles that ignore the legacy
// sandbox_workspace_write.network_access key used by the Agent Terminal shim.
func codexConfigUsesPermissionsSystem(configPath string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "default_permissions") {
			return true
		}
		if trimmed == "[permissions]" || strings.HasPrefix(trimmed, "[permissions.") {
			return true
		}
	}
	return false
}

func checkCodexSandboxNetwork(foundAgents []string) DoctorCheck {
	c := DoctorCheck{
		ID:    "codex.sandboxNetwork",
		Label: "Codex sandbox network",
	}
	if !foundAgent(foundAgents, "codex") {
		c.Status = "info"
		c.Detail = "codex not on PATH"
		return c
	}
	if runtime.GOOS == "windows" {
		c.Status = "info"
		c.Detail = "Codex network shim is not installed on Windows; apps/codex launcher is still written"
		return c
	}
	if !codexSandboxNetworkEnabled() {
		c.Status = "warn"
		c.Detail = "Disabled via codexSandboxNetwork=false — live Chrome attach in Codex will fail"
		c.FixHint = "Set \"codexSandboxNetwork\": true (or remove the key), run doctor --fix, then close Codex and open a new tab from the launcher"
		return c
	}
	if codexConfigUsesPermissionsSystem(codexHomeConfigPath()) {
		c.Status = "warn"
		c.Detail = "Codex config uses default_permissions / [permissions], which ignores sandbox_workspace_write.network_access — the Agent Terminal grant will not apply"
		c.FixHint = "Remove default_permissions and [permissions] from ~/.codex/config.toml (or $CODEX_HOME/config.toml), or expect live-Chrome attach to fail under Codex"
		return c
	}
	if codexShimInstalled() {
		c.Status = "ok"
		c.Detail = "Shim installed (sandbox network for AT PTYs). Opt out: codexSandboxNetwork=false"
		return c
	}
	c.Status = "warn"
	c.Detail = "Codex network shim missing — live Chrome attach will fail under Codex's default sandbox"
	c.FixHint = "Run: agent-terminal doctor --fix"
	return c
}
