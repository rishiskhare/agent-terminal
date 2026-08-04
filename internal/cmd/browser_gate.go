package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

func NewCmdBrowserGate() *cobra.Command {
	var realBinary string
	var healOnly bool

	cmd := &cobra.Command{
		Use:    "browser-gate",
		Short:  "Gate agent-browser to live Chrome only (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if realBinary == "" {
				return fmt.Errorf("browser-gate: --real is required")
			}
			if healOnly {
				return healStaleAgentBrowserSessions(realBinary)
			}
			return runBrowserGate(realBinary, args)
		},
	}
	cmd.Flags().StringVar(&realBinary, "real", "", "Path to the real agent-browser binary")
	cmd.Flags().BoolVar(&healOnly, "heal-only", false, "Clear stale sessions if CDP fingerprint changed, then exit")
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func runBrowserGate(realBinary string, args []string) error {
	if isBrowserGatePassthrough(args) {
		return execRealAgentBrowser(realBinary, args, nil)
	}

	ep, err := prepareLiveChromeAttach(realBinary)
	if err != nil {
		fmt.Fprint(os.Stderr, liveChromeAttachMessage())
		if err.Error() != "" {
			fmt.Fprintf(os.Stderr, "\nDetail: %v\n", err)
		}
		os.Exit(1)
	}

	gated := rewriteAgentBrowserArgs(args, ep.WSURL, os.Getenv("AGENT_BROWSER_SESSION"))
	return execRealAgentBrowser(realBinary, gated, nil)
}

func prepareLiveChromeAttach(realBinary string) (*CDPEndpoint, error) {
	// Codex's default sandbox blocks localhost TCP. Soft-probe always fails and
	// must not run close --all (that wipes concurrent agents' browser sessions).
	if os.Getenv("CODEX_SANDBOX_NETWORK_DISABLED") == "1" {
		return nil, fmt.Errorf("CODEX_SANDBOX_NETWORK_DISABLED=1")
	}

	ep, err := softProbeLiveChrome()
	if err != nil {
		// Port file missing / TCP dead: clear stale daemon once, soft-probe again.
		_ = resetAgentBrowserSessions(realBinary)
		ep, err = softProbeLiveChrome()
		if err != nil {
			return nil, err
		}
	} else if needsSessionHeal(ep.Fingerprint) {
		// Endpoint UUID/port changed (Chrome restarted). Clear old daemon only.
		_ = resetAgentBrowserSessions(realBinary)
	}

	if err := saveCDPFingerprint(ep.Fingerprint); err != nil {
		fmt.Fprintf(os.Stderr, "agent-terminal: warning: could not cache CDP fingerprint: %v\n", err)
	}
	return ep, nil
}

func healStaleAgentBrowserSessions(realBinary string) error {
	ep, err := discoverLiveChrome()
	if err != nil {
		// No live endpoint — clear any cold daemon so the next gate call starts clean.
		return resetAgentBrowserSessions(realBinary)
	}
	tcpOK := endpointTCPReachable(ep, cdpTCPTimeout) == nil
	if !tcpOK {
		_ = resetAgentBrowserSessions(realBinary)
		return nil
	}
	if needsSessionHeal(ep.Fingerprint) {
		if err := resetAgentBrowserSessions(realBinary); err != nil {
			return err
		}
	}
	// Cache fingerprint from soft probe only — never WS-verify here (Allow churn).
	_ = saveCDPFingerprint(ep.Fingerprint)
	return nil
}

// isBrowserGatePassthrough reports commands that must not require live Chrome.
func isBrowserGatePassthrough(args []string) bool {
	if len(args) == 0 {
		return true
	}
	for _, a := range args {
		switch a {
		case "-h", "--help", "-v", "--version":
			return true
		}
	}
	cmd, _ := firstAgentBrowserCommand(args)
	switch cmd {
	case "", "skills", "close", "help", "version":
		return true
	default:
		return false
	}
}

