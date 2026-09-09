import { createContext, use, useCallback, useEffect, useState, type ReactNode } from "react";
import { api, AUTH_EVENT, ApiError } from "../lib/api-client";
import { router } from "../router";
import { clearSessionHint, readSessionHint, writeSessionHint, type SessionHint } from "./session";

// Routes reachable without a session — never bounce a visitor already
// sitting on one of these back to /login (e.g. mid-reset-password link,
// or first-run bootstrap, both unauthenticated by design).
const PUBLIC_PATHS = ["/login", "/bootstrap", "/forgot-password", "/reset-password"];

function isOnPublicPath(): boolean {
  return PUBLIC_PATHS.some((p) => router.state.location.pathname.startsWith(p));
}

interface LoginResponse {
  organisation_id: string;
  user_id: string;
  idle_expires_at: string;
  absolute_expires_at: string;
}

interface AuthContextValue {
  session: SessionHint | null;
  login: (email: string, password: string, mfaCode?: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionHint | null>(() => readSessionHint());

  useEffect(() => {
    const onUnauthorized = () => {
      clearSessionHint();
      setSession(null);
      // Every 401 (an idle/absolute session timeout, a revoked session,
      // or any other reason a request came back unauthorized) used to
      // only clear local state here — nothing ever navigated the
      // visitor away from whatever protected page they were on, so the
      // page just sat there silently broken (every further request
      // failing) until they happened to click a link and router.tsx's
      // requireAuth beforeLoad caught it on that NEXT navigation. That
      // delayed, seemingly random redirect is what read as "the app
      // keeps logging me out" — this makes the redirect immediate,
      // right when the session actually becomes invalid.
      if (!isOnPublicPath()) void router.navigate({ to: "/login" });
    };
    window.addEventListener(AUTH_EVENT, onUnauthorized);
    return () => window.removeEventListener(AUTH_EVENT, onUnauthorized);
  }, []);

  const login = useCallback(async (email: string, password: string, mfaCode?: string) => {
    const res = await api.post<LoginResponse>("/auth/login", {
      email,
      password,
      mfa_code: mfaCode ?? "",
    });
    const hint: SessionHint = { organisationId: res.organisation_id, userId: res.user_id };
    writeSessionHint(hint);
    setSession(hint);
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.post("/auth/logout");
    } catch {
      // Best-effort — a network failure or an already-expired session
      // shouldn't block the user from clearing local state and landing
      // back on the login screen.
    }
    clearSessionHint();
    setSession(null);
    // AppShell's "Log out" button used to call only this function and
    // nothing else -- clearing session state with no navigation left
    // the same authenticated page on screen, looking exactly like the
    // click had done nothing (the bug this comment is fixing).
    void router.navigate({ to: "/login" });
  }, []);

  return <AuthContext value={{ session, login, logout }}>{children}</AuthContext>;
}

export function useAuth(): AuthContextValue {
  const ctx = use(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}

export function isUnauthorized(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401;
}
