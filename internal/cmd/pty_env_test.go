package cmd

import (
	"strings"
	"testing"
)

func TestMergePtyEnvReplacesPATH(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/test",
		"PATH=/should/not/win", // duplicate — first wins until overlay replaces
	}
	got := mergePtyEnv(base, map[string]string{
		"PATH": "/shim:/usr/bin",
		"TERM": "xterm-256color",
	})
	var path string
	pathCount := 0
	for _, e := range got {
		if strings.HasPrefix(e, "PATH=") {
			pathCount++
			path = strings.TrimPrefix(e, "PATH=")
		}
	}
	if pathCount != 1 {
		t.Fatalf("expected one PATH, got %d in %v", pathCount, got)
	}
	if path != "/shim:/usr/bin" {
		t.Fatalf("PATH not replaced: %q", path)
	}
}

func TestPrependPathKeepsShimFirst(t *testing.T) {
	got := prependPath("/usr/bin:/shim:/bin", "/shim")
	if got != "/shim:/usr/bin:/bin" {
		t.Fatalf("prependPath: %q", got)
	}
}
