/** Typed fetch wrapper for apps/server's REST API (/api/v1).
 *
 *  Auth: the server issues a session cookie on /auth/login. On a phone we keep that cookie ourselves (SecureStore) and send
 *  it as a header rather than trusting the OS cookie jar: a `Secure` cookie would be dropped on a plain-http shop-LAN
 *  install, and an explicit copy survives app restarts and works the same on iOS and Android. */

export class ApiError extends Error {
  constructor(readonly status: number, readonly code: string, message: string, readonly details?: Record<string, unknown>) {
    super(message);
    this.name = "ApiError";
  }
}

/** The request never got an answer: offline, DNS, timeout, server down. Always safe to retry later. */
export class NetworkError extends Error {
  constructor(message = "No connection to the server.") {
    super(message);
    this.name = "NetworkError";
  }
}

export interface ApiOptions {
  baseUrl: () => string | null;
  cookie: () => string | null;
  onCookie: (cookie: string) => void;
  onUnauthorized: () => void;
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
  cookieName?: string;
}

interface ErrorEnvelope { error: { code: string; message: string; details?: Record<string, unknown> } }
const isEnvelope = (v: unknown): v is ErrorEnvelope => typeof v === "object" && v !== null && "error" in v;

export class Api {
  constructor(private readonly o: ApiOptions) {}

  get<T>(path: string): Promise<T> { return this.request<T>("GET", path); }
  post<T>(path: string, body?: unknown): Promise<T> { return this.request<T>("POST", path, body ?? {}); }

  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const base = this.o.baseUrl();
    if (!base) throw new NetworkError("No server address is set.");
    const f = this.o.fetchImpl ?? fetch;
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), this.o.timeoutMs ?? 20000);
    const headers: Record<string, string> = { Accept: "application/json" };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const cookie = this.o.cookie();
    if (cookie) headers.Cookie = cookie;

    let res: Response;
    try {
      res = await f(`${base}/api/v1${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: ctl.signal, credentials: "omit" });
    } catch {
      throw new NetworkError();
    } finally {
      clearTimeout(timer);
    }

    const set = res.headers.get("set-cookie");
    if (set) {
      const name = this.o.cookieName ?? "bp_session";
      const m = new RegExp(`${name}=[^;,\\s]*`).exec(set);
      if (m) this.o.onCookie(m[0]);
    }

    if (res.status === 204) return undefined as T;
    const text = await res.text();
    let parsed: unknown;
    try { parsed = text ? JSON.parse(text) : undefined; } catch { parsed = undefined; }

    if (!res.ok) {
      if (res.status === 401) this.o.onUnauthorized();
      if (isEnvelope(parsed)) throw new ApiError(res.status, parsed.error.code, parsed.error.message, parsed.error.details);
      throw new ApiError(res.status, "UNKNOWN_ERROR", `The server answered ${res.status}.`);
    }
    return parsed as T;
  }
}
