package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	agentsMDManagedStart = "<!-- agent-terminal:start -->"
	agentsMDManagedEnd   = "<!-- agent-terminal:end -->"
)

// canonicalAgentsMDPath is the doctor-owned source of truth under configDir.
func canonicalAgentsMDPath() string {
	return filepath.Join(configDir, "AGENTS.md")
}

func homeAgentsMDPath() string {
	return filepath.Join(os.Getenv("HOME"), "AGENTS.md")
}

func homeClaudeMDPath() string {
	return filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
}

// managedAgentsMDBody loads the shipped Markdown from embed/AGENTS.md.
// Content lives in the .md file; Go only orchestrates install/upsert.
func managedAgentsMDBody() (string, error) {
	return readEmbeddedMarkdown("embed/AGENTS.md")
}

func managedClaudeMDShim() (string, error) {
	return readEmbeddedMarkdown("embed/CLAUDE.md")
}

func readEmbeddedMarkdown(name string) (string, error) {
	data, err := embedFs.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	s := string(data)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if !hasManagedBlock(s) {
		return "", fmt.Errorf("%s: missing %s / %s markers", name, agentsMDManagedStart, agentsMDManagedEnd)
	}
	return s, nil
}

// materializeAgentsMD writes the canonical AGENTS.md and upserts ~/AGENTS.md + ~/CLAUDE.md.
// Returns checks describing what happened (ok/warn); never fatally errors on user-owned files.
func materializeAgentsMD() ([]DoctorCheck, error) {
	var checks []DoctorCheck

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, err
	}

	body, err := managedAgentsMDBody()
	if err != nil {
		return nil, err
	}
	shim, err := managedClaudeMDShim()
	if err != nil {
		return nil, err
	}

	canonical := canonicalAgentsMDPath()
	if err := writeFileIfChanged(canonical, body); err != nil {
		return nil, fmt.Errorf("write canonical AGENTS.md: %w", err)
	}
	checks = append(checks, DoctorCheck{
		ID:     "agentsMD.canonical",
		Label:  "AGENTS.md (canonical)",
		Status: "ok",
		Detail: canonical,
	})

	homeAgents := homeAgentsMDPath()
	c, err := upsertHomeAgentsMD(homeAgents, body)
	if err != nil {
		return checks, err
	}
	checks = append(checks, c)

	homeClaude := homeClaudeMDPath()
	c, err = upsertHomeClaudeMD(homeClaude, shim)
	if err != nil {
		return checks, err
	}
	checks = append(checks, c)

	return checks, nil
}

func upsertHomeAgentsMD(path, managedBody string) (DoctorCheck, error) {
	c := DoctorCheck{
		ID:     "agentsMD.home",
		Label:  "AGENTS.md (home)",
		Detail: path,
	}

	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(managedBody), 0644); err != nil {
			return c, err
		}
		c.Status = "ok"
		c.Detail = path + " — created"
		return c, nil
	}
	if err != nil {
		return c, err
	}

	// Our symlink to the canonical file: target was refreshed above.
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return c, err
		}
		resolved := filepath.Clean(target)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Clean(filepath.Join(filepath.Dir(path), target))
		}
		if resolved == filepath.Clean(canonicalAgentsMDPath()) {
			c.Status = "ok"
			c.Detail = path + " — symlink to canonical"
			return c, nil
		}
		c.Status = "warn"
		c.Detail = path + " — symlink to unexpected target; left unchanged"
		c.FixHint = "Point it at " + canonicalAgentsMDPath() + " or replace with a real AGENTS.md"
		return c, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	content := string(data)

	if hasManagedBlock(content) {
		updated, ok := replaceManagedBlock(content, managedBody)
		if !ok {
			c.Status = "warn"
			c.Detail = path + " — managed markers malformed; left unchanged"
			c.FixHint = "Fix or remove <!-- agent-terminal:start/end --> markers"
			return c, nil
		}
		if updated == content {
			c.Status = "ok"
			c.Detail = path + " — up to date"
			return c, nil
		}
		if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
			return c, err
		}
		c.Status = "ok"
		c.Detail = path + " — managed block refreshed"
		return c, nil
	}

	c.Status = "warn"
	c.Detail = path + " — exists without Agent Terminal markers; left unchanged"
	c.FixHint = "Add the managed block from " + canonicalAgentsMDPath() + " or merge browser rules manually"
	return c, nil
}

func upsertHomeClaudeMD(path, shim string) (DoctorCheck, error) {
	c := DoctorCheck{
		ID:     "agentsMD.claude",
		Label:  "CLAUDE.md (home)",
		Detail: path,
	}

	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(shim), 0644); err != nil {
			return c, err
		}
		c.Status = "ok"
		c.Detail = path + " — created (@AGENTS.md import)"
		return c, nil
	}
	if err != nil {
		return c, err
	}

	// Symlink to AGENTS.md is Anthropic's secondary pattern — leave it.
	if info.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(path)
		c.Status = "ok"
		c.Detail = path + " — symlink (" + target + "); left unchanged"
		return c, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	content := string(data)

	if hasManagedBlock(content) {
		updated, ok := replaceManagedBlock(content, shim)
		if !ok {
			c.Status = "warn"
			c.Detail = path + " — managed markers malformed; left unchanged"
			c.FixHint = "Fix or remove <!-- agent-terminal:start/end --> markers"
			return c, nil
		}
		if updated == content {
			c.Status = "ok"
			c.Detail = path + " — up to date"
			return c, nil
		}
		if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
			return c, err
		}
		c.Status = "ok"
		c.Detail = path + " — managed import block refreshed"
		return c, nil
	}

	if claudeImportsAgentsMD(content) {
		c.Status = "ok"
		c.Detail = path + " — already imports AGENTS.md"
		return c, nil
	}

	// Prepend managed shim; preserve user content below.
	updated := shim + "\n" + strings.TrimLeft(content, "\n")
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return c, err
	}
	c.Status = "ok"
	c.Detail = path + " — prepended @AGENTS.md import"
	return c, nil
}

