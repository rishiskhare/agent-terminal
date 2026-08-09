package cmd

import (
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// findExecPath resolves the absolute path of this binary.
// Prefer os.Executable: on Windows, Git Bash passes os.Args[0] as "./agent-terminal"
// with forward slashes, and filepath.Base does not treat "/" as a separator, so the
// old PATH lookup fails with "executable file not found in %PATH%".
func findExecPath(entry string) (string, error) {
	if exe, err := os.Executable(); err == nil && exe != "" {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			return resolved, nil
		}
		return exe, nil
	}

	entry = filepath.FromSlash(entry)
	if filepath.IsAbs(entry) {
		return filepath.Clean(entry), nil
	}

	// Relative path (./my-app, bin/my-app, or Git Bash-style with forward slashes)
	if filepath.Base(entry) != entry || strings.ContainsAny(entry, `/\`) {
		return filepath.Abs(entry)
	}

	lp, err := exec.LookPath(entry)
	if err != nil {
		return "", fmt.Errorf("could not locate executable in PATH: %w", err)
	}
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

	execPath, err := findExecPath(os.Args[0])
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	execPath, err = ensureWindowsHostBinary(execPath)
	if err != nil {
		return err
	}

	hostPath, err := writeNativeMessagingHostWrapper(execPath)
	if err != nil {
		return err
	}

	manifestTemplate, err := template.ParseFS(embedFs, "embed/"+nativeHostManifest+".tmpl")
	if err != nil {
		return fmt.Errorf("failed to parse manifest template: %w", err)
	}

	browsers, err := GetBrowsers()
	if err != nil {
		return fmt.Errorf("failed to get manifest directories: %w", err)
	}

	installed := 0
	for _, browser := range browsers {
		// On Windows, Chromium hosts are discovered via the registry; the manifest
		// file can live anywhere. Still require a writable parent directory.
		parent := filepath.Dir(browser.ManifestDir)
		if _, err := os.Stat(parent); os.IsNotExist(err) {
			// Create dataDir-based parents; skip missing browser profile roots.
			if !strings.HasPrefix(browser.ManifestDir, dataDir) {
				continue
			}
		}

		if err := os.MkdirAll(browser.ManifestDir, 0755); err != nil {
			return fmt.Errorf("failed to create native messaging hosts directory: %w", err)
		}

		// Remove legacy host manifest if present.
		_ = os.Remove(filepath.Join(browser.ManifestDir, legacyNativeHostManifest))

		manifestFile := filepath.Join(browser.ManifestDir, nativeHostManifest)
		mf, err := os.Create(manifestFile)
		if err != nil {
			return fmt.Errorf("failed to get manifest file path: %w", err)
		}

		// Chrome's native messaging JSON must not contain raw backslashes.
		hostPathForManifest := filepath.ToSlash(hostPath)
		if err := manifestTemplate.Execute(mf, map[string]interface{}{
			"Path":    hostPathForManifest,
			"Browser": browser.Type,
		}); err != nil {
			mf.Close()
			return fmt.Errorf("failed to execute manifest template: %w", err)
		}
		mf.Close()

		if browser.RegistryKey != "" {
			if err := registerNativeHostManifest(browser.RegistryKey, manifestFile); err != nil {
				return err
			}
		}
		installed++
	}

	if installed == 0 {
		return fmt.Errorf("no browser native messaging locations found; is Chrome/Edge installed and launched once?")
	}

	return nil
}

// ensureWindowsHostBinary copies the built binary into dataDir as agent-terminal.exe
// when needed. Windows CreateProcess treats extensionless paths as *.exe, so a Git
// Bash build named "agent-terminal" (no .exe) cannot be launched by Chrome.
func ensureWindowsHostBinary(execPath string) (string, error) {
	if runtime.GOOS != "windows" {
		return execPath, nil
	}
	if strings.EqualFold(filepath.Ext(execPath), ".exe") {
		return execPath, nil
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return "", err
	}
	dest := filepath.Join(dataDir, "agent-terminal.exe")
	if err := copyFile(execPath, dest); err != nil {
		return "", fmt.Errorf("copy host binary to %s: %w", dest, err)
	}
	return dest, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func writeNativeMessagingHostWrapper(execPath string) (string, error) {
	tmplName := "embed/native_messaging_host.tmpl"
	hostName := "native_messaging_host"
	if runtime.GOOS == "windows" {
		tmplName = "embed/native_messaging_host.cmd.tmpl"
		hostName = "native_messaging_host.cmd"
	}

	hostTemplate, err := template.ParseFS(embedFs, tmplName)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	hostPath := filepath.Join(dataDir, hostName)
	f, err := os.Create(hostPath)
	if err != nil {
		return "", fmt.Errorf("failed to create native messaging host file: %w", err)
	}
	defer f.Close()

	if err := hostTemplate.Execute(f, map[string]interface{}{
		"ExecPath": execPath,
	}); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	if err := os.Chmod(hostPath, 0755); err != nil {
		return "", fmt.Errorf("failed to make host file executable: %w", err)
	}
	// Remove the Unix shell wrapper if a previous broken Windows install left one behind.
	if runtime.GOOS == "windows" {
		_ = os.Remove(filepath.Join(dataDir, "native_messaging_host"))
	}
	return hostPath, nil
}

type Browser struct {
	ManifestDir string
	// RegistryKey is the HKCU path (Windows Chromium browsers only).
	RegistryKey string
	Type        BrowserType
}

func GetBrowsers() ([]Browser, error) {
	switch runtime.GOOS {
	case "darwin":
		supportDir := filepath.Join(userHomeDir(), "Library", "Application Support")
		return []Browser{
			{ManifestDir: filepath.Join(supportDir, "Google", "Chrome", "NativeMessagingHosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(supportDir, "Chromium", "NativeMessagingHosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(supportDir, "BraveSoftware", "Brave-Browser", "NativeMessagingHosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(supportDir, "Vivaldi", "NativeMessagingHosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(supportDir, "net.imput.helium", "NativeMessagingHosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(supportDir, "Microsoft", "Edge", "NativeMessagingHosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(supportDir, "Mozilla", "NativeMessagingHosts"), Type: BrowserTypeGecko},
			{ManifestDir: filepath.Join(supportDir, "zen", "NativeMessagingHosts"), Type: BrowserTypeGecko},
		}, nil
	case "linux":
		homeDir := userHomeDir()
		linuxConfigDir := filepath.Join(homeDir, ".config")
		mozillaConfigDir := filepath.Join(homeDir, ".mozilla")
		return []Browser{
			{ManifestDir: filepath.Join(linuxConfigDir, "google-chrome", "native-messaging-hosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(linuxConfigDir, "chromium", "native-messaging-hosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(linuxConfigDir, "microsoft-edge", "native-messaging-hosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(linuxConfigDir, "brave", "native-messaging-hosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(linuxConfigDir, "vivaldi", "native-messaging-hosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(linuxConfigDir, "helium", "native-messaging-hosts"), Type: BrowserTypeChromium},
			{ManifestDir: filepath.Join(mozillaConfigDir, "native-messaging-hosts"), Type: BrowserTypeGecko},
			{ManifestDir: filepath.Join(linuxConfigDir, "zen", "native-messaging-hosts"), Type: BrowserTypeGecko},
		}, nil
	case "windows":
		// Chromium on Windows discovers hosts via the registry (manifest can live anywhere).
		manifestDir := filepath.Join(dataDir, "NativeMessagingHosts")
		roaming := envOr("APPDATA", filepath.Join(userHomeDir(), "AppData", "Roaming"))
		return []Browser{
			{
				ManifestDir: manifestDir,
				RegistryKey: `Software\Google\Chrome\NativeMessagingHosts\` + nativeHostID,
				Type:        BrowserTypeChromium,
			},
			{
				ManifestDir: manifestDir,
				RegistryKey: `Software\Chromium\NativeMessagingHosts\` + nativeHostID,
				Type:        BrowserTypeChromium,
			},
			{
				ManifestDir: manifestDir,
				RegistryKey: `Software\Microsoft\Edge\NativeMessagingHosts\` + nativeHostID,
				Type:        BrowserTypeChromium,
			},
			{
				ManifestDir: manifestDir,
				RegistryKey: `Software\BraveSoftware\Brave-Browser\NativeMessagingHosts\` + nativeHostID,
				Type:        BrowserTypeChromium,
			},
			{
				ManifestDir: manifestDir,
				RegistryKey: `Software\Vivaldi\NativeMessagingHosts\` + nativeHostID,
				Type:        BrowserTypeChromium,
			},
			{
				ManifestDir: filepath.Join(roaming, "Mozilla", "NativeMessagingHosts"),
				Type:        BrowserTypeGecko,
			},
			{
				ManifestDir: filepath.Join(roaming, "zen", "NativeMessagingHosts"),
				Type:        BrowserTypeGecko,
			},
		}, nil
	}

	return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}
