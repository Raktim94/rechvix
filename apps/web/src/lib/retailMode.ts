import { useSyncExternalStore } from "react";
import { NAV_GROUPS, type NavGroup } from "../nav";

const KEY = "rechvix.retailMode";

/** Routes a counter-billing shop rarely needs. They stay reachable by URL;
 * Retail Mode only declutters the sidebar and the command palette. */
const RETAIL_HIDDEN = new Set(["/crm", "/service", "/accounting", "/sepa", "/integrations"]);

const listeners = new Set<() => void>();

function read(): boolean {
  try {
    return window.localStorage.getItem(KEY) === "1";
  } catch {
    return false; // storage can throw in private windows or with blocked site data
  }
}

export function setRetailMode(on: boolean) {
  try {
    window.localStorage.setItem(KEY, on ? "1" : "0");
  } catch {
    /* the toggle still applies for this page load below */
  }
  memory = on;
  listeners.forEach((l) => l());
}

let memory: boolean | null = null;
const snapshot = () => (memory ?? read());

function subscribe(cb: () => void) {
  listeners.add(cb);
  window.addEventListener("storage", cb);
  return () => {
    listeners.delete(cb);
    window.removeEventListener("storage", cb);
  };
}

export function useRetailMode(): boolean {
  return useSyncExternalStore(subscribe, snapshot, () => false);
}

/** NAV_GROUPS minus the routes Retail Mode hides, dropping emptied groups. */
export function visibleNavGroups(retail: boolean): NavGroup[] {
  if (!retail) return NAV_GROUPS;
  return NAV_GROUPS.map((g) => ({ ...g, items: g.items.filter((i) => !RETAIL_HIDDEN.has(i.to)) })).filter((g) => g.items.length > 0);
}
