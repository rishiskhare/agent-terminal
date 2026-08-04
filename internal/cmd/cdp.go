package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	cdpVerifyTimeout = 3 * time.Second
	cdpTCPTimeout    = 500 * time.Millisecond
	cdpRetryWait     = 1500 * time.Millisecond
)

// CDPEndpoint is a live Chrome DevTools Protocol browser target.
type CDPEndpoint struct {
	Port        int
	WSPath      string
	WSURL       string
	Fingerprint string
	UserDataDir string // optional; set when discovered from a known user-data-dir
}

// liveChromeAttachError is the stderr message when we refuse to spawn a cold browser.
const liveChromeAttachError = `Agent Terminal: cannot attach to your live Chrome.
1. Open Google Chrome with your primary profile (not a Claude/tmp scratchpad window).
2. Enable chrome://inspect/#remote-debugging in that Chrome.
3. Click Allow when Chrome prompts (once per Chrome restart).
4. Quit any Chrome started with --user-data-dir under /tmp (Claude scratchpads).
5. Retry: agent-terminal doctor --fix
`

func cdpEndpointCachePath() string {
	return filepath.Join(cacheDir, "cdp-endpoint")
}

func chromeDevToolsActivePortPath() string {
	dir := chromeUserDataDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "DevToolsActivePort")
}

// parseDevToolsActivePort parses Chrome's DevToolsActivePort file contents.
// Line 1 is the port; line 2 is the WebSocket path (e.g. /devtools/browser/<uuid>).
func parseDevToolsActivePort(content string) (*CDPEndpoint, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("DevToolsActivePort is empty")
	}
	lines := strings.Split(content, "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("DevToolsActivePort: expected port and path lines")
	}
	portStr := strings.TrimSpace(lines[0])
	wsPath := strings.TrimSpace(lines[1])
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("DevToolsActivePort: invalid port %q", portStr)
	}
	if wsPath == "" || !strings.HasPrefix(wsPath, "/") {
		return nil, fmt.Errorf("DevToolsActivePort: invalid WebSocket path %q", wsPath)
	}
	ep := &CDPEndpoint{
		Port:        port,
		WSPath:      wsPath,
		WSURL:       fmt.Sprintf("ws://127.0.0.1:%d%s", port, wsPath),
		Fingerprint: fmt.Sprintf("%d|%s", port, wsPath),
	}
	return ep, nil
}

