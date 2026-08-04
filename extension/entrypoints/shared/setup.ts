const SETUP_COMMANDS = `go build -o agent-terminal .
./agent-terminal install`;

/** Minimal native-host disconnect screen: error + install commands + Retry. */
export function renderSetupScreen(errorMessage?: string): HTMLElement {
    const root = document.createElement("div");
    root.className = "at-setup";
    root.innerHTML = `
      <h1 class="at-setup__title">Native host not connected</h1>
      ${errorMessage ? `<p class="at-setup__error">${escapeHtml(errorMessage)}</p>` : ""}
      <pre class="at-setup__commands"><code>${SETUP_COMMANDS}</code></pre>
      <button type="button" class="at-btn at-btn--primary" data-action="retry">Retry</button>
    `;

    root.querySelector('[data-action="retry"]')?.addEventListener("click", () => {
        location.reload();
    });

    return root;
}

function escapeHtml(value: string): string {
    return value
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;");
}
