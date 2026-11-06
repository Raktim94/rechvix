import * as SQLite from "expo-sqlite";
import type { Db, Param } from "./types";

/** expo-sqlite adapter. The app opens one connection for its whole life. */
export async function openDb(name = "rechvix.db"): Promise<Db> {
  const x = await SQLite.openDatabaseAsync(name);
  await x.execAsync("PRAGMA journal_mode = WAL;");
  return {
    exec: (sql) => x.execAsync(sql),
    run: async (sql, p: Param[] = []) => { await x.runAsync(sql, p); },
    all: <T,>(sql: string, p: Param[] = []) => x.getAllAsync<T>(sql, p),
    first: <T,>(sql: string, p: Param[] = []) => x.getFirstAsync<T>(sql, p),
    tx: async <T,>(fn: () => Promise<T>) => {
      let out!: T;
      await x.withTransactionAsync(async () => { out = await fn(); });
      return out;
    },
  };
}
