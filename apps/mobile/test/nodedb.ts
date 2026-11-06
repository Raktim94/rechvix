import { DatabaseSync, type SQLInputValue } from "node:sqlite";
import type { Db, Param } from "../src/db/types";

/** The same Db interface, backed by Node's built-in SQLite, so tests exercise real SQL. */
export function memoryDb(): Db {
  const x = new DatabaseSync(":memory:");
  return {
    exec: async (sql) => { x.exec(sql); },
    run: async (sql, p: Param[] = []) => { x.prepare(sql).run(...(p as SQLInputValue[])); },
    all: async <T,>(sql: string, p: Param[] = []) => x.prepare(sql).all(...(p as SQLInputValue[])) as T[],
    first: async <T,>(sql: string, p: Param[] = []) => (x.prepare(sql).get(...(p as SQLInputValue[])) as T | undefined) ?? null,
    tx: async <T,>(fn: () => Promise<T>) => {
      x.exec("BEGIN");
      try { const r = await fn(); x.exec("COMMIT"); return r; } catch (e) { x.exec("ROLLBACK"); throw e; }
    },
  };
}
