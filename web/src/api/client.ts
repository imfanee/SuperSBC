/**
 * Admin API client. Sessions live in httpOnly cookies; the CSRF token comes
 * from /auth/me (mirrored from the sbc_csrf cookie) and is sent on every
 * state-changing request. A 401 triggers one refresh attempt, then the
 * caller is sent to /login.
 */
export class ApiError extends Error {
  status: number;
  details?: Record<string, string>;
  body?: unknown;
  constructor(status: number, message: string, body?: unknown) {
    super(message);
    this.status = status;
    this.body = body;
    const b = body as { details?: Record<string, string> } | undefined;
    this.details = b?.details;
  }
}

let csrfToken = "";
let onUnauthorized: (() => void) | null = null;

export function setCsrfToken(t: string) {
  csrfToken = t;
}
export function setUnauthorizedHandler(f: () => void) {
  onUnauthorized = f;
}

const BASE = "/api/v1";

async function refresh(): Promise<boolean> {
  const r = await fetch(`${BASE}/auth/refresh`, { method: "POST", credentials: "same-origin" });
  if (!r.ok) return false;
  const j = (await r.json()) as { csrf_token?: string };
  if (j.csrf_token) csrfToken = j.csrf_token;
  return true;
}

export interface RequestOptions {
  method?: string;
  body?: unknown;
  query?: Record<string, string | number | boolean | undefined | null>;
  raw?: boolean;
  formData?: FormData;
  retry?: boolean;
}

export async function api<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method ?? "GET";
  const url = new URL(BASE + path, window.location.origin);
  if (opts.query) {
    for (const [k, v] of Object.entries(opts.query)) {
      if (v !== undefined && v !== null && v !== "") url.searchParams.set(k, String(v));
    }
  }
  const headers: Record<string, string> = {};
  if (method !== "GET" && method !== "HEAD") headers["X-CSRF-Token"] = csrfToken;
  let body: BodyInit | undefined;
  if (opts.formData) {
    body = opts.formData;
  } else if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.body);
  }
  const res = await fetch(url.toString(), { method, headers, body, credentials: "same-origin" });
  if (res.status === 401 && opts.retry !== false && !path.startsWith("/auth/login")) {
    if (await refresh()) return api<T>(path, { ...opts, retry: false });
    onUnauthorized?.();
  }
  if (opts.raw) {
    if (!res.ok) throw new ApiError(res.status, await res.text());
    return (await res.text()) as unknown as T;
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let json: unknown = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    json = { error: text };
  }
  if (!res.ok) {
    const msg = (json as { error?: string } | null)?.error ?? `HTTP ${res.status}`;
    throw new ApiError(res.status, msg, json);
  }
  return json as T;
}

export const get = <T>(path: string, query?: RequestOptions["query"]) => api<T>(path, { query });
export const post = <T>(path: string, body?: unknown, query?: RequestOptions["query"]) =>
  api<T>(path, { method: "POST", body, query });
export const put = <T>(path: string, body?: unknown) => api<T>(path, { method: "PUT", body });
export const del = <T>(path: string) => api<T>(path, { method: "DELETE" });

/** Downloads a CSV export through the session (blob + anchor). */
export async function download(path: string, filename: string, query?: RequestOptions["query"]) {
  const text = await api<string>(path, { query, raw: true });
  const blob = new Blob([text], { type: "text/csv" });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  a.click();
  URL.revokeObjectURL(a.href);
}