func readDevToolsActivePortFile(path string) (*CDPEndpoint, error) {
	if path == "" {
		return nil, fmt.Errorf("Chrome user data directory unknown on this OS")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseDevToolsActivePort(string(data))
}

// discoverLiveChrome finds a CDP endpoint for the user's real Chrome.
// Prefers DevToolsActivePort under the default user-data-dir; falls back to
// common debugging ports (9222/9229) when the toggle is on but Chrome has not
// written DevToolsActivePort (seen on Chrome 151 + inspect remote-debugging).
// Never attaches to ephemeral Claude/tmp scratchpad profiles.
func discoverLiveChrome() (*CDPEndpoint, error) {
	primary := chromeUserDataDir()
	candidates := chromeUserDataDirCandidates()

	var ephemeralHits []string
	var lastErr error

	// 1) Primary install path first (Profile "Main" / last_used lives here).
	if primary != "" {
		ep, err := readDevToolsActivePortFile(filepath.Join(primary, "DevToolsActivePort"))
		if err == nil && endpointTCPReachable(ep, cdpTCPTimeout) == nil {
			ep.UserDataDir = primary
			return ep, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("primary Chrome CDP port not reachable")
		}
	}

	// 2) Other running Chrome user-data-dirs (non-ephemeral only).
	for _, dir := range candidates {
		if primary != "" && sameChromeUserDataDir(dir, primary) {
			continue
		}
		if isEphemeralChromeUserDataDir(dir) {
			ephemeralHits = append(ephemeralHits, dir)
			continue
		}
		ep, err := readDevToolsActivePortFile(filepath.Join(dir, "DevToolsActivePort"))
		if err != nil {
			lastErr = err
			continue
		}
		if err := endpointTCPReachable(ep, cdpTCPTimeout); err != nil {
			lastErr = err
			continue
		}
		ep.UserDataDir = dir
		return ep, nil
	}

	// 3) Port fallback: toggle on, DevToolsActivePort missing, but Chrome is
	// listening (ws://127.0.0.1:<port>/devtools/browser works on M144+).
	if ep, err := discoverLiveChromeViaDebugPorts(); err == nil {
		return ep, nil
	} else {
		lastErr = err
	}

	if len(ephemeralHits) > 0 {
		return nil, fmt.Errorf("only ephemeral Chrome found (e.g. Claude scratchpad under /tmp) — quit it and use your primary Chrome; saw: %s", ephemeralHits[0])
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("DevToolsActivePort not found for Google Chrome")
}

var commonChromeDebugPorts = []int{9222, 9229}

// discoverLiveChromeViaDebugPorts probes well-known CDP ports when the port file
// is absent. Only used when remote debugging is user-enabled.
func discoverLiveChromeViaDebugPorts() (*CDPEndpoint, error) {
	enabled, err := chromeRemoteDebuggingEnabled()
	if err != nil || !enabled {
		return nil, fmt.Errorf("Chrome remote debugging toggle is off")
	}
	for _, port := range commonChromeDebugPorts {
		hostPort := fmt.Sprintf("127.0.0.1:%d", port)
		if err := tcpReachable(hostPort, cdpTCPTimeout); err != nil {
			continue
		}
		// Generic browser target path — works for chrome://inspect remote-debugging
		// when DevToolsActivePort was never written (Chrome 151 observed).
		wsPath := "/devtools/browser"
		ep := &CDPEndpoint{
			Port:        port,
			WSPath:      wsPath,
			WSURL:       fmt.Sprintf("ws://127.0.0.1:%d%s", port, wsPath),
			Fingerprint: fmt.Sprintf("%d|%s", port, wsPath),
			UserDataDir: chromeUserDataDir(),
		}
		return ep, nil
	}
	return nil, fmt.Errorf("no Chrome debug port listening (tried 9222, 9229)")
}

func sameChromeUserDataDir(a, b string) bool {
	ca, errA := filepath.Abs(filepath.Clean(a))
	cb, errB := filepath.Abs(filepath.Clean(b))
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ca == cb
}

// isEphemeralChromeUserDataDir reports Claude scratchpads and other temp profiles
// that must never be treated as the user's primary Chrome.
func isEphemeralChromeUserDataDir(dir string) bool {
	d := strings.ToLower(filepath.Clean(dir))
	switch {
	case strings.Contains(d, "/tmp/"), strings.Contains(d, "/private/tmp/"):
		return true
	case strings.Contains(d, "scratchpad"):
		return true
	case strings.Contains(d, "/claude-"), strings.Contains(d, "claude-501"):
		return true
	case strings.Contains(d, "chrome-for-testing"), strings.Contains(d, "agent-browser-chrome"):
		return true
	default:
		return false
	}
}

// chromeUserDataDirCandidates lists user-data-dirs from running Google Chrome
// processes (best-effort). primary dir is not duplicated here as a requirement.
func chromeUserDataDirCandidates() []string {
	out, err := exec.Command("ps", "-ax", "-o", "command=").Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var dirs []string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "Google Chrome") && !strings.Contains(line, "google-chrome") {
			continue
		}
		if strings.Contains(line, "Helper") {
			continue
		}
		dir := parseUserDataDirArg(line)
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	return dirs
}

func parseUserDataDirArg(commandLine string) string {
	const flag = "--user-data-dir="
	i := strings.Index(commandLine, flag)
	if i < 0 {
		return ""
	}
	rest := commandLine[i+len(flag):]
	if rest == "" {
		return ""
	}
	switch rest[0] {
	case '"':
		end := strings.IndexByte(rest[1:], '"')
		if end < 0 {
			return ""
		}
		return rest[1 : 1+end]
	case '\'':
		end := strings.IndexByte(rest[1:], '\'')
		if end < 0 {
			return ""
		}
		return rest[1 : 1+end]
	default:
		// Unquoted: value runs until next space-prefixed flag or end.
		for j := 0; j < len(rest); j++ {
			if rest[j] == ' ' && j+1 < len(rest) && rest[j+1] == '-' {
				return rest[:j]
			}
		}
		return strings.TrimSpace(rest)
	}
}

// chromeLastUsedProfileDir returns Local State's profile.last_used (e.g. "Profile 7").
func chromeLastUsedProfileDir() string {
	userData := chromeUserDataDir()
	if userData == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(userData, "Local State"))
	if err != nil {
		return ""
	}
	var localState map[string]any
	if err := json.Unmarshal(data, &localState); err != nil {
		return ""
	}
	prof, _ := localState["profile"].(map[string]any)
	switch v := prof["last_used"].(type) {
	case string:
		return v
	default:
		return ""
	}
}

