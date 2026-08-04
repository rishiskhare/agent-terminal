<!-- agent-terminal:start -->
# Agent Terminal

## Browser automation

- Prefer **`agent-browser`** for any browser, web, Google, page, or form task.
  Live Chrome attach is already configured (PATH shim → `browser-gate`).
  Attach targets your **primary Google Chrome** user-data-dir — never a Claude
  `/tmp/.../scratchpad` Chrome or Chrome-for-Testing.
- Do: `agent-browser open <url>` → `agent-browser snapshot -i` → act on `@refs` →
  re-snapshot after any navigation or DOM change.
  (`open` / `goto` / `navigate` are rewritten to open a **tab** in your existing
  Chrome window — not a separate window or Chrome-for-Testing.)
- For the full workflow guide: `agent-browser skills get core --full`
- Never use `browser-use`, `bu`, Claude-in-Chrome, or a separate Chrome-for-Testing
  instance unless the user explicitly asks for that tool.
- If `agent-browser` fails with an **Agent Terminal: cannot attach to your live Chrome**
  error, **stop**. Tell the user to keep Chrome open, enable
  `chrome://inspect/#remote-debugging`, click **Allow** once, then run
  `agent-terminal doctor --fix`. Do not invent alternate browsers or `--profile` workarounds.
- After a **Chrome restart**, one “Allow remote debugging?” click is expected. Do not
  reconnect in a loop — that stacks more Allow dialogs.
<!-- agent-terminal:end -->
