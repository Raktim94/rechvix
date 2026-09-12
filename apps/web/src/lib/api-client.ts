/**
 * Typed fetch wrapper for apps/server's REST API.
 *
 * Auth: the backend issues a Secure/HttpOnly/SameSite session cookie
 * (internal/modules/identity, Stage 2) — this client never reads or
 * stores that token itself, it just sends `credentials: "include"` on
 * every request so the browser attaches the cookie automatically. There
 * is no bearer-token header anywhere in this file, on purpose.
 *
 * Errors: every non-2xx response is expected to carry
 * `internal/platform/http`'s error envelope,
 * `{"error":{"code","message","details","request_id"}}` — parsed into
 * `ApiError` below so callers can branch on `.code` (e.g. "MFA_REQUIRED")
 * without string-matching a human-readable message.
 */

const API_BASE = import.meta.env.VITE_API_BASE_URL ?? "/api/v1";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, unknown> | undefined;
  readonly requestId: string | undefined;

  constructor(status: number, code: string, message: string, details?: Record<string, unknown>, requestId?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
    this.requestId = requestId;
  }
}

interface ErrorEnvelope {
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
    request_id?: string;
  };
}

function isErrorEnvelope(v: unknown): v is ErrorEnvelope {
  return typeof v === "object" && v !== null && "error" in v;
}

/** Fired whenever a request comes back 401 — the app-level auth state
 * listens for this to redirect to /login, rather than every single
 * call site needing to handle it individually. */
export const AUTH_EVENT = "rechvix:unauthorized";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    credentials: "include",
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  const body: unknown = text ? JSON.parse(text) : undefined;

  if (!res.ok) {
    if (res.status === 401) {
      window.dispatchEvent(new CustomEvent(AUTH_EVENT));
    }
    if (isErrorEnvelope(body)) {
      throw new ApiError(res.status, body.error.code, body.error.message, body.error.details, body.error.request_id);
    }
    throw new ApiError(res.status, "UNKNOWN_ERROR", `Request failed with status ${res.status}`);
  }

  return body as T;
}

/** Builds a full same-origin URL for a report/export download link
 * (`<a href>`, not `fetch`) — the session cookie rides along on a normal
 * browser navigation the same way it does on every `fetch` above, no
 * separate auth handling needed. */
export function apiUrl(path: string): string {
  return `${API_BASE}${path}`;
}

export const api = {
  get: <T>(path: string) => request<T>(path, { method: "GET" }),
  post: <T>(path: string, data?: unknown) =>
    request<T>(path, { method: "POST", body: data !== undefined ? JSON.stringify(data) : undefined }),
  put: <T>(path: string, data?: unknown) =>
    request<T>(path, { method: "PUT", body: data !== undefined ? JSON.stringify(data) : undefined }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
  /** Uploads a raw CSV/XLSX file as the request body (internal/platform/
   * importer's ParseCSV/ParseXLSX read straight from r.Body — no
   * multipart wrapper, no base64) for a bulk-import endpoint. The
   * explicit Content-Type header below overrides `request`'s normal
   * "any body means application/json" default, since `init.headers` is
   * spread after it. */
  uploadFile: <T>(path: string, file: File, format: "csv" | "xlsx", dryRun: boolean, extraQuery?: Record<string, string>) => {
    const params = new URLSearchParams({ format, dry_run: String(dryRun), ...extraQuery });
    return request<T>(`${path}?${params.toString()}`, {
      method: "POST",
      headers: { "Content-Type": format === "csv" ? "text/csv" : "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" },
      body: file,
    });
  },
  /** Same raw-body-upload idea as uploadFile, for an endpoint that
   * doesn't care about format/dry_run query params (e.g. backup
   * restore/inspect, which just reads whatever bytes are in the body). */
  uploadRaw: <T>(path: string, file: File | Blob) => request<T>(path, { method: "POST", body: file }),
  /** GETs a `{[key]: T[]}`-shaped list endpoint and coalesces the array —
   * Go's `map[string]any{key: list}` marshals a nil slice as JSON `null`,
   * not `[]`, whenever a listing is genuinely empty, so every list
   * screen would otherwise crash on `.length`/`.map` the first time a
   * fresh organisation has zero rows. Use this instead of a bare `get`
   * for any endpoint shaped like `{"products": [...]}`. */
  getListField: async <T>(path: string, key: string): Promise<T[]> => {
    const res = await request<Record<string, T[] | null>>(path, { method: "GET" });
    return res[key] ?? [];
  },
};