func loadCDPFingerprint() (string, error) {
	data, err := os.ReadFile(cdpEndpointCachePath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func saveCDPFingerprint(fp string) error {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(cdpEndpointCachePath(), []byte(fp+"\n"), 0644)
}

// needsSessionHeal reports whether the agent-browser daemon should be cleared
// because Chrome's CDP endpoint changed since the last successful attach.
func needsSessionHeal(currentFP string) bool {
	if currentFP == "" {
		return false
	}
	prev, err := loadCDPFingerprint()
	if err != nil || prev == "" {
		return false
	}
	return prev != currentFP
}

func cdpHostPort(wsURL string) string {
	hostPort := strings.TrimPrefix(wsURL, "ws://")
	hostPort = strings.TrimPrefix(hostPort, "wss://")
	if i := strings.IndexByte(hostPort, '/'); i >= 0 {
		hostPort = hostPort[:i]
	}
	return hostPort
}

// tcpReachable checks whether something is listening — does NOT trigger Chrome Allow.
func tcpReachable(hostPort string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = cdpTCPTimeout
	}
	conn, err := net.DialTimeout("tcp", hostPort, timeout)
	if err != nil {
		return fmt.Errorf("Chrome CDP port not reachable: %w", err)
	}
	_ = conn.Close()
	return nil
}

func endpointTCPReachable(ep *CDPEndpoint, timeout time.Duration) error {
	if ep == nil {
		return fmt.Errorf("no CDP endpoint")
	}
	return tcpReachable(cdpHostPort(ep.WSURL), timeout)
}

// verifyCDPEndpoint dials the browser WebSocket and sends Browser.getVersion.
// This triggers Chrome's "Allow remote debugging?" prompt — use only from doctor,
// never on the agent-browser hot path.
func verifyCDPEndpoint(wsURL string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = cdpVerifyTimeout
	}

	if err := tcpReachable(cdpHostPort(wsURL), timeout); err != nil {
		return err
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		Proxy:            http.ProxyFromEnvironment,
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("CDP WebSocket handshake failed (click Allow in Chrome if prompted): %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	_ = conn.SetWriteDeadline(deadline)
	_ = conn.SetReadDeadline(deadline)

	req := map[string]any{"id": 1, "method": "Browser.getVersion"}
	if err := conn.WriteJSON(req); err != nil {
		return fmt.Errorf("CDP write failed: %w", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("CDP read failed: %w", err)
	}
	var resp struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("CDP response invalid: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("CDP error: %s", resp.Error.Message)
	}
	if len(resp.Result) == 0 {
		return fmt.Errorf("CDP Browser.getVersion returned empty result")
	}
	return nil
}

// softProbeLiveChrome discovers DevToolsActivePort and checks TCP only.
// Safe for the hot path: does not open a CDP WebSocket (no Allow prompt).
func softProbeLiveChrome() (*CDPEndpoint, error) {
	ep, err := discoverLiveChrome()
	if err == nil {
		if tErr := endpointTCPReachable(ep, cdpTCPTimeout); tErr == nil {
			return ep, nil
		} else {
			err = tErr
		}
	}

	time.Sleep(cdpRetryWait)

	ep2, err2 := discoverLiveChrome()
	if err2 != nil {
		if err != nil {
			return nil, fmt.Errorf("%v; retry: %w", err, err2)
		}
		return nil, err2
	}
	if tErr := endpointTCPReachable(ep2, cdpTCPTimeout); tErr != nil {
		return nil, tErr
	}
	return ep2, nil
}

// deepProbeLiveChrome discovers and verifies via a full CDP WebSocket handshake.
// Doctor-only: each call can trigger Chrome's Allow prompt.
func deepProbeLiveChrome() (*CDPEndpoint, error) {
	ep, err := softProbeLiveChrome()
	if err != nil {
		return nil, err
	}
	if err := verifyCDPEndpoint(ep.WSURL, cdpVerifyTimeout); err != nil {
		return nil, err
	}
	return ep, nil
}

// chromeRemoteDebuggingEnabled reads Local State for the user toggle.
func chromeRemoteDebuggingEnabled() (enabled bool, err error) {
	userData := chromeUserDataDir()
	if userData == "" {
		return false, fmt.Errorf("unsupported OS")
	}
	data, err := os.ReadFile(filepath.Join(userData, "Local State"))
	if err != nil {
		return false, err
	}
	var localState map[string]any
	if err := json.Unmarshal(data, &localState); err != nil {
		return false, err
	}
	devtools, _ := localState["devtools"].(map[string]any)
	rd, _ := devtools["remote_debugging"].(map[string]any)
	switch v := rd["user-enabled"].(type) {
	case bool:
		return v, nil
	default:
		return false, nil
	}
}
