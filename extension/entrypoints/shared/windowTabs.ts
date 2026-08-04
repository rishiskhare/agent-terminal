import {
    RequestAttachTTY,
    RequestCreateTTY,
    RequestDestroyTTY,
    ResponseAttachTTY,
    ResponseCreateTTY,
} from "./rpc";

export const MAX_TABS = 8;

export type WindowTabState = {
    tabs: string[];
    activeId: string;
};

export function windowStateKey(windowId: number): string {
    return `agent-terminal.window.${windowId}`;
}

export function legacySessionKey(windowId: number): string {
    return `agent-terminal.ttySession.${windowId}`;
}

export async function resolveWindowId(): Promise<number> {
    const win = await browser.windows.getCurrent();
    if (win.id == null) {
        throw new Error("Could not resolve Chrome window id");
    }
    return win.id;
}

export async function loadWindowState(windowId: number): Promise<WindowTabState | null> {
    const key = windowStateKey(windowId);
    const stored = await browser.storage.session.get<{ [key: string]: WindowTabState }>(key);
    const state = stored[key];
    if (!state || !Array.isArray(state.tabs) || typeof state.activeId !== "string") {
        return null;
    }
    return state;
}

export async function saveWindowState(windowId: number, state: WindowTabState): Promise<void> {
    await browser.storage.session.set({ [windowStateKey(windowId)]: state });
}

export async function clearWindowState(windowId: number): Promise<void> {
    await browser.storage.session.remove(windowStateKey(windowId));
}

export async function destroyTTY(id: string): Promise<void> {
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

export async function createTTY(): Promise<{ id: string; url: string }> {
    const resp = await browser.runtime.sendMessage<RequestCreateTTY, ResponseCreateTTY>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "tty.create",
        params: undefined,
    });
    if ("error" in resp) {
        throw new Error(resp.error.message);
    }
    return resp.result;
}

export async function attachTTY(id: string): Promise<{ id: string; url: string } | null> {
    const resp = await browser.runtime.sendMessage<RequestAttachTTY, ResponseAttachTTY>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "tty.attach",
        params: { id },
    });
    if ("error" in resp) {
        return null;
    }
    return resp.result;
}

/** Destroy orphan from pre-tabs single-session key, if any. */
export async function reclaimLegacySession(windowId: number): Promise<void> {
    const key = legacySessionKey(windowId);
    const stored = await browser.storage.session.get<{ [key: string]: string }>(key);
    const id = stored[key];
    if (!id) {
        return;
    }
    await browser.storage.session.remove(key);
    await destroyTTY(id);
}

export async function ensureWindowAlive(windowId: number, ttyId: string): Promise<void> {
    try {
        await browser.windows.get(windowId);
    } catch {
        await destroyTTY(ttyId);
        throw new Error("Chrome window closed before the terminal could attach");
    }
}

export function shortTabLabel(id: string, title?: string): string {
    const t = title?.trim();
    if (t) {
        return t.length > 16 ? `${t.slice(0, 15)}…` : t;
    }
    return id.slice(0, 8);
}

/** Drop a dead id; pick a new activeId. Returns null if tabs empty. */
export function pruneTab(state: WindowTabState, deadId: string): WindowTabState | null {
    const tabs = state.tabs.filter((id) => id !== deadId);
    if (tabs.length === 0) {
        return null;
    }
    const activeId = state.activeId === deadId
        ? tabs[Math.max(0, state.tabs.indexOf(deadId) - 1)] ?? tabs[0]
        : state.activeId;
    const stillActive = tabs.includes(activeId) ? activeId : tabs[0];
    return { tabs, activeId: stillActive };
}
