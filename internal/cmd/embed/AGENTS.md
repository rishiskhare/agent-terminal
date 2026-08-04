<!-- agent-terminal:start -->
# Agent Terminal

You are in the **Agent Terminal** side-panel PTY (a shell), not a browser page.
Live Google Chrome is the attach target (primary profile; visible tabs).
Never scratchpad Chrome or Chrome-for-Testing.
`open` / `goto` / `navigate` open a **tab** in the existing window.

## Use the Agent Terminal browser shim (shell)

Call the **shim** (not a bare `agent-browser` from Hermès/npm that may skip the gate):

```bash
"${AGENT_TERMINAL_BROWSER:-$HOME/.local/share/agent-terminal/bin/agent-browser}" open "<url-or-search-url>"
"${AGENT_TERMINAL_BROWSER:-$HOME/.local/share/agent-terminal/bin/agent-browser}" snapshot -i
```

Use that for open-web asks (“search for …”, “google …”, “look up …”, “find the docs for …”,
live facts) and for browse, forms, screenshots, and page extract.
**Do not** use Claude **Web Search**, **WebFetch**, web-search MCPs, or other browser MCPs
for those asks unless the user **names** that tool.

Known URL → open that URL; otherwise open a search URL, then `snapshot -i`.
Act on `@refs`; re-snapshot after navigation or DOM changes.
Workflow: `agent-browser skills get core --full` (skills may passthrough).

## Do not open Chrome

- **Local:** repo, files, symbols/identifiers, `node_modules`, “in this project/codebase”
  (e.g. “search for getUserById”) → local tools only
- **No retrieval:** definitions, reasoning, code explanation with no search/look-up ask
  → answer without Chrome
- **Ambiguous surface** (IDE vs Chrome settings/theme; “this page” with no URL) → ask once
- **Personal accounts** (email, calendar, banking, “my invoices”) → confirm before acting in Chrome

## Attach failure

If you see **Agent Terminal: cannot attach to your live Chrome**, or
**Auto-launch failed: No running Chrome instance found**, **stop**.
Do not retry in a loop and do not fall back to Web Search.

Tell the user (Chrome may already be open):

1. Click **Allow** if an “Allow remote debugging?” dialog is showing
2. Keep Google Chrome open with remote debugging on (`chrome://inspect/#remote-debugging`)
3. Run `agent-terminal doctor --fix` if it still fails

After a **Chrome restart**, one Allow click is expected.
<!-- agent-terminal:end -->
