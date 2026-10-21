import type { TFunction } from "i18next";
import type { NavItem } from "../nav";

/** Translated sidebar/palette label. The tax entry reads "GST / Tax" for
 * Indian companies and plain "Tax" everywhere else (i18next context `gst`). */
export function navLabel(t: TFunction, item: NavItem, countryCode?: string): string {
  const context = item.labelKey === "nav.item.tax" && countryCode === "IN" ? "gst" : undefined;
  return t(item.labelKey, { context });
}
