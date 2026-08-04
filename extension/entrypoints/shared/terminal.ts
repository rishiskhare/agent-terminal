import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { AttachAddon } from "@xterm/addon-attach";
import { WebglAddon } from "@xterm/addon-webgl";
import { WebLinksAddon } from "@xterm/addon-web-links";
import {
    RequestCreateTTY,
    RequestGetXtermConfig,
    RequestResizeTTY,
    ResponseCreateTTY,
    ResponseGetXtermConfig,
} from "./rpc";
import { renderSetupScreen } from "./setup";
import {
    LauncherController,
    mountAgentLauncher,
    optsFromAppId,
} from "./agentLauncher";
import {
    MAX_TABS,
    WindowTabState,
    attachTTY,
    clearWindowState,
    createTTY,
    destroyTTY,
    ensureWindowAlive,
    loadWindowState,
    pruneTab,
    reclaimLegacySession,
    resolveWindowId,
    saveWindowState,
    shortTabLabel,
} from "./windowTabs";

function isSidePanelPage(): boolean {
    const path = location.pathname;
    return path.endsWith("sidepanel.html") || path.includes("/sidepanel");
}

function isEphemeralLaunch(): boolean {
    return new URLSearchParams(location.search).has("app") || !isSidePanelPage();
}

async function createEphemeralTTY(): Promise<{ id: string; url: string }> {
    const searchParams = new URLSearchParams(window.location.search);
    if (searchParams.has("app")) {
        const resp = await browser.runtime.sendMessage<RequestCreateTTY, ResponseCreateTTY>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "tty.create",
            params: {
                mode: "app",
                app: searchParams.get("app")!,
                args: searchParams.getAll("arg"),
                cwd: searchParams.get("cwd") || undefined,
            },
        });
        if ("error" in resp) {
            throw new Error(resp.error.message);
        }
        return resp.result;
    }
    return createTTY({ mode: "shell" });
}

function showSetup(message: string) {
    document.body.className = "at-setup-host";
    document.body.innerHTML = "";
    document.body.appendChild(renderSetupScreen(message));
}

/** Rebuild tab list only — launcher host is a stable sibling. */
function renderTabList(
    list: HTMLElement,
    state: WindowTabState,
    titles: Map<string, string>,
    handlers: {
        onSelect: (id: string) => void;
        onClose: (id: string) => void;
    },
) {
    list.replaceChildren();

    for (const id of state.tabs) {
        const tab = document.createElement("button");
        tab.type = "button";
        tab.className = "at-tabs__tab" + (id === state.activeId ? " at-tabs__tab--active" : "");
        tab.title = titles.get(id) || id;

        const label = document.createElement("span");
        label.className = "at-tabs__label";
        label.textContent = shortTabLabel(id, titles.get(id));
        tab.appendChild(label);

        const close = document.createElement("span");
        close.className = "at-tabs__close";
        close.textContent = "×";
        close.title = "Close terminal";
        close.addEventListener("click", (e) => {
            e.stopPropagation();
            handlers.onClose(id);
        });
        tab.appendChild(close);

        tab.addEventListener("click", () => handlers.onSelect(id));
        list.appendChild(tab);
    }
}

