# Windows fixes changelog

Feedback for upstream: [rishiskhare/agent-terminal](https://github.com/rishiskhare/agent-terminal).

These changes make the native messaging host install and connect on Windows (PowerShell and Git Bash). Without them, the extension side panel shows **Native host not connected**.

## 2026-08-08 — Theme embed path broken on Windows

### Symptom

After native host connects:  
`Internal error: failed to read theme file: open themes\Tomorrow Night.json: file does not exist`

### Root cause

`themeFs.ReadFile(filepath.Join("themes", theme+".json"))` uses OS path separators.  
`embed.FS` only accepts `/`, so Windows looks up `themes\Tomorrow Night.json` and fails.

### Fix

| Area | Change |
| --- | --- |
| `internal/cmd/serve.go` | Use `path.Join` (slash-separated) for embedded theme reads |
| `internal/cmd/themes_test.go` | Guard against regressions |

## 2026-08-08 — Windows native host support

### Root cause

`install` was effectively Unix-only:

1. `GetBrowsers()` returned `unsupported OS: windows`.
2. Chrome on Windows discovers native hosts via the **registry**, not only manifest files on disk.
3. The host launcher was a `#!/bin/sh` script, which Chrome cannot launch as a native messaging host on Windows.
4. `findExecPath(os.Args[0])` broke under Git Bash: `./agent-terminal` uses forward slashes, and `filepath.Base` on Windows does not treat `/` as a separator, so lookup fell through to `PATH` and failed.
5. `go build -o agent-terminal .` produces an extensionless binary; Windows `CreateProcess` looks for `*.exe`, so Chrome could not start the host.
6. Config/data paths used `$HOME/.config` / `$HOME/.local/share` instead of `%APPDATA%` / `%LOCALAPPDATA%`.
7. Default shell fell through to `/bin/sh`; Chrome user-data probing skipped Windows entirely.

### Fixes

| Area | Change |
| --- | --- |
| `internal/cmd/install.go` | Windows browser list; `.cmd` host wrapper; JSON paths with forward slashes; copy binary to `agent-terminal.exe` when needed; prefer `os.Executable()` for path resolution |
| `internal/cmd/install_windows.go` | Register `HKCU\Software\...\NativeMessagingHosts\com.agentterminal.native` for Chrome, Chromium, Edge, Brave, Vivaldi |
| `internal/cmd/install_other.go` | No-op registry helpers on non-Windows |
| `internal/cmd/embed/native_messaging_host.cmd.tmpl` | `@echo off` wrapper that runs `agent-terminal.exe serve` |
| `internal/cmd/paths.go` | Windows config/cache/data under AppData; shared `userHomeDir()` / `envOr()` helpers |
| `internal/cmd/root.go` | Windows default shell (Git Bash → PowerShell → `COMSPEC`); migrate prior XDG-style dirs |
| `internal/cmd/doctor.go` | Chrome `%LOCALAPPDATA%\Google\Chrome\User Data`; registry-aware native-host check; `agent-browser.cmd` shim |
| `internal/cmd/agent_app.go` | Windows `.cmd` agent launchers |
| `internal/cmd/serve.go` | Use `userHomeDir()` and Windows shim path |
| `README.md` | Windows build/install instructions |

### Verify

```powershell
go build -o agent-terminal.exe .
.\agent-terminal.exe install
.\agent-terminal.exe doctor
reg query "HKCU\Software\Google\Chrome\NativeMessagingHosts\com.agentterminal.native"
```

Then reload the unpacked extension, fully restart Chrome, and open the side panel.

### Suggested upstream PR title

`fix(windows): native messaging host install + registry registration`
