import { ApiError, NetworkError, type Api } from "../api/client";
import type { Db } from "../db/types";
import { dueSales, getOrg, kvSet, markState, resetSyncing, saveProgress } from "../db/repo";
import { pullMaster } from "./pull";
import { runSale } from "./saga";

export interface SyncResult { pushed: number; failed: number; pulled: boolean; offline: boolean; authRequired: boolean; message?: string }

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

    for (const sale of await dueSales(this.db)) {
      await markState(this.db, sale.local_id, "syncing");
      this.onChange();
      try {
        const out = await runSale(this.api, org, sale.local_id, sale.payload, sale.progress, (p) => saveProgress(this.db, sale.local_id, p));
        await saveProgress(this.db, sale.local_id, out.progress);
        await markState(this.db, sale.local_id, "synced", { docNumber: out.docNumber, total: out.total, syncedAt: this.now() });
        res.pushed++;
      } catch (e) {
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

    await this.pull(res);
    return res;
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
