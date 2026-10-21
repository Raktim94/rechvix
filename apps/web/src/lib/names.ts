import { currentLanguage } from "../i18n";

/** Country and currency names in the UI language, from the browser's own data
 * (Intl.DisplayNames), falling back to the server's English name when the
 * runtime has none. Sorting uses the same language's collation. */
export function countryName(code: string, fallback: string): string {
  try {
    return new Intl.DisplayNames([currentLanguage()], { type: "region" }).of(code) ?? fallback;
  } catch {
    return fallback;
  }
}

export function currencyName(code: string, fallback: string): string {
  try {
    return new Intl.DisplayNames([currentLanguage()], { type: "currency" }).of(code) ?? fallback;
  } catch {
    return fallback;
  }
}

/** Sort entries by their localized display name. */
export function sortByName<T>(items: readonly T[], name: (x: T) => string): T[] {
  const collator = new Intl.Collator(currentLanguage());
  return [...items].sort((a, b) => collator.compare(name(a), name(b)));
}
