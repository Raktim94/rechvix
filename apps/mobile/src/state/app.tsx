import NetInfo from "@react-native-community/netinfo";
import * as SecureStore from "expo-secure-store";
import React, { createContext, use, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppState } from "react-native";
import { Api, ApiError } from "../api/client";
import { openDb } from "../db/expo";
import { countByState, getOrg, insertSale, kvDel, kvGet, kvSet, resetSyncing, type Org, type SalePayload, type SaleState } from "../db/repo";
import { migrate } from "../db/schema";
import type { Db } from "../db/types";
import { normalizeServer, probeServer } from "../lib/server";
import { SyncEngine, type SyncResult } from "../sync/engine";

export type Phase = "boot" | "connect" | "login" | "ready";
type Counts = Record<SaleState, number>;
const NO_COUNTS: Counts = { pending: 0, syncing: 0, synced: 0, failed: 0 };
const COOKIE_KEY = "rechvix.session";

export interface AppCtx {
  phase: Phase;
  db: Db | null;
  org: Org | null;
  server: string | null;
  email: string | null;
  online: boolean;
  syncing: boolean;
  counts: Counts;
  lastSyncAt: number | null;
  sessionExpired: boolean;
  lastSync: SyncResult | null;
  /** Bumped whenever local data changed, so screens re-query. */
  version: number;
  connect(raw: string): Promise<string | null>;
  login(email: string, password: string, mfa?: string): Promise<string | null>;
  logout(): Promise<string | null>;
  forgetServer(): Promise<string | null>;
  syncNow(): Promise<SyncResult | null>;
  saveSale(payload: SalePayload): Promise<string>;
}

const Ctx = createContext<AppCtx | null>(null);
export const useApp = (): AppCtx => {
  const v = use(Ctx);
  if (!v) throw new Error("useApp outside provider");
  return v;
};

