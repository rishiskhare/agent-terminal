import {
    RequestConfigGet,
    RequestConfigSet,
    RequestDoctorFix,
    ResponseConfigGet,
    ResponseConfigSet,
    ResponseDoctorFix,
} from "./rpc";

const form = document.getElementById("settings-form") as HTMLFormElement;
const statusEl = document.getElementById("status") as HTMLDivElement;
const configPathEl = document.getElementById("config-path") as HTMLParagraphElement;
const commandEl = document.getElementById("command") as HTMLInputElement;
const argsEl = document.getElementById("args") as HTMLInputElement;
const envEl = document.getElementById("env") as HTMLTextAreaElement;

function setStatus(message: string, kind: "ok" | "error" | "" = "") {
    statusEl.hidden = !message;
    statusEl.textContent = message;
    statusEl.className = "at-status" + (kind ? ` at-status--${kind}` : "");
}

function envToText(env: unknown): string {
    if (!env || typeof env !== "object") {
        return "";
    }
    return Object.entries(env as Record<string, unknown>)
        .map(([k, v]) => `${k}=${String(v ?? "")}`)
        .join("\n");
}

function textToEnv(text: string): Record<string, string> {
    const out: Record<string, string> = {};
    for (const line of text.split("\n")) {
        const trimmed = line.trim();
        if (!trimmed || trimmed.startsWith("#")) {
            continue;
        }
        const eq = trimmed.indexOf("=");
        if (eq <= 0) {
            continue;
        }
        out[trimmed.slice(0, eq)] = trimmed.slice(eq + 1);
    }
    return out;
}

function parseArgs(raw: string): string[] {
    return raw
        .trim()
        .split(/\s+/)
        .filter(Boolean);
}

async function load() {
    setStatus("Loading…");
    const resp = await browser.runtime.sendMessage<RequestConfigGet, ResponseConfigGet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.get",
        params: undefined,
    });

    if ("error" in resp) {
        setStatus(resp.error.message, "error");
        return;
    }

    const { config, path } = resp.result;
    configPathEl.textContent = path;

    commandEl.value = String(config.command ?? "");
    const args = Array.isArray(config.args) ? (config.args as string[]) : [];
    argsEl.value = args.join(" ");
    envEl.value = envToText(config.env);
    setStatus("Loaded.", "ok");
}

async function save(event: Event) {
    event.preventDefault();
    setStatus("Saving…");

    const config: Record<string, unknown> = {
        command: commandEl.value.trim() || undefined,
        args: parseArgs(argsEl.value),
        env: textToEnv(envEl.value),
    };

    const resp = await browser.runtime.sendMessage<RequestConfigSet, ResponseConfigSet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.set",
        params: { config },
    });

    if ("error" in resp) {
        setStatus(resp.error.message, "error");
        return;
    }

    setStatus("Saved. Open a new side-panel session to apply.", "ok");
}

async function runDoctor() {
    setStatus("Running doctor…");
    const resp = await browser.runtime.sendMessage<RequestDoctorFix, ResponseDoctorFix>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "doctor.fix",
        params: undefined,
    });
    if ("error" in resp) {
        setStatus(resp.error.message, "error");
        return;
    }
    setStatus(resp.result.message, resp.result.ok ? "ok" : "error");
    await load();
}

form.addEventListener("submit", (e) => void save(e));
document.getElementById("run-doctor")?.addEventListener("click", () => void runDoctor());

void load();
