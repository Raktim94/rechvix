import NetInfo from "@react-native-community/netinfo";
import * as SecureStore from "expo-secure-store";
import React, { createContext, use, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppState } from "react-native";
import { Api, ApiError } from "../api/client";
import { openDb } from "../db/expo";
import {
  countByState, getOrg, getProfile, insertSale, kvDel, kvGet, kvSet, resetSyncing, setProfile, takeInvoiceNumber, createParty,
  DEFAULT_PROFILE, type Org, type Profile, type SalePayload, type SaleState,
} from "../db/repo";
import { migrate } from "../db/schema";
import type { Db } from "../db/types";
import { normalizeServer, probeServer } from "../lib/server";
import { SyncEngine, type SyncResult } from "../sync/engine";

/** boot: opening the database. welcome: first run, no business yet. ready: the app proper. */
export type Phase = "boot" | "welcome" | "ready";
/** While linking a server from inside the app: which step is on screen. */
export type Linking = null | "connect" | "login";
type Counts = Record<SaleState, number>;
const NO_COUNTS: Counts = { pending: 0, syncing: 0, synced: 0, failed: 0 };
const COOKIE_KEY = "rechvix.session";

export interface AppCtx {
  phase: Phase;
  db: Db | null;
  profile: Profile | null;
  org: Org | null;
  currency: string;
  businessName: string;
  /** True once a server address is set AND we hold a session. Until then the phone works entirely on its own. */
  linked: boolean;
  server: string | null;
  email: string | null;
  linking: Linking;
  online: boolean;
  syncing: boolean;
  counts: Counts;
  lastSyncAt: number | null;
  sessionExpired: boolean;
  lastSync: SyncResult | null;
  /** Bumped whenever local data changed, so screens re-query. */
  version: number;
  startLinking(): void;
  cancelLinking(): void;
  connect(raw: string): Promise<string | null>;
  login(email: string, password: string, mfa?: string): Promise<string | null>;
  logout(): Promise<string | null>;
  disconnect(): Promise<void>;
  syncNow(): Promise<SyncResult | null>;
  /** Call after any local edit: refreshes screens and, if linked, uploads in the background. */
  changed(): void;
  completeSetup(p: Profile): Promise<void>;
  saveProfile(p: Profile): Promise<void>;
  saveSale(payload: SalePayload): Promise<{ localId: string; number: string }>;
}

const Ctx = createContext<AppCtx | null>(null);
export const useApp = (): AppCtx => {
  const v = use(Ctx);
  if (!v) throw new Error("useApp outside provider");
  return v;
};

