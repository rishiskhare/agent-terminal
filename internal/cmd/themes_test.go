package cmd

import (
	"path"
	"testing"
)

func TestThemeEmbedPathUsesSlash(t *testing.T) {
	name := "Tomorrow Night"
	p := path.Join("themes", name+".json")
	if p != "themes/Tomorrow Night.json" {
		t.Fatalf("embed path must use forward slashes, got %q", p)
	}
	if _, err := themeFs.ReadFile(p); err != nil {
		t.Fatalf("read embedded theme: %v", err)
	}
}
