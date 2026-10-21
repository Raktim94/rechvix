// A convenience preference only: book currency and all exchange-rate validation
// remain authoritative server settings. Scope it to the signed-in tenant/user/company.
export function invoiceCurrencyKey(organisationId: string, userId: string, companyId: string): string {
  return `rechvix:invoice-currency:${organisationId}:${userId}:${companyId}`;
}
export function readInvoiceCurrency(key: string, fallback: string): string {
  try { return localStorage.getItem(key) || fallback; } catch { return fallback; }
}
export function saveInvoiceCurrency(key: string, value: string): boolean {
  try { localStorage.setItem(key, value); return true; } catch { return false; }
}
