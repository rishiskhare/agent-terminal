package cmd

import "testing"

func TestIsSafeAppID(t *testing.T) {
	ok := []string{"claude", "codex", "cursor-agent", "my_app", "A1"}
	for _, id := range ok {
		if !isSafeAppID(id) {
			t.Fatalf("expected safe: %q", id)
		}
	}
	bad := []string{"", "../x", "a/b", "a b", "a;b", "app\n"}
	for _, id := range bad {
		if isSafeAppID(id) {
			t.Fatalf("expected unsafe: %q", id)
		}
	}
}
