import type { Db, Param } from "../db/types";

/** Every table that holds the business's own records. (server_balances and the sync bookkeeping are caches the server rebuilds.) */
export const BACKUP_TABLES = ["products", "parties", "sales", "id_map", "payments", "sale_revisions", "reminders", "quotes", "credit_notes", "item_low"] as const;
/** Settings and counters. The server link ("server") is never part of a backup: restoring must not silently re-link a phone. */
const KV_SKIP = new Set(["server"]);

export interface BackupFile {
  app: "rechvix-mobile";
  /** 2 = every table; 1 (older app versions) had only kv, products, parties, sales and id_map, and no payments. */
  format: 1 | 2;
  /** The database version the file was made at. A file from a newer app cannot be loaded by an older one. */
  schema: number;
  exportedAt: string;
  kv: { key: string; value: string }[];
  tables: Record<string, Record<string, Param>[]>;
}

export class BackupError extends Error {}

const schemaVersion = async (db: Db): Promise<number> => (await db.first<{ user_version: number }>("PRAGMA user_version"))?.user_version ?? 0;

/** The whole phone as data: every record, including payments, quotations, credit notes and edit history. */
export async function buildBackup(db: Db, now = new Date()): Promise<BackupFile> {
  const tables: BackupFile["tables"] = {};
  for (const t of BACKUP_TABLES) tables[t] = await db.all<Record<string, Param>>(`SELECT * FROM ${t}`);
  return {
    app: "rechvix-mobile", format: 2, schema: await schemaVersion(db), exportedAt: now.toISOString(),
    kv: (await db.all<{ key: string; value: string }>("SELECT key, value FROM kv")).filter((r) => !KV_SKIP.has(r.key)), tables,
  };
}

/** Checks a file before anything is touched: it must be ours, not from a newer app, and shaped as expected. Returns what it holds. */
export function inspectBackup(text: string): { file: BackupFile; counts: Record<string, number> } {
  let raw: unknown;
  try { raw = JSON.parse(text); } catch { throw new BackupError("This is not a Rechvix backup file."); }
  const o = raw as Partial<BackupFile> & { products?: unknown; parties?: unknown; sales?: unknown; id_map?: unknown };
  if (!o || o.app !== "rechvix-mobile") throw new BackupError("This is not a Rechvix backup file.");
  // Format 1 kept the tables at the top level.
  const legacy = o.format === undefined;
  const tables: BackupFile["tables"] = legacy ? { products: o.products as never, parties: o.parties as never, sales: o.sales as never, id_map: o.id_map as never } : (o.tables ?? {});
  if (!Array.isArray(o.kv)) throw new BackupError("The backup file is damaged (settings are missing).");
  for (const [name, rows] of Object.entries(tables)) {
    if (!(BACKUP_TABLES as readonly string[]).includes(name)) throw new BackupError(`The backup file has an unknown table "${name}".`);
    if (!Array.isArray(rows)) throw new BackupError(`The backup file is damaged (${name}).`);
  }
  const file: BackupFile = { app: "rechvix-mobile", format: legacy ? 1 : 2, schema: legacy ? 3 : Number(o.schema), exportedAt: String(o.exportedAt ?? ""), kv: o.kv as BackupFile["kv"], tables };
  if (!Number.isInteger(file.schema)) throw new BackupError("The backup file is damaged (version is missing).");
  const counts: Record<string, number> = {};
  for (const [name, rows] of Object.entries(tables)) counts[name] = rows.length;
  return { file, counts };
}

/** Replaces everything on this phone with the backup, all or nothing: if anything is wrong the phone is left exactly as it was.
 *  Table and column names come from the phone's own schema, never from the file, so a crafted file cannot run its own SQL. */
export async function restoreBackup(db: Db, text: string): Promise<Record<string, number>> {
  const { file, counts } = inspectBackup(text);
  const have = await schemaVersion(db);
  if (file.schema > have) throw new BackupError("This backup was made by a newer version of the app. Update the app, then restore it.");
  await db.tx(async () => {
    for (const t of BACKUP_TABLES) {
      const rows = file.tables[t];
      // An old-format file never held payments and the like: leave those tables as they are rather than wipe them.
      if (rows === undefined && file.format === 1) continue;
      if (rows === undefined && !tableAbsentInOldFile(t, file)) throw new BackupError(`The backup file is missing "${t}".`);
      const known = new Set((await db.all<{ name: string }>(`PRAGMA table_info(${t})`)).map((c) => c.name));
      await db.run(`DELETE FROM ${t}`);
      for (const row of rows ?? []) {
        const cols = Object.keys(row);
        for (const c of cols) if (!known.has(c)) throw new BackupError(`The backup file has a field "${c}" this app does not know (${t}).`);
        if (cols.length === 0) continue;
        await db.run(`INSERT INTO ${t}(${cols.join(",")}) VALUES(${cols.map(() => "?").join(",")})`, cols.map((c) => row[c] as Param));
      }
    }
    // Settings and counters: replaced, except the server link, which stays as this phone has it.
    await db.run("DELETE FROM kv WHERE key != 'server'");
    for (const r of file.kv) if (!KV_SKIP.has(r.key)) await db.run("INSERT INTO kv(key,value) VALUES(?,?)", [r.key, r.value]);
  });
  return counts;
}

/** Tables added after a file's schema version are legitimately absent from it (quotes and credit notes arrived in v4, item_low in v5). */
const tableAbsentInOldFile = (t: string, file: BackupFile): boolean =>
  (t === "quotes" || t === "credit_notes") ? file.schema < 4 : t === "item_low" ? file.schema < 5 : (t === "payments" || t === "sale_revisions" || t === "reminders") ? file.schema < 3 : false;
