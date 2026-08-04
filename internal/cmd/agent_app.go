package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const agentAppMarker = "# agent-terminal-app:2"

// configuredShell returns the shell used for mode:shell and post-agent exec.
func configuredShell() (command string, args []string) {
	command = k.String("command")
	if command == "" {
		command = getDefaultShell()
	}
	args = k.Strings("args")
	return command, args
}

// renderAgentAppScript builds a launcher that runs the agent, then execs the
// configured shell. trap '' INT keeps Ctrl-C from killing the wrapper before exec.
func renderAgentAppScript(agentPath, shell string, shellArgs []string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(agentAppMarker + "\n")
	b.WriteString("trap '' INT\n")
	b.WriteString(shellQuote(agentPath))
	b.WriteString(" \"$@\"\n")
	b.WriteString("trap - INT\n")
	b.WriteString("exec ")
	b.WriteString(shellQuote(shell))
	for _, a := range shellArgs {
		b.WriteByte(' ')
		b.WriteString(shellQuote(a))
	}
	b.WriteByte('\n')
	return b.String()
}

func agentAppScriptCurrent(script string) bool {
	return strings.Contains(script, agentAppMarker)
}

func writeAgentApp(name string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil
	}
	return writeAgentAppTo(name, path)
}

// writeAgentAppTo writes apps/<name> pointing at an explicit binary path.
func writeAgentAppTo(name, execPath string) error {
	if execPath == "" {
		return fmt.Errorf("writeAgentAppTo %q: empty path", name)
	}
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return err
	}
	shell, args := configuredShell()
	script := renderAgentAppScript(execPath, shell, args)
	target := filepath.Join(appDir, name)
	return os.WriteFile(target, []byte(script), 0755)
}

// ensureAgentAppScript rewrites stale or mis-pointed launchers before PTY start.
func ensureAgentAppScript(entrypoint, appID string) error {
	data, err := os.ReadFile(entrypoint)
	if err != nil {
		if os.IsNotExist(err) {
			return writeAgentApp(appID)
		}
		return err
	}
	script := string(data)

	// Minimal Codex heal: doctor owns retarget on --fix; this covers upgrades
	// where apps/codex still points at Homebrew instead of the shim.
	if appID == "codex" && runtime.GOOS != "windows" && codexSandboxNetworkEnabled() {
		shim := codexShimPath()
		if _, err := os.Stat(shim); err == nil && extractQuotedCommand(script) != shim {
			return writeAgentAppTo("codex", shim)
		}
	}

	if agentAppScriptCurrent(script) {
		return nil
	}
	agentPath, lookErr := exec.LookPath(appID)
	if lookErr != nil {
		if extracted := extractQuotedCommand(script); extracted != "" {
			agentPath = extracted
		} else {
			return fmt.Errorf("heal app %q: %w", appID, lookErr)
		}
	}
	if appID == "codex" && filepath.Dir(agentPath) == shimDir {
		if real := loadCodexRealPath(); real != "" {
			agentPath = real
		}
	}
	return writeAgentAppTo(appID, agentPath)
}

// extractQuotedCommand pulls the first single-quoted path from an old launcher.
func extractQuotedCommand(script string) string {
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "exec ")
		if !strings.HasPrefix(line, "'") {
			continue
		}
		end := strings.Index(line[1:], "'")
		if end < 0 {
			continue
		}
		return line[1 : 1+end]
	}
	return ""
}
