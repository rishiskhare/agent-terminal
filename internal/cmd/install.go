package cmd

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"text/template"

	"github.com/spf13/cobra"
)

//go:embed all:embed
var embedFs embed.FS

type BrowserType string

var (
	BrowserTypeChromium BrowserType = "chromium"
	BrowserTypeGecko    BrowserType = "gecko"
)

// findExecPath reconstructs the executable path without using os.Executable()
func findExecPath(entry string) (string, error) {
	if filepath.IsAbs(entry) {
		return filepath.Clean(entry), nil
	}

	// 2. If it contains a path separator, it's relative to the current directory
	// Example: ./my-app or bin/my-app
	if filepath.Base(entry) != entry {
		return filepath.Abs(entry)
	}

	// 3. It's a bare name (e.g., "my-app"), so we must search the system PATH
	// exec.LookPath mimics the shell's logic to find the binary
	lp, err := exec.LookPath(entry)
	if err != nil {
		return "", fmt.Errorf("could not locate executable in PATH: %w", err)
	}

	// LookPath might return a relative path depending on the OS/Env;
	// we convert it to absolute to be sure.
	return filepath.Abs(lp)
}

func NewCmdInstall() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install native messaging host manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := InstallNativeMessagingHost(); err != nil {
				return err
			}
			cmd.PrintErrln("Installed native messaging host manifest.")

			if _, err := runDoctor(true); err != nil {
				cmd.PrintErrf("Doctor: %v\n", err)
			} else {
				cmd.PrintErrln("Doctor applied safe defaults. Open the Agent Terminal side panel.")
			}
			return nil
		},
	}
}

// InstallNativeMessagingHost writes the host wrapper and browser manifests.
func InstallNativeMessagingHost() error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	hostTemplate, err := template.ParseFS(embedFs, "embed/native_messaging_host.tmpl")
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	hostPath := filepath.Join(dataDir, "native_messaging_host")
	f, err := os.Create(hostPath)
	if err != nil {
		return fmt.Errorf("failed to create native messaging host file: %w", err)
	}
	defer f.Close()

	execPath, err := findExecPath(os.Args[0])
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	if err := hostTemplate.Execute(f, map[string]interface{}{
		"ExecPath": execPath,
	}); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	if err := os.Chmod(hostPath, 0755); err != nil {
		return fmt.Errorf("failed to make host file executable: %w", err)
	}

	manifestTemplate, err := template.ParseFS(embedFs, "embed/"+nativeHostManifest+".tmpl")
	if err != nil {
		return fmt.Errorf("failed to parse manifest template: %w", err)
	}

	browsers, err := GetBrowsers()
	if err != nil {
		return fmt.Errorf("failed to get manifest directories: %w", err)
	}

	for _, browser := range browsers {
		if _, err := os.Stat(filepath.Dir(browser.ManifestDir)); os.IsNotExist(err) {
			continue
		}

		if err := os.MkdirAll(browser.ManifestDir, 0755); err != nil {
			return fmt.Errorf("failed to create native messaging hosts directory: %w", err)
		}

		// Remove legacy host manifest if present.
		_ = os.Remove(filepath.Join(browser.ManifestDir, legacyNativeHostManifest))

		mf, err := os.Create(filepath.Join(browser.ManifestDir, nativeHostManifest))
		if err != nil {
			return fmt.Errorf("failed to get manifest file path: %w", err)
		}

		if err := manifestTemplate.Execute(mf, map[string]interface{}{
			"Path":    hostPath,
			"Browser": browser.Type,
		}); err != nil {
			mf.Close()
			return fmt.Errorf("failed to execute manifest template: %w", err)
		}
		mf.Close()
	}

	return nil
}

type Browser struct {
	ManifestDir string
	Type        BrowserType
}

func GetBrowsers() ([]Browser, error) {
	switch runtime.GOOS {
	case "darwin":
		supportDir := filepath.Join(os.Getenv("HOME"), "Library", "Application Support")
		return []Browser{
			{filepath.Join(supportDir, "Google", "Chrome", "NativeMessagingHosts"), BrowserTypeChromium},
			{filepath.Join(supportDir, "Chromium", "NativeMessagingHosts"), BrowserTypeChromium},
			{filepath.Join(supportDir, "BraveSoftware", "Brave-Browser", "NativeMessagingHosts"), BrowserTypeChromium},
			{filepath.Join(supportDir, "Vivaldi", "NativeMessagingHosts"), BrowserTypeChromium},
			{filepath.Join(supportDir, "net.imput.helium", "NativeMessagingHosts"), BrowserTypeChromium},
			{filepath.Join(supportDir, "Microsoft", "Edge", "NativeMessagingHosts"), BrowserTypeChromium},
			{filepath.Join(supportDir, "Mozilla", "NativeMessagingHosts"), BrowserTypeGecko},
			{filepath.Join(supportDir, "zen", "NativeMessagingHosts"), BrowserTypeGecko},
		}, nil
	case "linux":
		homeDir := os.Getenv("HOME")
		linuxConfigDir := filepath.Join(homeDir, ".config")
		mozillaConfigDir := filepath.Join(homeDir, ".mozilla")
		return []Browser{
			{filepath.Join(linuxConfigDir, "google-chrome", "native-messaging-hosts"), BrowserTypeChromium},
			{filepath.Join(linuxConfigDir, "chromium", "native-messaging-hosts"), BrowserTypeChromium},
			{filepath.Join(linuxConfigDir, "microsoft-edge", "native-messaging-hosts"), BrowserTypeChromium},
			{filepath.Join(linuxConfigDir, "brave", "native-messaging-hosts"), BrowserTypeChromium},
			{filepath.Join(linuxConfigDir, "vivaldi", "native-messaging-hosts"), BrowserTypeChromium},
			{filepath.Join(linuxConfigDir, "helium", "native-messaging-hosts"), BrowserTypeChromium},
			{filepath.Join(mozillaConfigDir, "native-messaging-hosts"), BrowserTypeGecko},
			{filepath.Join(linuxConfigDir, "zen", "native-messaging-hosts"), BrowserTypeGecko},
		}, nil
	}

	return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}
