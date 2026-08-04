package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDevToolsActivePort(t *testing.T) {
	ep, err := parseDevToolsActivePort("9222\n/devtools/browser/abc-def\n")
	if err != nil {
		t.Fatal(err)
	}
	if ep.Port != 9222 || ep.WSPath != "/devtools/browser/abc-def" {
		t.Fatalf("unexpected parse: %+v", ep)
	}
	if ep.WSURL != "ws://127.0.0.1:9222/devtools/browser/abc-def" {
		t.Fatalf("ws url: %s", ep.WSURL)
	}
	if ep.Fingerprint != "9222|/devtools/browser/abc-def" {
		t.Fatalf("fingerprint: %s", ep.Fingerprint)
	}
}

func TestParseDevToolsActivePortWhitespace(t *testing.T) {
	ep, err := parseDevToolsActivePort("  54459  \n  /devtools/browser/uuid  \n")
	if err != nil {
		t.Fatal(err)
	}
	if ep.Port != 54459 {
		t.Fatalf("port: %d", ep.Port)
	}
}

func TestParseDevToolsActivePortErrors(t *testing.T) {
	cases := []string{
		"",
		"9222",
		"nope\n/devtools/browser/x",
		"0\n/devtools/browser/x",
		"9222\n",
		"9222\ndevtools/browser/x",
	}
	for _, c := range cases {
		if _, err := parseDevToolsActivePort(c); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestNeedsSessionHeal(t *testing.T) {
	orig := cacheDir
	t.Cleanup(func() { cacheDir = orig })
	cacheDir = t.TempDir()

	if needsSessionHeal("1|/a") {
		t.Fatal("no prior fingerprint should not heal")
	}
	if err := saveCDPFingerprint("1|/a"); err != nil {
		t.Fatal(err)
	}
	if needsSessionHeal("1|/a") {
		t.Fatal("same fingerprint should not heal")
	}
	if !needsSessionHeal("2|/b") {
		t.Fatal("changed fingerprint should heal")
	}
}

func TestVerifyCDPEndpointClosedPort(t *testing.T) {
	// Bind and immediately close so the port is unused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	err = verifyCDPEndpoint("ws://"+addr+"/devtools/browser/x", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected error against closed port")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSoftProbeLiveChromeTCPOnly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	portFile := filepath.Join(dir, "DevToolsActivePort")
	content := fmt.Sprintf("%d\n/devtools/browser/soft-probe\n", port)
	if err := os.WriteFile(portFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ep, err := readDevToolsActivePortFile(portFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := endpointTCPReachable(ep, 200*time.Millisecond); err != nil {
		t.Fatalf("TCP should succeed on listening port: %v", err)
	}

	// Soft probe must not require a WebSocket server — TCP is enough.
	_ = ln.Close()
	if err := endpointTCPReachable(ep, 100*time.Millisecond); err == nil {
		t.Fatal("expected TCP failure after close")
	}
}

func TestTCPReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := tcpReachable(addr, 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	if err := tcpReachable(addr, 100*time.Millisecond); err == nil {
		t.Fatal("expected failure")
	}
}

func TestReadDevToolsActivePortFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DevToolsActivePort")
	if err := os.WriteFile(path, []byte("9333\n/devtools/browser/test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ep, err := readDevToolsActivePortFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Port != 9333 {
		t.Fatalf("port: %d", ep.Port)
	}
}

func TestIsEphemeralChromeUserDataDir(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{"/Users/rishikhare/Library/Application Support/Google/Chrome", false},
		{"/private/tmp/claude-501/foo/scratchpad/chrome-profile", true},
		{"/tmp/agent-browser-chrome-xyz", true},
		{"/var/folders/xx/chrome-for-testing", true},
	}
	for _, tc := range cases {
		if got := isEphemeralChromeUserDataDir(tc.dir); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.dir, got, tc.want)
		}
	}
}

func TestParseUserDataDirArg(t *testing.T) {
	got := parseUserDataDirArg(`/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --remote-debugging-port=9222 --user-data-dir=/private/tmp/claude-501/scratchpad/chrome-profile`)
	want := "/private/tmp/claude-501/scratchpad/chrome-profile"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got = parseUserDataDirArg(`Chrome --user-data-dir="/path/with spaces/Chrome" --foo`)
	if got != "/path/with spaces/Chrome" {
		t.Fatalf("quoted: %q", got)
	}
}

func TestDiscoverLiveChromePrefersPrimaryOverEphemeral(t *testing.T) {
	// Unit-level: ephemeral classifier + primary path helper wiring.
	primary := chromeUserDataDir()
	if primary == "" {
		t.Skip("no chrome user data dir on this OS")
	}
	if isEphemeralChromeUserDataDir(primary) {
		t.Fatalf("primary should not be ephemeral: %s", primary)
	}
}