async function main() {
    const anchor = document.getElementById("terminal");
    if (!anchor) {
        console.error("terminal element not found");
        return;
    }

    const xtermResp = await browser.runtime.sendMessage<RequestGetXtermConfig, ResponseGetXtermConfig>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "xterm.getConfig",
        params: {
            variant: window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light",
        },
    });

    if ("error" in xtermResp) {
        showSetup(xtermResp.error.message);
        return;
    }

    // Ephemeral: terminal.html / popup / ?app= — no strip, no window state.
    if (isEphemeralLaunch()) {
        let tty: { id: string; url: string };
        try {
            tty = await createEphemeralTTY();
        } catch (err) {
            showSetup((err as Error).message);
            return;
        }
        await runSingleTerminal(anchor, xtermResp.result, tty);
        return;
    }

    const strip = document.getElementById("tab-strip");
    if (!strip) {
        showSetup("Tab strip missing from side panel");
        return;
    }

    let windowId: number;
    try {
        windowId = await resolveWindowId();
    } catch (err) {
        showSetup((err as Error).message);
        return;
    }

    await reclaimLegacySession(windowId);

    strip.hidden = false;
    strip.classList.add("at-tabs");
    strip.replaceChildren();

    const list = document.createElement("div");
    list.className = "at-tabs__list";
    strip.appendChild(list);

    const launcherHost = document.createElement("div");
    strip.appendChild(launcherHost);

    let launcher!: LauncherController;
    launcher = mountAgentLauncher(launcherHost, {
        onLaunch: (appId) => void newTab(appId),
        canLaunch: () => !!state && state.tabs.length < MAX_TABS,
    });

    let state = await loadWindowState(windowId);
    if (!state || state.tabs.length === 0) {
        const created = await createWithFallback();
        await ensureWindowAlive(windowId, created.id);
        state = { tabs: [created.id], activeId: created.id };
        await saveWindowState(windowId, state);
    }

    const titles = new Map<string, string>();
    let switchGen = 0;
    let activeTtyId = state.activeId;
    let ws: WebSocket | null = null;
    let attachAddon: AttachAddon | null = null;

    const terminal = new Terminal(xtermResp.result);
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(anchor);

    terminal.onResize(async (size) => {
        if (!activeTtyId) {
            return;
        }
        const { cols, rows } = size;
        await browser.runtime.sendMessage<RequestResizeTTY>({
            jsonrpc: "2.0",
            method: "tty.resize",
            params: { tty: activeTtyId, cols, rows },
        });
    });

    try {
        const webglAddon = new WebglAddon();
        webglAddon.onContextLoss(() => {
            webglAddon.dispose();
        });
        terminal.loadAddon(webglAddon);
    } catch {
        // Canvas renderer remains the default.
    }

    const refreshStrip = () => {
        renderTabList(list, state!, titles, {
            onSelect: (id) => void switchTo(id),
            onClose: (id) => void closeTab(id),
        });
        launcher.setLaunchEnabled(state!.tabs.length < MAX_TABS);
    };

    async function createWithFallback(preferred?: string) {
        try {
            if (preferred !== undefined) {
                await launcher.rememberApp(preferred);
                return await createTTY(optsFromAppId(preferred));
            }
            const opts = await launcher.getCreateOpts();
            return await createTTY(opts);
        } catch {
            await launcher.rememberApp("");
            return await createTTY({ mode: "shell" });
        }
    }

    const detachSocket = () => {
        if (attachAddon) {
            try {
                attachAddon.dispose();
            } catch {
                // already disposed
            }
            attachAddon = null;
        }
        if (ws) {
            ws.onmessage = null;
            ws.onerror = null;
            ws.onclose = null;
            try {
                ws.close();
            } catch {
                // ignore
            }
            ws = null;
        }
    };

    const clearTerminalBuffer = () => {
        terminal.reset();
    };

    async function attachActive(gen: number): Promise<boolean> {
        if (gen !== switchGen || !state) {
            return false;
        }

        let endpoint = await attachTTY(state.activeId);
        if (!endpoint) {
            const pruned = pruneTab(state, state.activeId);
            if (!pruned) {
                // Recreate with shell — no per-tab app metadata.
                const created = await createTTY({ mode: "shell" });
                if (gen !== switchGen) {
                    await destroyTTY(created.id);
                    return false;
                }
                await ensureWindowAlive(windowId, created.id);
                state = { tabs: [created.id], activeId: created.id };
            } else {
                state = pruned;
            }
            await saveWindowState(windowId, state);
            refreshStrip();
            endpoint = await attachTTY(state.activeId);
            if (!endpoint) {
                const created = await createTTY({ mode: "shell" });
                if (gen !== switchGen) {
                    await destroyTTY(created.id);
                    return false;
                }
                await ensureWindowAlive(windowId, created.id);
                state = { tabs: [created.id], activeId: created.id };
                await saveWindowState(windowId, state);
                refreshStrip();
                endpoint = await attachTTY(state.activeId);
                if (!endpoint) {
                    throw new Error("Could not attach to terminal session");
                }
            }
        }

        if (gen !== switchGen) {
            return false;
        }

        detachSocket();
        clearTerminalBuffer();

        activeTtyId = endpoint.id;
        ws = new WebSocket(endpoint.url);
        attachAddon = new AttachAddon(ws);
        terminal.loadAddon(attachAddon);

        fitAddon.fit();
        const dims = { cols: terminal.cols, rows: terminal.rows };
        await browser.runtime.sendMessage<RequestResizeTTY>({
            jsonrpc: "2.0",
            method: "tty.resize",
            params: { tty: activeTtyId, cols: dims.cols, rows: dims.rows },
        });

        if (gen !== switchGen) {
            return false;
        }
        terminal.focus();
        return true;
    }

    async function switchTo(id: string) {
        if (!state || id === state.activeId) {
            return;
        }
        if (!state.tabs.includes(id)) {
            return;
        }
        const gen = ++switchGen;
        state = { ...state, activeId: id };
        await saveWindowState(windowId, state);
        refreshStrip();
        try {
            await attachActive(gen);
        } catch (err) {
            if (gen === switchGen) {
                console.error("switch failed:", err);
                showSetup((err as Error).message);
            }
        }
    }

    async function newTab(appId?: string) {
        if (!state || state.tabs.length >= MAX_TABS) {
            return;
        }
        const gen = ++switchGen;
        let created: { id: string; url: string };
        try {
            created = await createWithFallback(appId);
            await ensureWindowAlive(windowId, created.id);
        } catch (err) {
            showSetup((err as Error).message);
            return;
        }
        if (gen !== switchGen) {
            await destroyTTY(created.id);
            return;
        }
        state = {
            tabs: [...state.tabs, created.id],
            activeId: created.id,
        };
        await saveWindowState(windowId, state);
        refreshStrip();
        try {
            await attachActive(gen);
        } catch (err) {
            if (gen === switchGen) {
                showSetup((err as Error).message);
            }
        }
    }

    async function closeTab(id: string) {
        if (!state) {
            return;
        }

        if (id !== state.activeId) {
            await destroyTTY(id);
            titles.delete(id);
            const next = pruneTab(state, id);
            if (!next) {
                return;
            }
            state = next;
            await saveWindowState(windowId, state);
            refreshStrip();
            return;
        }

        const idx = state.tabs.indexOf(id);
        const neighbor = state.tabs[idx + 1] ?? state.tabs[idx - 1];

        if (!neighbor) {
            const gen = ++switchGen;
            await destroyTTY(id);
            titles.delete(id);
            let created: { id: string; url: string };
            try {
                created = await createWithFallback();
                await ensureWindowAlive(windowId, created.id);
            } catch (err) {
                await clearWindowState(windowId);
                showSetup((err as Error).message);
                return;
            }
            if (gen !== switchGen) {
                await destroyTTY(created.id);
                return;
            }
            state = { tabs: [created.id], activeId: created.id };
            await saveWindowState(windowId, state);
            refreshStrip();
            try {
                await attachActive(gen);
            } catch (err) {
                if (gen === switchGen) {
                    showSetup((err as Error).message);
                }
            }
            return;
        }

        const gen = ++switchGen;
        state = { tabs: state.tabs.filter((t) => t !== id), activeId: neighbor };
        await saveWindowState(windowId, state);
        refreshStrip();
        try {
            const ok = await attachActive(gen);
            if (ok && gen === switchGen) {
                await destroyTTY(id);
                titles.delete(id);
            }
        } catch (err) {
            if (gen === switchGen) {
                showSetup((err as Error).message);
            }
        }
    }

    terminal.onTitleChange((title) => {
        if (activeTtyId) {
            titles.set(activeTtyId, title);
            document.title = `${title}  |  Agent Terminal`;
            refreshStrip();
        }
    });

    globalThis.onbeforeunload = () => {
        detachSocket();
    };

    globalThis.onresize = () => {
        fitAddon.fit();
    };

    globalThis.onfocus = () => {
        // Don't steal focus from the agent menu.
        if (document.querySelector(".at-launcher__menu")) {
            return;
        }
        terminal.focus();
    };

    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", async (event) => {
        const variant = event.matches ? "dark" : "light";
        const resp = await browser.runtime.sendMessage<RequestGetXtermConfig, ResponseGetXtermConfig>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "xterm.getConfig",
            params: { variant },
        });
        if ("error" in resp) {
            return;
        }
        terminal.options.theme = resp.result.theme;
    });

    refreshStrip();
    const gen = ++switchGen;
    try {
        await attachActive(gen);
    } catch (err) {
        showSetup((err as Error).message);
    }
}

