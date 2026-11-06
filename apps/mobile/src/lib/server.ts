/** "shop.example.com" -> https://shop.example.com. Private/LAN addresses get http, because self-hosted shop installs
 *  often have no certificate. Same rule as the Android shell it replaces. */
export function normalizeServer(raw: string): string {
  let s = raw.trim();
  if (!s.includes("://")) {
    const host = s.split(/[/:]/)[0] ?? "";
    const lan =
      host === "localhost" || host.endsWith(".local") || /^(10|127)\./.test(host) || /^192\.168\./.test(host) || /^172\.(1[6-9]|2\d|3[01])\./.test(host);
    s = `${lan ? "http" : "https"}://${s}`;
  }
  return s.replace(/\/+$/, "");
}

export const isCleartext = (base: string): boolean => base.startsWith("http://");

/** Returns null when the address answers like Rechvix, otherwise a message a shop owner can act on. */
export async function probeServer(base: string, fetchImpl: typeof fetch = fetch, timeoutMs = 7000): Promise<string | null> {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), timeoutMs);
  try {
    const res = await fetchImpl(`${base}/health/ready`, { signal: ctl.signal, credentials: "omit" });
    if (res.status === 200) return null;
    return `That address answered, but not like Rechvix (status ${res.status}). Check it and try again.`;
  } catch (e) {
    if (e instanceof Error && e.name === "AbortError") return "The server took too long to answer. Check the address and your connection.";
    return "Can't reach the server. Check the address and that you are online.";
  } finally {
    clearTimeout(timer);
  }
}