const uid = (): string => {
  const b = Array.from({ length: 16 }, () => Math.floor(Math.random() * 256));
  b[6] = (b[6]! & 0x0f) | 0x40; b[8] = (b[8]! & 0x3f) | 0x80;
  const h = b.map((x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
};

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [phase, setPhase] = useState<Phase>("boot");
  const [db, setDb] = useState<Db | null>(null);
  const [org, setOrg] = useState<Org | null>(null);
  const [server, setServer] = useState<string | null>(null);
  const [email, setEmail] = useState<string | null>(null);
  const [online, setOnline] = useState(true);
  const [syncing, setSyncing] = useState(false);
  const [counts, setCounts] = useState<Counts>(NO_COUNTS);
  const [lastSyncAt, setLastSyncAt] = useState<number | null>(null);
  const [lastSync, setLastSync] = useState<SyncResult | null>(null);
  const [sessionExpired, setSessionExpired] = useState(false);
  const [version, setVersion] = useState(0);

  const serverRef = useRef<string | null>(null);
  const cookieRef = useRef<string | null>(null);
  const dbRef = useRef<Db | null>(null);
  const engineRef = useRef<SyncEngine | null>(null);
  const phaseRef = useRef<Phase>("boot");
  phaseRef.current = phase;

  const refresh = useCallback(async () => {
    const d = dbRef.current;
    if (!d) return;
    setCounts(await countByState(d));
    setOrg(await getOrg(d));
    const t = await kvGet(d, "last_sync_at");
    setLastSyncAt(t ? Number(t) : null);
    setVersion((v) => v + 1);
  }, []);

  const api = useMemo(
    () =>
      new Api({
        baseUrl: () => serverRef.current,
        cookie: () => cookieRef.current,
        onCookie: (c) => { cookieRef.current = c; void SecureStore.setItemAsync(COOKIE_KEY, c); },
        onUnauthorized: () => { if (phaseRef.current === "ready") { setSessionExpired(true); } },
      }),
    [],
  );

  // ---- boot ----
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const d = await openDb();
      await migrate(d);
      await resetSyncing(d);
      if (cancelled) return;
      dbRef.current = d;
      setDb(d);
      engineRef.current = new SyncEngine(api, d, Date.now, () => void refresh());
      serverRef.current = await kvGet(d, "server");
      setServer(serverRef.current);
      setEmail(await kvGet(d, "email"));
      cookieRef.current = await SecureStore.getItemAsync(COOKIE_KEY);
      await refresh();
      setPhase(!serverRef.current ? "connect" : !cookieRef.current ? "login" : "ready");
    })().catch(() => setPhase("connect"));
    return () => { cancelled = true; };
  }, [api, refresh]);

  const syncNow = useCallback(async (): Promise<SyncResult | null> => {
    const eng = engineRef.current;
    if (!eng || phaseRef.current !== "ready") return null;
    setSyncing(true);
    try {
      const r = await eng.syncNow();
      setLastSync(r);
      if (r.authRequired) setSessionExpired(true);
      return r;
    } finally {
      setSyncing(false);
      await refresh();
    }
  }, [refresh]);

  // ---- when to sync: coming online, app foregrounded, after sign-in, and a gentle heartbeat while sales wait ----
  useEffect(() => NetInfo.addEventListener((s) => {
    const up = !!s.isConnected && s.isInternetReachable !== false;
    setOnline(up);
    if (up) void syncNow();
  }), [syncNow]);
  useEffect(() => {
    const sub = AppState.addEventListener("change", (s) => { if (s === "active") void syncNow(); });
    return () => sub.remove();
  }, [syncNow]);
  useEffect(() => {
    if (phase !== "ready") return;
    void syncNow();
    const t = setInterval(() => { if (counts.pending > 0 || counts.syncing > 0) void syncNow(); }, 45_000);
    return () => clearInterval(t);
  }, [phase, syncNow, counts.pending, counts.syncing]);

  const connect = useCallback(async (raw: string): Promise<string | null> => {
    const base = normalizeServer(raw);
    const problem = await probeServer(base);
    if (problem) return problem;
    serverRef.current = base;
    await kvSet(dbRef.current!, "server", base);
    setServer(base);
    setPhase(cookieRef.current ? "ready" : "login");
    return null;
  }, []);

  const login = useCallback(async (mail: string, password: string, mfa?: string): Promise<string | null> => {
    try {
      await api.post("/auth/login", { email: mail.trim(), password, mfa_code: mfa ?? "" });
    } catch (e) {
      if (e instanceof ApiError) return e.code === "MFA_REQUIRED" ? "MFA_REQUIRED" : e.message;
      return "Can't reach the server. Check your connection.";
    }
    await kvSet(dbRef.current!, "email", mail.trim());
    setEmail(mail.trim());
    setSessionExpired(false);
    setPhase("ready");
    return null;
  }, [api]);

  const clearSession = useCallback(async () => {
    cookieRef.current = null;
    await SecureStore.deleteItemAsync(COOKIE_KEY);
  }, []);

  const logout = useCallback(async (): Promise<string | null> => {
    const c = await countByState(dbRef.current!);
    if (c.pending + c.syncing + c.failed > 0) return "Some sales haven't reached the server yet. Sync them first, or they would be lost.";
    try { await api.post("/auth/logout"); } catch { /* best effort */ }
    await clearSession();
    setSessionExpired(false);
    setPhase("login");
    return null;
  }, [api, clearSession]);

  const forgetServer = useCallback(async (): Promise<string | null> => {
    const d = dbRef.current!;
    const c = await countByState(d);
    if (c.pending + c.syncing + c.failed > 0) return "Some sales haven't reached the server yet. Sync them first, or they would be lost.";
    await clearSession();
    for (const t of ["products", "parties", "server_docs", "sales"]) await d.run(`DELETE FROM ${t}`);
    for (const k of ["server", "org", "last_sync_at", "last_pull_at"]) await kvDel(d, k);
    serverRef.current = null;
    setServer(null);
    setSessionExpired(false);
    await refresh();
    setPhase("connect");
    return null;
  }, [clearSession, refresh]);

  const saveSale = useCallback(async (payload: SalePayload): Promise<string> => {
    const id = uid();
    await insertSale(dbRef.current!, id, payload, Date.now());
    await refresh();
    void syncNow(); // in the background; the sale is already safe on the phone
    return id;
  }, [refresh, syncNow]);

  const value: AppCtx = { phase, db, org, server, email, online, syncing, counts, lastSyncAt, sessionExpired, lastSync, version, connect, login, logout, forgetServer, syncNow, saveSale };
  return <Ctx value={value}>{children}</Ctx>;
}
