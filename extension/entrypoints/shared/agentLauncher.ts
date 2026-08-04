import { agentIconSVG } from "./agentIcons";
import { RequestConfigGet, ResponseConfigGet } from "./rpc";
import { CreateTTYOpts } from "./windowTabs";

const LAST_APP_KEY = "agent-terminal.lastApp";
const SHELL_ID = "";

/** Product agents in display order (only shown when present in apps/). */
const PRODUCT_ORDER = ["claude", "codex", "cursor-agent", "hermes", "openclaw"] as const;

const LABELS: Record<string, string> = {
    claude: "Claude",
    codex: "Codex",
    "cursor-agent": "Cursor",
    hermes: "Hermes",
    openclaw: "OpenClaw",
};

export type LauncherController = {
    setLaunchEnabled: (enabled: boolean) => void;
    refreshCatalog: () => Promise<void>;
    getCreateOpts: () => Promise<CreateTTYOpts>;
    rememberApp: (appId: string) => Promise<void>;
};

function labelFor(id: string): string {
    if (id === SHELL_ID) {
        return "Terminal";
    }
    return LABELS[id] || id;
}

function iconEl(id: string): HTMLElement {
    const wrap = document.createElement("span");
    wrap.className = "at-launcher__icon";
    wrap.innerHTML = agentIconSVG(id);
    return wrap;
}

export async function loadLastApp(): Promise<string> {
    const stored = await browser.storage.local.get<{ [LAST_APP_KEY]?: string }>(LAST_APP_KEY);
    const v = stored[LAST_APP_KEY];
    return typeof v === "string" ? v : SHELL_ID;
}

export async function saveLastApp(appId: string): Promise<void> {
    await browser.storage.local.set({ [LAST_APP_KEY]: appId });
}

export async function fetchAppNames(): Promise<string[]> {
    const resp = await browser.runtime.sendMessage<RequestConfigGet, ResponseConfigGet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.get",
        params: undefined,
    });
    if ("error" in resp || !Array.isArray(resp.result.apps)) {
        return [];
    }
    return resp.result.apps.filter((a) => typeof a === "string" && a && a !== "agent-browser");
}

/** Drop stale lastApp; shell is always valid. */
export function resolveLastApp(last: string, apps: string[]): string {
    if (!last || last === "terminal") {
        return SHELL_ID;
    }
    return apps.includes(last) ? last : SHELL_ID;
}

export function optsFromAppId(appId: string): CreateTTYOpts {
    if (!appId) {
        return { mode: "shell" };
    }
    return { mode: "app", app: appId };
}

/** Known product order, then any extra apps/, then Terminal. */
export function orderLauncherIds(apps: string[]): string[] {
    const known = PRODUCT_ORDER.filter((id) => apps.includes(id));
    const extras = apps
        .filter((id) => !(PRODUCT_ORDER as readonly string[]).includes(id))
        .sort();
    return [...known, ...extras, SHELL_ID];
}

