<!-- agent-terminal:start -->
# Agent Terminal

You are in the **Agent Terminal** side-panel PTY (a shell), not a browser page.
`agent-browser` attaches to the user's **live Google Chrome** (primary profile;
visible tabs). Never scratchpad Chrome or Chrome-for-Testing.
`open` / `goto` / `navigate` open a **tab** in the existing window.

## Use `agent-browser` (shell)

For open-web asks like **“search for …”**, **“google …”**, **“look up …”**,
**“find the docs for …”**, or live facts (weather, prices, “latest …”), run
`agent-browser` in the shell. **Do not** use Claude **Web Search**, **WebFetch**,
web-search MCPs, or other browser MCPs for those asks.

Also use `agent-browser` for browse, navigate, forms, screenshots, and page extract.
Known URL → open that URL; otherwise open a search URL, then `snapshot -i`:

```bash
agent-browser open "<url-or-search-url>"
agent-browser snapshot -i
```

Act on `@refs`; re-snapshot after navigation or DOM changes.
Workflow: `agent-browser skills get core --full`

**Do not** use Web Search, WebFetch, web-search MCPs, `chrome-devtools-mcp`,
Playwright MCP, `browser-use` / `bu`, Claude-in-Chrome, or Chrome-for-Testing
for those tasks unless the user **names** that tool.

## Do not open Chrome

- **Local:** repo, files, symbols/identifiers, `node_modules`, “in this project/codebase”
  (e.g. “search for getUserById”) → local tools only
- **No retrieval:** definitions, reasoning, code explanation with no search/look-up ask
  → answer without Chrome
- **Ambiguous surface** (IDE vs Chrome settings/theme; “this page” with no URL) → ask once
- **Personal accounts** (email, calendar, banking, “my invoices”) → confirm before acting in Chrome

## Attach failure

If you see **Agent Terminal: cannot attach to your live Chrome**, **stop**.
Tell the user: keep Chrome open, enable `chrome://inspect/#remote-debugging`,
click **Allow** once, run `agent-terminal doctor --fix`.
No `--profile` workarounds and **no** falling back to Web Search or other tools.

After a **Chrome restart**, one Allow click is expected. Do not reconnect in a loop.
<!-- agent-terminal:end -->
