package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

type DoctorCheck struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"` // ok | warn | error | info
	Detail  string `json:"detail"`
	FixHint string `json:"fixHint,omitempty"`
}

type DoctorStatus struct {
	Ok      bool           `json:"ok"`
	Level   string         `json:"level"` // ok | warn | error
	Checks  []DoctorCheck  `json:"checks"`
	Config  map[string]any `json:"config,omitempty"`
	Agents  []string       `json:"agents"`
	Message string         `json:"message"`
}

func NewCmdDoctor() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose setup and write safe defaults",
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := runDoctor(fix)
			if err != nil {
				return err
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(status); err != nil {
				return err
			}

			for _, c := range status.Checks {
				prefix := "·"
				switch c.Status {
				case "ok":
					prefix = "✓"
				case "warn":
					prefix = "!"
				case "error":
					prefix = "✗"
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%s %s — %s\n", prefix, c.Label, c.Detail)
				if c.FixHint != "" && c.Status != "ok" {
					fmt.Fprintf(cmd.ErrOrStderr(), "    %s\n", c.FixHint)
				}
			}
			fmt.Fprintln(cmd.ErrOrStderr(), status.Message)

			if status.Level == "error" {
				return fmt.Errorf("doctor found problems")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", true, "Write safe config defaults and install native messaging if missing")
	return cmd
}

func runDoctor(fix bool) (*DoctorStatus, error) {
	status := &DoctorStatus{
		Checks: []DoctorCheck{},
		Agents: []string{},
	}

	if fix {
		if err := ensureInstall(); err != nil {
			status.Checks = append(status.Checks, DoctorCheck{
				ID:      "nativeMessaging",
				Label:   "Native messaging host",
				Status:  "error",
				Detail:  err.Error(),
				FixHint: "Run: agent-terminal install",
			})
		} else {
			status.Checks = append(status.Checks, DoctorCheck{
				ID:     "nativeMessaging",
				Label:  "Native messaging host",
				Status: "ok",
				Detail: "Manifests installed for available browsers",
			})
		}
	} else {
		ok, detail := checkNativeMessaging()
		c := DoctorCheck{
			ID:     "nativeMessaging",
			Label:  "Native messaging host",
			Detail: detail,
		}
		if ok {
			c.Status = "ok"
		} else {
			c.Status = "error"
			c.FixHint = "Run: agent-terminal install"
		}
		status.Checks = append(status.Checks, c)
	}

	// Resolve real agent-browser before we install PATH shims that shadow it.
	realAgentBrowser, _ := exec.LookPath("agent-browser")
	if realAgentBrowser != "" {
		if resolved, err := filepath.EvalSymlinks(realAgentBrowser); err == nil {
			realAgentBrowser = resolved
		}
		// Don't treat our own shim as the real binary.
		if filepath.Dir(realAgentBrowser) == shimDir {
			realAgentBrowser = ""
		}
	}

	agents := []struct{ name, bin string }{
		{"claude", "claude"},
		{"codex", "codex"},
		{"cursor-agent", "cursor-agent"},
		{"agent-browser", "agent-browser"},
	}
	foundAgents := []string{}
	for _, a := range agents {
		path, err := exec.LookPath(a.bin)
		if a.name == "agent-browser" && realAgentBrowser != "" {
			path = realAgentBrowser
			err = nil
		}
		if err != nil || path == "" {
			status.Checks = append(status.Checks, DoctorCheck{
				ID:      "cli:" + a.name,
				Label:   a.bin,
				Status:  "info",
				Detail:  "Not found on PATH",
				FixHint: fmt.Sprintf("Install %s and ensure it is on PATH", a.bin),
			})
			continue
		}
		foundAgents = append(foundAgents, a.name)
		status.Checks = append(status.Checks, DoctorCheck{
			ID:     "cli:" + a.name,
			Label:  a.bin,
			Status: "ok",
			Detail: path,
		})
	}
	status.Agents = foundAgents

	if fix {
		if err := applyDoctorDefaults(foundAgents, realAgentBrowser); err != nil {
			status.Checks = append(status.Checks, DoctorCheck{
				ID:     "config",
				Label:  "Config defaults",
				Status: "warn",
				Detail: err.Error(),
			})
		} else {
			status.Checks = append(status.Checks, DoctorCheck{
				ID:     "config",
				Label:  "Config defaults",
				Status: "ok",
				Detail: configPath(),
			})
		}

		agentsMDChecks, err := materializeAgentsMD()
		if err != nil {
			status.Checks = append(status.Checks, DoctorCheck{
				ID:     "agentsMD",
				Label:  "AGENTS.md",
				Status: "warn",
				Detail: err.Error(),
			})
		} else {
			status.Checks = append(status.Checks, agentsMDChecks...)
		}

		if realAgentBrowser != "" {
			if err := resetAgentBrowserSessions(realAgentBrowser); err != nil {
				status.Checks = append(status.Checks, DoctorCheck{
					ID:      "agentBrowserSession",
					Label:   "agent-browser session",
					Status:  "warn",
					Detail:  err.Error(),
					FixHint: "doctor --fix runs close --all (wipes every side-panel browser session)",
				})
			} else {
				status.Checks = append(status.Checks, DoctorCheck{
					ID:     "agentBrowserSession",
					Label:  "agent-browser session",
					Status: "ok",
					Detail: "Cleared all agent-browser sessions (destructive to concurrent Claudes) so the next command uses live Chrome",
				})
			}
		}
	} else {
		status.Checks = append(status.Checks, checkAgentsMD()...)
	}

	// Probe after session reset on --fix so a stale daemon does not false-fail attach.
	chromeCheck := checkChromeRemoteDebugging()
	status.Checks = append(status.Checks, chromeCheck)

	if runtime.GOOS == "darwin" {
		status.Checks = append(status.Checks, DoctorCheck{
			ID:     "macosTCC",
			Label:  "macOS privacy prompts",
			Status: "info",
			Detail: "TCC may name the host binary (agent-terminal) when the agent touches iCloud or other apps — click Allow",
		})
	}

	cfg, _ := readConfigMap()
	status.Config = cfg

	hasError, hasWarn := false, false
	for _, c := range status.Checks {
		if c.Status == "error" {
			hasError = true
		}
		if c.Status == "warn" {
			hasWarn = true
		}
	}
	switch {
	case hasError:
		status.Level = "error"
		status.Ok = false
		status.Message = "Fix the errors above, then reopen the side panel."
	case hasWarn:
		status.Level = "warn"
		status.Ok = true
		status.Message = "Setup is usable; review warnings when you can."
	default:
		status.Level = "ok"
		status.Ok = true
		status.Message = "Ready. Open the Agent Terminal side panel in Chrome."
	}

	return status, nil
}

func checkNativeMessaging() (bool, string) {
	browsers, err := GetBrowsers()
	if err != nil {
		return false, err.Error()
	}
	found := 0
	for _, b := range browsers {
		path := filepath.Join(b.ManifestDir, nativeHostManifest)
		if _, err := os.Stat(path); err == nil {
			found++
		}
	}
	if found == 0 {
		return false, "No native messaging manifests found"
	}
	return true, fmt.Sprintf("%d browser manifest(s) present", found)
}

func ensureInstall() error {
	return InstallNativeMessagingHost()
}

func chromeUserDataDir() string {
	home := os.Getenv("HOME")
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	case "linux":
		return filepath.Join(home, ".config", "google-chrome")
	default:
		return ""
	}
}

func checkChromeRemoteDebugging() DoctorCheck {
	c := DoctorCheck{
		ID:      "chromeRemoteDebugging",
		Label:   "Chrome remote debugging",
		FixHint: "Open chrome://inspect/#remote-debugging, enable the toggle once, keep Chrome open, click Allow when prompted, then: agent-terminal doctor --fix",
	}

	userData := chromeUserDataDir()
	if userData == "" {
		c.Status = "info"
		c.Detail = "Enable once at chrome://inspect/#remote-debugging (Chrome 144+), then click Allow when prompted"
		return c
	}

	enabled, err := chromeRemoteDebuggingEnabled()
	if err != nil {
		c.Status = "error"
		c.Detail = "Chrome Local State not found — is Google Chrome installed and launched once?"
		return c
	}

	if !enabled {
		c.Status = "error"
		c.Detail = "Off — enable once at chrome://inspect/#remote-debugging (Agent Terminal will not spawn a second browser)"
		return c
	}

	// Soft probe only (file + TCP). Never WS-handshake here: doctor.status runs on
	// every panel open and a deep probe would stack "Allow remote debugging?" dialogs.
	// Missing DevToolsActivePort while the toggle is on is common until Chrome is
	// running / a client attaches — warn, do not error the side-panel banner.
	ep, err := softProbeLiveChrome()
	if err != nil {
		c.Status = "warn"
		c.Detail = "Enabled — keep Google Chrome open; click Allow once when agent-browser first connects"
		c.FixHint = "Open Chrome with your normal profile, leave it running, then retry a browser task"
		return c
	}

	_ = saveCDPFingerprint(ep.Fingerprint)
	c.Status = "ok"
	detail := fmt.Sprintf("Ready (CDP %s)", ep.WSURL)
	if last := chromeLastUsedProfileDir(); last != "" {
		detail = fmt.Sprintf("Ready — primary profile %q (CDP %s)", last, ep.WSURL)
	}
	c.Detail = detail
	c.FixHint = ""
	return c
}

func applyDoctorDefaults(foundAgents []string, realAgentBrowser string) error {
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(shimDir, 0755); err != nil {
		return err
	}

	updates := map[string]interface{}{}

	current, err := readConfigMap()
	if err != nil {
		current = map[string]interface{}{}
	}

	if _, ok := current["command"]; !ok {
		updates["command"] = getDefaultShell()
	}
	if _, ok := current["theme"]; !ok {
		updates["theme"] = "Tomorrow Night"
	}
	if _, ok := current["themeDark"]; !ok {
		updates["themeDark"] = "Tomorrow Night"
	}

	xterm, _ := current["xterm"].(map[string]interface{})
	if xterm == nil {
		xterm = map[string]interface{}{}
	}
	if _, ok := xterm["fontSize"]; !ok {
		xterm["fontSize"] = 13.0
		updates["xterm"] = xterm
	}

	env, _ := current["env"].(map[string]interface{})
	if env == nil {
		env = map[string]interface{}{}
	}
	envChanged := false

	hasAgentBrowser := realAgentBrowser != ""
	for _, a := range foundAgents {
		if a == "agent-browser" {
			hasAgentBrowser = true
			break
		}
	}

	if hasAgentBrowser && realAgentBrowser != "" {
		if err := writeAgentBrowserShim(realAgentBrowser); err != nil {
			return err
		}

		env["AGENT_BROWSER_AUTO_CONNECT"] = "1"
		// Always rebuild PATH from the process environment so repeated doctor
		// runs do not stack duplicate entries from a previous config PATH.
		env["PATH"] = prependPath(os.Getenv("PATH"), shimDir)
		envChanged = true
	}

	if envChanged {
		updates["env"] = env
	}

	for _, name := range foundAgents {
		if name == "agent-browser" {
			continue
		}
		if err := writeAgentApp(name); err != nil {
			return err
		}
	}

	if len(updates) == 0 {
		return nil
	}
	return writeConfigMap(updates)
}

func writeAgentBrowserShim(realBinary string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve agent-terminal binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	if err := os.MkdirAll(shimDir, 0755); err != nil {
		return err
	}
	// Sidecar so the daemon can heal without re-resolving PATH (shim shadows the real binary).
	if err := os.WriteFile(realAgentBrowserSidecarPath(), []byte(realBinary+"\n"), 0644); err != nil {
		return err
	}

	script := fmt.Sprintf(`#!/bin/sh
# Agent Terminal shim: live Chrome only via browser-gate (never spawn cold Chrome).
export AGENT_BROWSER_AUTO_CONNECT=1
exec %s browser-gate --real %s -- "$@"
`, shellQuote(self), shellQuote(realBinary))
	target := filepath.Join(shimDir, "agent-browser")
	return os.WriteFile(target, []byte(script), 0755)
}

func realAgentBrowserSidecarPath() string {
	return filepath.Join(shimDir, "agent-browser.real")
}

func loadRealAgentBrowserPath() string {
	data, err := os.ReadFile(realAgentBrowserSidecarPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func resetAgentBrowserSessions(realBinary string) error {
	cmd := exec.Command(realBinary, "close", "--all")
	cmd.Env = append(os.Environ(), "AGENT_BROWSER_AUTO_CONNECT=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// close --all can fail if nothing is running; treat as ok when message is benign.
		msg := strings.ToLower(string(out) + err.Error())
		if strings.Contains(msg, "no session") || strings.Contains(msg, "not found") || strings.Contains(msg, "no running") {
			return nil
		}
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// closeNamedAgentBrowserSession closes one agent-browser session (never --all).
func closeNamedAgentBrowserSession(sessionName string) error {
	if sessionName == "" {
		return nil
	}
	real := loadRealAgentBrowserPath()
	if real == "" {
		return nil
	}
	if _, err := os.Stat(real); err != nil {
		return nil
	}
	cmd := exec.Command(real, "--session", sessionName, "close")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out) + err.Error())
		if strings.Contains(msg, "no session") || strings.Contains(msg, "not found") || strings.Contains(msg, "no running") {
			return nil
		}
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func prependPath(pathEnv, dir string) string {
	if dir == "" {
		return pathEnv
	}
	parts := filepath.SplitList(pathEnv)
	out := make([]string, 0, len(parts)+1)
	out = append(out, dir)
	for _, p := range parts {
		if p == "" || p == dir {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func writeAgentApp(name string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil
	}
	script := fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", shellQuote(path))
	target := filepath.Join(appDir, name)
	if err := os.WriteFile(target, []byte(script), 0755); err != nil {
		return err
	}
	return nil
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
