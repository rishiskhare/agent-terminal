export type JSONRPCRequest = {
    jsonrpc: string;
    id?: string;
    method: string;
    params: Record<string, any> | any[] | undefined;
}

type JSONRPCRequestBase<M extends string, P extends Record<string, any> | undefined = undefined> = {
    jsonrpc: "2.0";
    id?: string
    method: M;
    params?: P
}

export type RequestResizeTTY = JSONRPCRequestBase<"tty.resize", {
    tty: string;
    cols: number;
    rows: number;
}>

export type RequestCreateTTY = JSONRPCRequestBase<"tty.create", {
    mode?: "app";
    app?: string;
    args?: string[];
    cwd?: string;
}>

export type RequestAttachTTY = JSONRPCRequestBase<"tty.attach", {
    id: string;
}>

export type RequestListTTY = JSONRPCRequestBase<"tty.list", undefined>

export type RequestDestroyTTY = JSONRPCRequestBase<"tty.destroy", {
    id: string;
}>

export type RequestGetXtermConfig = JSONRPCRequestBase<"xterm.getConfig", {
    variant?: "light" | "dark";
}>;

export type RequestConfigGet = JSONRPCRequestBase<"config.get", undefined>;

export type RequestConfigSet = JSONRPCRequestBase<"config.set", {
    config: Record<string, unknown>;
}>;

export type RequestDoctorStatus = JSONRPCRequestBase<"doctor.status", undefined>;

export type RequestDoctorFix = JSONRPCRequestBase<"doctor.fix", undefined>;

export type DoctorCheck = {
    id: string;
    label: string;
    status: "ok" | "warn" | "error" | "info";
    detail: string;
    fixHint?: string;
};

export type DoctorStatusResult = {
    ok: boolean;
    level: "ok" | "warn" | "error";
    checks: DoctorCheck[];
    agents?: string[];
    message: string;
    config?: Record<string, unknown>;
};


type JSONRPCResponseBase<T extends Record<string, any> = Record<string, any>> = {
    jsonrpc: "2.0";
    id: string;
    result: T;
} | {
    jsonrpc: "2.0";
    id: string;
    error: {
        code: number;
        message: string;
        data?: Record<string, any>;
    }
}

export type JSONRPCResponse = {
    jsonrpc: "2.0";
    id: string;
    result?: Record<string, any>;
    error?: {
        code: number;
        message: string;
        data?: Record<string, any>;
    };
}

export type ResponseGetXtermConfig = JSONRPCResponseBase

export type ResponseCreateTTY = JSONRPCResponseBase<{
    id: string;
    url: string;
}>

export type ResponseAttachTTY = ResponseCreateTTY

export type ResponseListTTY = JSONRPCResponseBase<{
    sessions: { id: string }[];
}>

export type ResponseConfigGet = JSONRPCResponseBase<{
    config: Record<string, unknown>;
    themes: string[];
    apps: string[];
    path: string;
}>

export type ResponseConfigSet = JSONRPCResponseBase<{
    config: Record<string, unknown>;
}>

export type ResponseDoctorStatus = JSONRPCResponseBase<DoctorStatusResult>
export type ResponseDoctorFix = ResponseDoctorStatus