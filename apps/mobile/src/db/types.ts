/** The tiny slice of SQLite the app needs. expo-sqlite implements it on the phone (db/expo.ts);
 *  node:sqlite implements it in the unit tests, so the sync logic runs against a real SQL engine. */
export type Param = string | number | null;

export interface Db {
  exec(sql: string): Promise<void>;
  run(sql: string, params?: Param[]): Promise<void>;
  all<T>(sql: string, params?: Param[]): Promise<T[]>;
  first<T>(sql: string, params?: Param[]): Promise<T | null>;
  tx<T>(fn: () => Promise<T>): Promise<T>;
}