func firstAgentBrowserCommand(args []string) (cmd string, rest []string) {
	idx := firstAgentBrowserCommandIndex(args)
	if idx < 0 {
		return "", nil
	}
	return args[idx], args[idx+1:]
}

func firstAgentBrowserCommandIndex(args []string) int {
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			i++
			if i < len(args) {
				return i
			}
			return -1
		}
		if strings.HasPrefix(a, "-") {
			name, hasEq := strings.CutPrefix(a, "--")
			if !hasEq {
				name, _ = strings.CutPrefix(a, "-")
			} else if strings.Contains(name, "=") {
				i++
				continue
			}
			if strings.Contains(name, "=") {
				i++
				continue
			}
			switch name {
			case "session", "profile", "cdp", "executable-path",
				"args", "user-agent", "proxy", "proxy-bypass", "headers", "config",
				"provider", "p", "name", "n", "color-scheme", "download-path",
				"extension", "viewport", "device":
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i += 2
					continue
				}
			}
			i++
			continue
		}
		return i
	}
	return -1
}

// rewriteAgentBrowserArgs forces --cdp to the live endpoint, optionally forces
// --session from AGENT_BROWSER_SESSION, strips conflicting flags, and rewrites
// open/goto/navigate to tab new so pages open as tabs in the existing Chrome
// window instead of a separate OS window.
func rewriteAgentBrowserArgs(args []string, wsURL, session string) []string {
	forceSession := strings.TrimSpace(session) != ""
	stripped := make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--auto-connect" || a == "-auto-connect":
			i++
		case a == "--cdp":
			i++
			if i < len(args) && !strings.HasPrefix(args[i], "-") {
				i++
			}
		case strings.HasPrefix(a, "--cdp="):
			i++
		case forceSession && a == "--session":
			i++
			if i < len(args) && !strings.HasPrefix(args[i], "-") {
				i++
			}
		case forceSession && strings.HasPrefix(a, "--session="):
			i++
		case a == "--":
			stripped = append(stripped, args[i:]...)
			i = len(args)
		default:
			stripped = append(stripped, a)
			i++
		}
	}

	rewritten := rewriteOpenToTabNew(stripped)
	out := make([]string, 0, len(rewritten)+4)
	out = append(out, "--cdp", wsURL)
	if forceSession {
		out = append(out, "--session", strings.TrimSpace(session))
	}
	out = append(out, rewritten...)
	return out
}

// rewriteOpenToTabNew maps open|goto|navigate to "tab new" (preserving leading
// flags) so CDP attach opens a tab in the existing window rather than a new OS window.
func rewriteOpenToTabNew(args []string) []string {
	idx := firstAgentBrowserCommandIndex(args)
	if idx < 0 {
		return args
	}
	cmd := args[idx]
	switch cmd {
	case "open", "goto", "navigate":
		prefix := args[:idx]
		rest := args[idx+1:]
		url := "about:blank"
		outRest := rest
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			url = rest[0]
			outRest = rest[1:]
		}
		out := make([]string, 0, len(prefix)+2+len(outRest))
		out = append(out, prefix...)
		out = append(out, "tab", "new", url)
		out = append(out, outRest...)
		return out
	default:
		return args
	}
}

func execRealAgentBrowser(realBinary string, args []string, extraEnv map[string]string) error {
	env := os.Environ()
	// --cdp and AGENT_BROWSER_AUTO_CONNECT are mutually exclusive in agent-browser.
	if argsHaveCDP(args) {
		env = scrubEnvVar(env, "AGENT_BROWSER_AUTO_CONNECT")
	}
	for k, v := range extraEnv {
		env = scrubEnvVar(env, k)
		env = append(env, k+"="+v)
	}

	cmd := exec.Command(realBinary, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env

	err := cmd.Run()
	if err == nil {
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			os.Exit(status.ExitStatus())
		}
		os.Exit(1)
	}
	return err
}

func argsHaveCDP(args []string) bool {
	for _, a := range args {
		if a == "--cdp" || strings.HasPrefix(a, "--cdp=") {
			return true
		}
	}
	return false
}

func scrubEnvVar(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}