export const uid = (): string => {
  const b = Array.from({ length: 16 }, () => Math.floor(Math.random() * 256));
  b[6] = (b[6]! & 0x0f) | 0x40; b[8] = (b[8]! & 0x3f) | 0x80;
  const h = b.map((x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
};

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [phase, setPhase] = useState<Phase>("boot");
  const [db, setDb] = useState<Db | null>(null);
  const [profile, setProfileState] = useState<Profile | null>(null);
  const [org, setOrg] = useState<Org | null>(null);
  const [server, setServer] = useState<string | null>(null);
  const [hasCookie, setHasCookie] = useState(false);
  const [email, setEmail] = useState<string | null>(null);
  const [linking, setLinking] = useState<Linking>(null);
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
  const linkedRef = useRef(false);
  const linked = !!server && hasCookie;
  linkedRef.current = linked;

  const refresh = useCallback(async () => {
    const d = dbRef.current;
    if (!d) return;
    setCounts(await countByState(d));
    setOrg(await getOrg(d));
    setProfileState(await getProfile(d));
    const t = await kvGet(d, "last_sync_at");
    setLastSyncAt(t ? Number(t) : null);
    setVersion((v) => v + 1);
  }, []);

  const api = useMemo(
    () =>
      new Api({
        baseUrl: () => serverRef.current,
        cookie: () => cookieRef.current,
        onCookie: (c) => { cookieRef.current = c; setHasCookie(true); void SecureStore.setItemAsync(COOKIE_KEY, c); },
        onUnauthorized: () => { if (linkedRef.current) setSessionExpired(true); },
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
      setHasCookie(!!cookieRef.current);
      await refresh();
      setPhase((await getProfile(d)) ? "ready" : "welcome");
    })().catch(() => setPhase("welcome"));
    return () => { cancelled = true; };
  }, [api, refresh]);

  const syncNow = useCallback(async (): Promise<SyncResult | null> => {
    const eng = engineRef.current;
    if (!eng || !linkedRef.current) return null;
    setSyncing(true);
    try {
      const r = await eng.syncNow();
      setLastSync(r);
      if (r.authRequired) setSessionExpired(true);
      // First sync after linking: show the business as the server knows it.
      const d = dbRef.current!;
      const o = await getOrg(d), p = await getProfile(d);
      if (o && p && (p.currency !== o.currency || !p.name)) await setProfile(d, { ...p, currency: o.currency, country: o.country, gstState: p.gstState || o.gstStateCode, name: p.name || o.legalName });
      return r;
    } finally {
      setSyncing(false);
      await refresh();
    }
  }, [refresh]);

  // ---- when to sync (only ever when linked): coming online, app foregrounded, after sign-in, a gentle heartbeat while sales wait ----
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
    if (phase !== "ready" || !linked) return;
    void syncNow();
    const t = setInterval(() => { void syncNow(); }, 45_000);
    return () => clearInterval(t);
  }, [phase, linked, syncNow]);

  // ---- setup ----
  const completeSetup = useCallback(async (p: Profile) => {
    const d = dbRef.current!;
    await setProfile(d, p);
    // A shared "Walk-in Customer" so a cash sale never needs a name.
    const have = await d.first("SELECT 1 FROM parties WHERE is_walk_in=1");
    if (!have) await createParty(d, uid(), { name: "Walk-in Customer", phone: "", gstin: "", address: "" }, Date.now(), true);
    await refresh();
    setPhase("ready");
  }, [refresh]);

  const saveProfile = useCallback(async (p: Profile) => { await setProfile(dbRef.current!, p); await refresh(); }, [refresh]);

  // ---- linking a server (optional, any time) ----
  const startLinking = useCallback(() => setLinking(serverRef.current ? "login" : "connect"), []);
  const cancelLinking = useCallback(() => setLinking(null), []);

  const connect = useCallback(async (raw: string): Promise<string | null> => {
    const base = normalizeServer(raw);
    const problem = await probeServer(base);
    if (problem) return problem;
    serverRef.current = base;
    await kvSet(dbRef.current!, "server", base);
    setServer(base);
    setLinking("login");
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
    setLinking(null);
    linkedRef.current = true;
    setHasCookie(true);
    void syncNow(); // uploads everything made on the phone, then downloads the business's data
    return null;
  }, [api, syncNow]);

  const clearSession = useCallback(async () => {
    cookieRef.current = null;
    setHasCookie(false);
    await SecureStore.deleteItemAsync(COOKIE_KEY);
  }, []);

  const logout = useCallback(async (): Promise<string | null> => {
    const c = await countByState(dbRef.current!);
    if (c.pending + c.syncing + c.failed > 0) return "Some sales haven't reached the server yet. Sync them first, or they would be lost.";
    try { await api.post("/auth/logout"); } catch { /* best effort */ }
    await clearSession();
    setSessionExpired(false);
    return null;
  }, [api, clearSession]);

  /** Back to working on the phone alone. Nothing is deleted: items, customers and sales all stay. */
  const disconnect = useCallback(async () => {
    const d = dbRef.current!;
    try { await api.post("/auth/logout"); } catch { /* best effort */ }
    await clearSession();
    for (const k of ["server", "last_sync_at", "last_pull_at"]) await kvDel(d, k);
    serverRef.current = null;
    setServer(null);
    setSessionExpired(false);
    setLinking(null);
    await refresh();
  }, [api, clearSession, refresh]);

  const saveSale = useCallback(async (payload: SalePayload): Promise<{ localId: string; number: string }> => {
    const d = dbRef.current!;
    const localId = uid();
    const number = await takeInvoiceNumber(d, (await getProfile(d))?.invoicePrefix ?? DEFAULT_PROFILE.invoicePrefix);
    await insertSale(d, localId, payload, Date.now(), number);
    await refresh();
    void syncNow(); // no-op when standalone; in the background when linked. The sale is already safe on the phone.
    return { localId, number };
  }, [refresh, syncNow]);

  const changed = useCallback(() => { void refresh().then(() => syncNow()); }, [refresh, syncNow]);

  const value: AppCtx = {
    phase, db, profile, org,
    currency: org?.currency ?? profile?.currency ?? "INR",
    businessName: profile?.name || org?.legalName || "Rechvix",
    linked, server, email, linking, online, syncing, counts, lastSyncAt, sessionExpired, lastSync, version,
    startLinking, cancelLinking, connect, login, logout, disconnect, syncNow, changed, completeSetup, saveProfile, saveSale,
  };
  return <Ctx value={value}>{children}</Ctx>;
}
