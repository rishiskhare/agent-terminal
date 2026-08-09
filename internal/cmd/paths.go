package cmd

import (
	"os"
	"path/filepath"
	"runtime"
)

var (
	configDir  string
	cacheDir   string
	dataDir    string
	commandDir string
	appDir     string
	shimDir    string

	legacyConfigDir string
	legacyCacheDir  string
	legacyDataDir   string
)

func init() {
	home := userHomeDir()
	if runtime.GOOS == "windows" {
		appData := envOr("APPDATA", filepath.Join(home, "AppData", "Roaming"))
		local := envOr("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		configDir = filepath.Join(appData, "agent-terminal")
		cacheDir = filepath.Join(local, "agent-terminal", "Cache")
		dataDir = filepath.Join(local, "agent-terminal")
	} else {
		configDir = filepath.Join(home, ".config", "agent-terminal")
		cacheDir = filepath.Join(home, ".cache", "agent-terminal")
		dataDir = filepath.Join(home, ".local", "share", "agent-terminal")
	}
	// Tweety migration sources (may exist on any OS from older installs).
	legacyConfigDir = filepath.Join(home, ".config", "tweety")
	legacyCacheDir = filepath.Join(home, ".cache", "tweety")
	legacyDataDir = filepath.Join(home, ".local", "share", "tweety")

	commandDir = filepath.Join(configDir, "commands")
	appDir = filepath.Join(configDir, "apps")
	shimDir = filepath.Join(dataDir, "bin")
}

func userHomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return ""
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func agentBrowserShimPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(shimDir, "agent-browser.cmd")
	}
	return filepath.Join(shimDir, "agent-browser")
}
