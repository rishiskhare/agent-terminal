import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { AttachAddon } from "@xterm/addon-attach";
import { WebglAddon } from "@xterm/addon-webgl";
import { WebLinksAddon } from "@xterm/addon-web-links";
import {
    RequestAttachTTY,
    RequestCreateTTY,
    RequestDestroyTTY,
    RequestGetXtermConfig,
    RequestResizeTTY,
    ResponseAttachTTY,
    ResponseCreateTTY,
    ResponseGetXtermConfig,
} from "./rpc";
import { renderSetupScreen } from "./setup";

/** Flat per-window key — avoids RMW races on a shared map object. */
function sessionKey(windowId: number): string {
    return `agent-terminal.ttySession.${windowId}`;
}

async function resolveWindowId(): Promise<number> {
    const win = await browser.windows.getCurrent();
    if (win.id == null) {
        throw new Error("Could not resolve Chrome window id");
    }
    return win.id;
}

async function destroyTTY(id: string): Promise<void> {
    try {
        await browser.runtime.sendMessage<RequestDestroyTTY>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "tty.destroy",
            params: { id },
        });
    } catch {
        // Host may already be gone.
    }
}

async function createOrAttachTTY(): Promise<{ id: string; url: string }> {
    const searchParams = new URLSearchParams(window.location.search);

    // App launches always get a fresh session (not bound to the window map).
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

    const windowId = await resolveWindowId();
    const key = sessionKey(windowId);
    const stored = await browser.storage.session.get<{ [key: string]: string }>(key);
    const existingId = stored[key];

    if (existingId) {
        const attachResp = await browser.runtime.sendMessage<RequestAttachTTY, ResponseAttachTTY>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "tty.attach",
            params: { id: existingId },
        });

        if (!("error" in attachResp)) {
            return attachResp.result;
        }

        // Stale id (daemon restarted or session destroyed) — fall through to create.
        await browser.storage.session.remove(key);
    }

    const createResp = await browser.runtime.sendMessage<RequestCreateTTY, ResponseCreateTTY>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "tty.create",
        params: undefined,
    });

    if ("error" in createResp) {
        throw new Error(createResp.error.message);
    }

    // Window may have closed between create and persist — don't orphan the PTY.
    try {
        await browser.windows.get(windowId);
    } catch {
        await destroyTTY(createResp.result.id);
        throw new Error("Chrome window closed before the terminal could attach");
    }

    await browser.storage.session.set({ [key]: createResp.result.id });
    return createResp.result;
}

function isNativeHostError(message: string): boolean {
    return /native host is not connected/i.test(message);
}

function showSetup(message: string) {
    document.body.className = "at-setup-host";
    document.body.innerHTML = "";
    document.body.appendChild(renderSetupScreen(message));
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
            variant: window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
        }
    });

    if ("error" in xtermResp) {
        showSetup(xtermResp.error.message);
        return;
    }

    let tty: { id: string; url: string };
    try {
        tty = await createOrAttachTTY();
    } catch (err) {
        const message = (err as Error).message;
        console.error("Error creating/attaching TTY:", err);
        showSetup(message);
        return;
    }

    const terminal = new Terminal(xtermResp.result);

    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());

    terminal.open(anchor);
    terminal.onResize(async (size) => {
        const { cols, rows } = size;
        await browser.runtime.sendMessage<RequestResizeTTY>({
            jsonrpc: "2.0",
            method: "tty.resize",
            params: {
                tty: tty.id,
                cols,
                rows,
            },
        })
    });
    fitAddon.fit();

    // Open/fit → WebGL → then Attach (VS Code order).
    try {
        const webglAddon = new WebglAddon();
        webglAddon.onContextLoss(() => {
            webglAddon.dispose();
        });
        terminal.loadAddon(webglAddon);
    } catch {
        // Canvas renderer remains the default.
    }

    const ws = new WebSocket(tty.url);
    const attachAddon = new AttachAddon(ws);
    terminal.loadAddon(attachAddon);

    // Detach only — do not destroy the daemon session on panel/tab close.
    globalThis.onbeforeunload = () => {
        ws.onclose = () => { };
        ws.close();
    };

    globalThis.onresize = () => {
        fitAddon.fit();
    };

    terminal.onTitleChange((title) => {
        document.title = `${title}  |  Agent Terminal`
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
            params: { variant }
        });
        if ("error" in resp) {
            console.error("Error getting Xterm config:", resp.error);
            return;
        }

        terminal.options.theme = resp.result.theme
    });

    terminal.focus();
}

main();