async function runSingleTerminal(
    anchor: HTMLElement,
    xtermConfig: Record<string, unknown>,
    tty: { id: string; url: string },
) {
    let activeTtyId = tty.id;
    const terminal = new Terminal(xtermConfig);
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(anchor);

    terminal.onResize(async (size) => {
        const { cols, rows } = size;
        await browser.runtime.sendMessage<RequestResizeTTY>({
            jsonrpc: "2.0",
            method: "tty.resize",
            params: { tty: activeTtyId, cols, rows },
        });
    });
    fitAddon.fit();

    try {
        const webglAddon = new WebglAddon();
        webglAddon.onContextLoss(() => {
            webglAddon.dispose();
        });
        terminal.loadAddon(webglAddon);
    } catch {
        // Canvas fallback.
    }

    const ws = new WebSocket(tty.url);
    const attachAddon = new AttachAddon(ws);
    terminal.loadAddon(attachAddon);

    globalThis.onbeforeunload = () => {
        ws.onclose = () => { };
        ws.close();
    };
    globalThis.onresize = () => {
        fitAddon.fit();
    };
    terminal.onTitleChange((title) => {
        document.title = `${title}  |  Agent Terminal`;
    });
    globalThis.onfocus = () => {
        terminal.focus();
    };
    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", async (event) => {
        const variant = event.matches ? "dark" : "light";
        const resp = await browser.runtime.sendMessage<RequestGetXtermConfig, ResponseGetXtermConfig>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "xterm.getConfig",
            params: { variant },
        });
        if ("error" in resp) {
            return;
        }
        terminal.options.theme = resp.result.theme;
    });
    terminal.focus();
}

void main();
