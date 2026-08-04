import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { AttachAddon } from "@xterm/addon-attach";
import { WebglAddon } from "@xterm/addon-webgl";
import { WebLinksAddon } from "@xterm/addon-web-links";
import {
    RequestAttachTTY,
    RequestCreateTTY,
    RequestGetXtermConfig,
    RequestResizeTTY,
    ResponseAttachTTY,
    ResponseCreateTTY,
    ResponseGetXtermConfig,
} from "./rpc";

const SESSION_STORAGE_KEY = "tweety.ttySessionId";

async function createOrAttachTTY(): Promise<{ id: string; url: string }> {
    const searchParams = new URLSearchParams(window.location.search);

    // App launches always get a fresh session.
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

    const stored = await browser.storage.session.get<{ [SESSION_STORAGE_KEY]?: string }>(SESSION_STORAGE_KEY);
    const existingId = stored[SESSION_STORAGE_KEY];

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
        await browser.storage.session.remove(SESSION_STORAGE_KEY);
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

    await browser.storage.session.set({ [SESSION_STORAGE_KEY]: createResp.result.id });
    return createResp.result;
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
    })

    if ("error" in xtermResp) {
        console.error("Error getting Xterm config:", xtermResp.error);
        return;
    }

    let tty: { id: string; url: string };
    try {
        tty = await createOrAttachTTY();
    } catch (err) {
        console.error("Error creating/attaching TTY:", err);
        globalThis.document.body.innerHTML = `<h1>Error: ${(err as Error).message}</h1>`;
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

    // Open/fit → WebGL → then Attach, so scrollback/TUI bytes do not hit the
    // DOM renderer then force glyph-atlas warm-up under load (VS Code order).
    // IdleTaskQueue "deadline exceeded" warns are benign xterm diagnostics;
    // do not disable WebGL for them (@xterm/xterm@5.5 hardcodes console.warn).
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
        document.title = `${title}  |  Tweety`
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
