package cmd

import (
	"reflect"
	"testing"
)

func TestIsBrowserGatePassthrough(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, true},
		{[]string{}, true},
		{[]string{"--help"}, true},
		{[]string{"-h"}, true},
		{[]string{"--version"}, true},
		{[]string{"skills", "get", "core"}, true},
		{[]string{"close", "--all"}, true},
		{[]string{"open", "https://example.com"}, false},
		{[]string{"--auto-connect", "snapshot", "-i"}, false},
		{[]string{"snapshot"}, false},
	}
	for _, tc := range cases {
		if got := isBrowserGatePassthrough(tc.args); got != tc.want {
			t.Fatalf("passthrough(%v)=%v want %v", tc.args, got, tc.want)
		}
	}
}

func TestRewriteAgentBrowserArgs(t *testing.T) {
	ws := "ws://127.0.0.1:9222/devtools/browser/x"
	got := rewriteAgentBrowserArgs([]string{"--auto-connect", "open", "https://x.test"}, ws)
	want := []string{"--cdp", ws, "tab", "new", "https://x.test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	got = rewriteAgentBrowserArgs([]string{"--cdp", "9999", "snapshot"}, ws)
	want = []string{"--cdp", ws, "snapshot"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("strip cdp: got %v want %v", got, want)
	}

	got = rewriteAgentBrowserArgs([]string{"--cdp=ws://old", "click", "@e1"}, ws)
	want = []string{"--cdp", ws, "click", "@e1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("strip cdp=: got %v want %v", got, want)
	}

	got = rewriteAgentBrowserArgs([]string{"goto", "https://a.test"}, ws)
	want = []string{"--cdp", ws, "tab", "new", "https://a.test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("goto: got %v want %v", got, want)
	}

	got = rewriteAgentBrowserArgs([]string{"open"}, ws)
	want = []string{"--cdp", ws, "tab", "new", "about:blank"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("open bare: got %v want %v", got, want)
	}

	got = rewriteAgentBrowserArgs([]string{"window", "new"}, ws)
	want = []string{"--cdp", ws, "window", "new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("window new left alone: got %v want %v", got, want)
	}
}

func TestRewriteOpenToTabNew(t *testing.T) {
	got := rewriteOpenToTabNew([]string{"navigate", "https://b.test", "--headed"})
	want := []string{"tab", "new", "https://b.test", "--headed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	got = rewriteOpenToTabNew([]string{"snapshot", "-i"})
	want = []string{"snapshot", "-i"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("non-open: got %v want %v", got, want)
	}
}

func TestFirstAgentBrowserCommand(t *testing.T) {
	cmd, _ := firstAgentBrowserCommand([]string{"--session", "s1", "open", "u"})
	if cmd != "open" {
		t.Fatalf("got %q", cmd)
	}
	cmd, _ = firstAgentBrowserCommand([]string{"snapshot", "-i"})
	if cmd != "snapshot" {
		t.Fatalf("got %q", cmd)
	}
}

func TestScrubEnvVar(t *testing.T) {
	env := []string{"FOO=1", "AGENT_BROWSER_AUTO_CONNECT=1", "BAR=2"}
	got := scrubEnvVar(env, "AGENT_BROWSER_AUTO_CONNECT")
	if len(got) != 2 || got[0] != "FOO=1" || got[1] != "BAR=2" {
		t.Fatalf("got %v", got)
	}
}

func TestArgsHaveCDP(t *testing.T) {
	if !argsHaveCDP([]string{"--cdp", "ws://x", "snapshot"}) {
		t.Fatal("expected true")
	}
	if argsHaveCDP([]string{"snapshot"}) {
		t.Fatal("expected false")
	}
}
