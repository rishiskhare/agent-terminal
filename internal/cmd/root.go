package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	jsonparser "github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"

	"github.com/knadh/koanf/v2"

	"github.com/spf13/cobra"
)

var k = koanf.New(".")

var (
	maxBufferSizeBytes   = 512
	keepalivePingTimeout = 20 * time.Second
)

const nativeHostID = "com.agentterminal.native"
const nativeHostManifest = "com.agentterminal.native.json"
const legacyNativeHostManifest = "com.github.pomdtr.tweety.json"

func NewCmdRoot(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent-terminal",
		SilenceUsage: true,
		Short:        "An integrated terminal for your agent in your browser",
		Version:      version,
		Args:         cobra.ArbitraryArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return loadConfig()
		},
	}

	cmd.Flags().SetInterspersed(true)

	cmd.AddCommand(
		NewCmdServe(),
		NewCmdInstall(),
		NewCmdRun(),
		NewCmdDoctor(),
		NewCmdBrowserGate(),
	)

	return cmd
}

func loadConfig() error {
	migrateLegacyDirs()

	confmapProvider := confmap.Provider(map[string]interface{}{
		"command": getDefaultShell(),
		"theme":   "Tomorrow Night",
		"xterm": map[string]interface{}{
			"fontSize": 13,
		},
	}, ".")
	if err := k.Load(confmapProvider, nil); err != nil {
		return fmt.Errorf("failed to load default config: %w", err)
	}

	configPath := filepath.Join(configDir, "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		configBytes, err := json.MarshalIndent(map[string]interface{}{
			"command": getDefaultShell(),
			"theme":   "Tomorrow Night",
			"xterm": map[string]interface{}{
				"fontSize": 13,
			},
		}, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal default config: %w", err)
		}

		if err := os.WriteFile(configPath, configBytes, 0644); err != nil {
			return fmt.Errorf("failed to write default config: %w", err)
		}
	}

	f := file.Provider(configPath)
	if err := k.Load(f, jsonparser.Parser()); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	f.Watch(func(event interface{}, err error) {
		if err != nil {
			log.Printf("watch error: %v", err)
			return
		}

		k = koanf.New(".")
		_ = k.Load(confmapProvider, nil)
		_ = k.Load(f, jsonparser.Parser())
	})

	return nil
}

// migrateLegacyDirs renames old tweety XDG dirs to agent-terminal once.
// On Windows, also migrate prior Git-Bash/XDG-style agent-terminal dirs into AppData.
func migrateLegacyDirs() {
	if runtime.GOOS == "windows" {
		home := userHomeDir()
		migrateDir(filepath.Join(home, ".config", "agent-terminal"), configDir)
		migrateDir(filepath.Join(home, ".cache", "agent-terminal"), cacheDir)
		migrateDir(filepath.Join(home, ".local", "share", "agent-terminal"), dataDir)
	}
	migrateDir(legacyConfigDir, configDir)
	migrateDir(legacyCacheDir, cacheDir)
	migrateDir(legacyDataDir, dataDir)
}

func migrateDir(from, to string) {
	if from == "" || to == "" || from == to {
		return
	}
	if _, err := os.Stat(to); err == nil {
		return
	}
	if _, err := os.Stat(from); err != nil {
		return
	}
	if err := os.Rename(from, to); err != nil {
		log.Printf("failed to migrate %s -> %s: %v", from, to, err)
	}
}

func configPath() string {
	return filepath.Join(configDir, "config.json")
}

// writeConfigMap merges values into config.json and reloads koanf.
func writeConfigMap(updates map[string]interface{}) error {
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	current := map[string]interface{}{}
	path := configPath()
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &current)
	}

	for key, value := range updates {
		if value == nil {
			delete(current, key)
			continue
		}
		current[key] = value
	}

	out, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return loadConfig()
}

func readConfigMap() (map[string]interface{}, error) {
	current := map[string]interface{}{}
	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{
				"command": getDefaultShell(),
				"theme":   "Tomorrow Night",
			}, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &current); err != nil {
		return nil, err
	}
	return current, nil
}

func getDefaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	switch runtime.GOOS {
	case "darwin":
		return "/bin/zsh"
	case "linux":
		return "/bin/bash"
	case "windows":
		// Prefer Git Bash when present so agent app launchers (#!/bin/sh) work.
		for _, candidate := range []string{
			filepath.Join(os.Getenv("PROGRAMFILES"), "Git", "bin", "bash.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Git", "bin", "bash.exe"),
			`C:\Program Files\Git\bin\bash.exe`,
		} {
			if candidate == "" {
				continue
			}
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
		if ps, err := exec.LookPath("powershell.exe"); err == nil {
			return ps
		}
		if comspec := os.Getenv("COMSPEC"); comspec != "" {
			return comspec
		}
		return `C:\Windows\System32\cmd.exe`
	default:
		return "/bin/sh"
	}
}