func hasManagedBlock(content string) bool {
	return strings.Contains(content, agentsMDManagedStart) && strings.Contains(content, agentsMDManagedEnd)
}

func replaceManagedBlock(content, newBlock string) (string, bool) {
	start := strings.Index(content, agentsMDManagedStart)
	end := strings.Index(content, agentsMDManagedEnd)
	if start < 0 || end < 0 || end < start {
		return content, false
	}
	end += len(agentsMDManagedEnd)
	// The managed block owns one trailing newline after the end marker.
	if end < len(content) && content[end] == '\n' {
		end++
	}
	replacement := strings.TrimRight(newBlock, "\n") + "\n"
	return content[:start] + replacement + content[end:], true
}

var (
	agentsMDImportRe = regexp.MustCompile(`(?m)@AGENTS\.md\b`)
	mdFencedRe       = regexp.MustCompile("(?s)```.*?```")
	mdInlineCodeRe   = regexp.MustCompile("`[^`]*`")
)

// claudeImportsAgentsMD reports whether CLAUDE.md already has an @AGENTS.md import
// outside code spans/fences (same rule Claude uses).
func claudeImportsAgentsMD(content string) bool {
	return agentsMDImportRe.MatchString(stripMarkdownCode(content))
}

func stripMarkdownCode(s string) string {
	s = mdFencedRe.ReplaceAllString(s, "")
	return mdInlineCodeRe.ReplaceAllString(s, "")
}

// checkAgentsMD reports status without writing (doctor --fix=false).
func checkAgentsMD() []DoctorCheck {
	var checks []DoctorCheck

	canonical := canonicalAgentsMDPath()
	if _, err := os.Stat(canonical); err != nil {
		checks = append(checks, DoctorCheck{
			ID:      "agentsMD.canonical",
			Label:   "AGENTS.md (canonical)",
			Status:  "warn",
			Detail:  "Missing — run doctor --fix",
			FixHint: "Run: agent-terminal doctor --fix",
		})
	} else {
		checks = append(checks, DoctorCheck{
			ID:     "agentsMD.canonical",
			Label:  "AGENTS.md (canonical)",
			Status: "ok",
			Detail: canonical,
		})
	}

	homeAgents := homeAgentsMDPath()
	switch data, err := os.ReadFile(homeAgents); {
	case os.IsNotExist(err):
		checks = append(checks, DoctorCheck{
			ID:      "agentsMD.home",
			Label:   "AGENTS.md (home)",
			Status:  "warn",
			Detail:  "Missing — agents started from $HOME will not see Agent Terminal guidance",
			FixHint: "Run: agent-terminal doctor --fix",
		})
	case err != nil:
		checks = append(checks, DoctorCheck{
			ID:     "agentsMD.home",
			Label:  "AGENTS.md (home)",
			Status: "warn",
			Detail: err.Error(),
		})
	case hasManagedBlock(string(data)):
		checks = append(checks, DoctorCheck{
			ID:     "agentsMD.home",
			Label:  "AGENTS.md (home)",
			Status: "ok",
			Detail: homeAgents + " — managed block present",
		})
	default:
		checks = append(checks, DoctorCheck{
			ID:      "agentsMD.home",
			Label:   "AGENTS.md (home)",
			Status:  "warn",
			Detail:  homeAgents + " — present but not managed by Agent Terminal",
			FixHint: "Merge browser rules from " + canonical + " or add managed markers",
		})
	}

	homeClaude := homeClaudeMDPath()
	switch data, err := os.ReadFile(homeClaude); {
	case os.IsNotExist(err):
		checks = append(checks, DoctorCheck{
			ID:      "agentsMD.claude",
			Label:   "CLAUDE.md (home)",
			Status:  "warn",
			Detail:  "Missing — Claude Code will not import AGENTS.md",
			FixHint: "Run: agent-terminal doctor --fix",
		})
	case err != nil:
		checks = append(checks, DoctorCheck{
			ID:     "agentsMD.claude",
			Label:  "CLAUDE.md (home)",
			Status: "warn",
			Detail: err.Error(),
		})
	case claudeImportsAgentsMD(string(data)):
		checks = append(checks, DoctorCheck{
			ID:     "agentsMD.claude",
			Label:  "CLAUDE.md (home)",
			Status: "ok",
			Detail: homeClaude + " — imports @AGENTS.md",
		})
	default:
		checks = append(checks, DoctorCheck{
			ID:      "agentsMD.claude",
			Label:   "CLAUDE.md (home)",
			Status:  "warn",
			Detail:  homeClaude + " — no @AGENTS.md import",
			FixHint: "Add a line `@AGENTS.md` at the top (Claude reads CLAUDE.md, not AGENTS.md)",
		})
	}

	return checks
}

// writeFileIfChanged writes data only when the file is missing or contents differ.
func writeFileIfChanged(path, content string) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0644)
}