export function mountAgentLauncher(
    host: HTMLElement,
    handlers: {
        onLaunch: (appId: string) => void;
        canLaunch: () => boolean;
    },
): LauncherController {
    host.classList.add("at-launcher");
    host.replaceChildren();

    let apps: string[] = [];
    let lastApp = SHELL_ID;
    let menuOpen = false;
    let launchEnabled = true;
    let menuEl: HTMLElement | null = null;
    let menuItems: HTMLButtonElement[] = [];
    let activeIndex = 0;

    const primary = document.createElement("button");
    primary.type = "button";
    primary.className = "at-launcher__primary";
    primary.title = "New terminal";
    primary.setAttribute("aria-label", "New terminal");

    const chevron = document.createElement("button");
    chevron.type = "button";
    chevron.className = "at-launcher__chevron";
    chevron.title = "Choose agent";
    chevron.setAttribute("aria-label", "Choose agent");
    chevron.setAttribute("aria-haspopup", "menu");
    chevron.setAttribute("aria-expanded", "false");
    chevron.innerHTML = `<svg viewBox="0 0 12 12" width="10" height="10" aria-hidden="true"><path d="M2.5 4.5L6 8l3.5-3.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`;

    const paintPrimary = () => {
        primary.replaceChildren(iconEl(lastApp));
        const plus = document.createElement("span");
        plus.className = "at-launcher__plus";
        plus.textContent = "+";
        primary.appendChild(plus);
        primary.title = `New ${labelFor(lastApp)}`;
        primary.setAttribute("aria-label", `New ${labelFor(lastApp)}`);
        primary.disabled = !launchEnabled;
    };

    const closeMenu = () => {
        if (!menuOpen) {
            return;
        }
        menuOpen = false;
        chevron.setAttribute("aria-expanded", "false");
        menuEl?.remove();
        menuEl = null;
        menuItems = [];
        document.removeEventListener("pointerdown", onDocPointer, true);
        document.removeEventListener("keydown", onMenuKey, true);
        chevron.focus({ preventScroll: true });
    };

    const onDocPointer = (e: PointerEvent) => {
        const t = e.target as Node;
        if (host.contains(t) || menuEl?.contains(t)) {
            return;
        }
        closeMenu();
    };

    const positionMenu = () => {
        if (!menuEl) {
            return;
        }
        const r = chevron.getBoundingClientRect();
        menuEl.style.width = "max-content";
        const cap = Math.min(280, window.innerWidth - 16);
        let mw = menuEl.offsetWidth;
        if (mw > cap) {
            mw = cap;
            menuEl.style.width = `${mw}px`;
        }
        let left = r.right - mw;
        if (left < 8) {
            left = 8;
        }
        const maxLeft = window.innerWidth - mw - 8;
        if (left > maxLeft) {
            left = Math.max(8, maxLeft);
        }
        menuEl.style.left = `${left}px`;
        menuEl.style.top = `${r.bottom + 4}px`;
    };

    const selectItem = async (appId: string) => {
        lastApp = appId;
        await saveLastApp(appId);
        paintPrimary();
        closeMenu();
        if (handlers.canLaunch()) {
            handlers.onLaunch(appId);
        }
    };

    const onMenuKey = (e: KeyboardEvent) => {
        if (!menuOpen) {
            return;
        }
        if (e.key === "Escape") {
            e.preventDefault();
            closeMenu();
            return;
        }
        if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            if (menuItems.length === 0) {
                return;
            }
            activeIndex = e.key === "ArrowDown"
                ? (activeIndex + 1) % menuItems.length
                : (activeIndex - 1 + menuItems.length) % menuItems.length;
            menuItems[activeIndex]?.focus();
            return;
        }
        if (e.key === "Enter" || e.key === " ") {
            const el = menuItems[activeIndex];
            if (el && !el.disabled) {
                e.preventDefault();
                el.click();
            }
        }
    };

    const openMenu = async () => {
        if (menuOpen) {
            closeMenu();
            return;
        }
        apps = await fetchAppNames();
        lastApp = resolveLastApp(await loadLastApp(), apps);
        await saveLastApp(lastApp);
        paintPrimary();

        menuEl = document.createElement("div");
        menuEl.className = "at-launcher__menu";
        menuEl.setAttribute("role", "menu");

        const ids = orderLauncherIds(apps);
        menuItems = [];
        ids.forEach((id, i) => {
            const item = document.createElement("button");
            item.type = "button";
            item.className = "at-launcher__item";
            item.setAttribute("role", "menuitem");
            item.appendChild(iconEl(id));
            const text = document.createElement("span");
            text.className = "at-launcher__item-label";
            text.textContent = labelFor(id);
            item.appendChild(text);
            if (id === lastApp) {
                const check = document.createElement("span");
                check.className = "at-launcher__check";
                check.textContent = "✓";
                item.appendChild(check);
            }
            item.addEventListener("click", () => void selectItem(id));
            menuEl!.appendChild(item);
            menuItems.push(item);
            if (id === lastApp) {
                activeIndex = i;
            }
        });

        document.body.appendChild(menuEl);
        positionMenu();
        menuOpen = true;
        chevron.setAttribute("aria-expanded", "true");
        document.addEventListener("pointerdown", onDocPointer, true);
        document.addEventListener("keydown", onMenuKey, true);
        window.addEventListener("resize", positionMenu, { once: true });
        menuItems[activeIndex]?.focus();
    };

    primary.addEventListener("click", () => {
        if (!launchEnabled || !handlers.canLaunch()) {
            return;
        }
        handlers.onLaunch(lastApp);
    });
    chevron.addEventListener("click", () => void openMenu());

    host.appendChild(primary);
    host.appendChild(chevron);
    paintPrimary();

    void (async () => {
        apps = await fetchAppNames();
        lastApp = resolveLastApp(await loadLastApp(), apps);
        await saveLastApp(lastApp);
        paintPrimary();
    })();

    return {
        setLaunchEnabled(enabled: boolean) {
            launchEnabled = enabled;
            primary.disabled = !enabled;
        },
        async refreshCatalog() {
            apps = await fetchAppNames();
            const next = resolveLastApp(await loadLastApp(), apps);
            if (next !== lastApp) {
                lastApp = next;
                await saveLastApp(lastApp);
            }
            paintPrimary();
        },
        async getCreateOpts() {
            apps = await fetchAppNames();
            lastApp = resolveLastApp(await loadLastApp(), apps);
            await saveLastApp(lastApp);
            paintPrimary();
            return optsFromAppId(lastApp);
        },
        async rememberApp(appId: string) {
            lastApp = resolveLastApp(appId, apps.length ? apps : await fetchAppNames());
            await saveLastApp(lastApp);
            paintPrimary();
        },
    };
}
