import { ApiError, NetworkError, type Api } from "../api/client";
import type { Db } from "../db/types";
import { dueSales, getOrg, kvSet, mapGet, markState, resetSyncing, saveProgress } from "../db/repo";
import { pullMaster } from "./pull";
import { pushPayments } from "./payments";
import { pushItems, pushParties } from "./push";
import { NotReadyError, runSale, type Resolver } from "./saga";

export interface SyncResult { pushed: number; failed: number; pulled: boolean; offline: boolean; authRequired: boolean; message?: string; paymentsPosted?: number; paymentsFailed?: number }

const MAX_SERVER_ERROR_ATTEMPTS = 5;

/** Push waiting sales (oldest first), then refresh the catalogue. One run at a time; callers may call freely. */
export class SyncEngine {
  private running: Promise<SyncResult> | null = null;

  constructor(private readonly api: Api, private readonly db: Db, private readonly now: () => number = Date.now, private readonly onChange: () => void = () => {}) {}

  syncNow(): Promise<SyncResult> {
    if (this.running) return this.running;
    this.running = this.run().finally(() => { this.running = null; this.onChange(); });
    return this.running;
  }

  private async run(): Promise<SyncResult> {
    const res: SyncResult = { pushed: 0, failed: 0, pulled: false, offline: false, authRequired: false };
    await resetSyncing(this.db);
    this.onChange();

    let org = await getOrg(this.db);
    // First ever sync has nothing to push; pull first so there is an org to sell into.
    if (!org) {
      const ok = await this.pull(res);
      if (!ok) return res;
      org = await getOrg(this.db);
      if (!org) return res;
    }

    // Things made on the phone go up before the sales that use them.
    try {
      const a = await pushParties(this.api, this.db, org);
      const b = await pushItems(this.api, this.db, org);
      const problems = [...a.problems, ...b.problems];
      if (problems.length) res.message = `${problems[0]}${problems.length > 1 ? ` (+${problems.length - 1} more)` : ""}`;
    } catch (e) {
      if (!this.classify(e, res)) res.message = `Couldn't upload your items and customers: ${e instanceof Error ? e.message : "unknown error"}`;
      return res;
    }

    for (const sale of await dueSales(this.db)) {
      let resolver: Resolver;
      try { resolver = await this.resolver(sale.payload.customerId, sale.payload.lines.map((l) => l.variantId)); } catch { continue; }
      await markState(this.db, sale.local_id, "syncing");
      this.onChange();
      try {
        const out = await runSale(this.api, org, sale.local_id, sale.payload, sale.progress, (p) => saveProgress(this.db, sale.local_id, p), resolver);
        await saveProgress(this.db, sale.local_id, out.progress);
        await markState(this.db, sale.local_id, "synced", { docNumber: out.docNumber, total: out.total, syncedAt: this.now() });
        res.pushed++;
      } catch (e) {
        if (e instanceof NotReadyError) {
          await markState(this.db, sale.local_id, "pending", { error: e.message });
          continue;
        }
        if (e instanceof NetworkError) {
          await markState(this.db, sale.local_id, "pending");
          res.offline = true;
          return res;
        }
        if (e instanceof ApiError && e.status === 401) {
          await markState(this.db, sale.local_id, "pending");
          res.authRequired = true;
          return res;
        }
        const msg = e instanceof Error ? e.message : "Unknown error";
        const retryable = e instanceof ApiError && (e.status >= 500 || e.status === 408 || e.status === 429);
        const attempts = sale.attempts + 1;
        if (retryable && attempts < MAX_SERVER_ERROR_ATTEMPTS) {
          await markState(this.db, sale.local_id, "pending", { error: msg, attempts });
          res.message = "The server had a problem. Will retry.";
          return res;
        }
        await markState(this.db, sale.local_id, "failed", { error: msg, attempts });
        res.failed++;
      }
      this.onChange();
    }

    try {
      const pay = await pushPayments(this.api, this.db);
      res.paymentsPosted = pay.posted; res.paymentsFailed = pay.failed;
      res.failed += pay.failed;
    } catch (e) {
      if (!this.classify(e, res)) res.message = res.message ?? `Couldn't send payments: ${e instanceof Error ? e.message : "unknown error"}`;
      return res;
    }

    await this.pull(res);
    return res;
  }

  /** True when the error was a connectivity/auth problem and has been recorded on `res`. */
  private classify(e: unknown, res: SyncResult): boolean {
    if (e instanceof NetworkError) { res.offline = true; return true; }
    if (e instanceof ApiError && e.status === 401) { res.authRequired = true; return true; }
    return false;
  }

  /** Looks up the server ids for anything the sale mentions that was created on this phone. */
  private async resolver(customerId: string, variantIds: string[]): Promise<Resolver> {
    const party = new Map<string, string>(), item = new Map<string, { variantId: string; unitId: string }>();
    const c = await mapGet(this.db, "party", customerId);
    if (c) party.set(customerId, c.serverId);
    for (const v of variantIds) {
      const m = await mapGet(this.db, "item", v);
      if (m) item.set(v, { variantId: m.serverId, unitId: m.extra.unitId });
    }
    const isLocalParty = async (id: string) => !!(await this.db.first("SELECT 1 FROM parties WHERE id=? AND origin='local'", [id]));
    const localItems = new Set((await this.db.all<{ variant_id: string }>("SELECT variant_id FROM products WHERE origin='local'")).map((r) => r.variant_id));
    const localParty = await isLocalParty(customerId);
    return {
      party: (id) => party.get(id) ?? (localParty && id === customerId ? null : id),
      item: (id) => item.get(id) ?? (localItems.has(id) ? null : { variantId: id, unitId: "" }),
    };
  }

  private async pull(res: SyncResult): Promise<boolean> {
    try {
      await pullMaster(this.api, this.db, this.now);
      res.pulled = true;
      await kvSet(this.db, "last_sync_at", String(this.now()));
      return true;
    } catch (e) {
      if (e instanceof NetworkError) res.offline = true;
      else if (e instanceof ApiError && e.status === 401) res.authRequired = true;
      else res.message = e instanceof Error ? e.message : "Could not refresh data.";
      return false;
    }
  }
}
