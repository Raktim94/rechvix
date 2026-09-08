/** Mirrors internal/modules/sales/printing.Template's 11 values — these
 * 5 are the ones worth surfacing directly in the UI (brief: "let people
 * choose from 5 good invoice designs"); the other 6 (purchase order,
 * delivery challan, receipt, statement, credit/debit note) are used
 * elsewhere in the product, not from a sale's own print button. */
export interface PrintTemplateOption {
  value: string;
  label: string;
  description: string;
}

export const PRINT_TEMPLATES: PrintTemplateOption[] = [
  { value: "A4_GST_INVOICE", label: "GST invoice (A4)", description: "Full tax invoice for print or email — the standard choice." },
  { value: "COMPACT_INVOICE", label: "Compact invoice", description: "Same details, tighter layout — less paper per bill." },
  { value: "THERMAL_80MM", label: "Thermal receipt (80mm)", description: "For an 80mm receipt printer at the counter." },
  { value: "THERMAL_58MM", label: "Thermal receipt (58mm)", description: "For a narrow 58mm receipt printer." },
  { value: "QUOTATION", label: "Quotation", description: "No tax breakdown — for a price quote, not a final bill." },
];

const STORAGE_KEY = "rechvix.defaultPrintTemplate";

export function getDefaultPrintTemplate(): string {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored && PRINT_TEMPLATES.some((t) => t.value === stored)) return stored;
  } catch {
    // Storage can throw in a locked-down browser context — falling back
    // to the same default the backend itself uses either way.
  }
  return "A4_GST_INVOICE";
}

export function setDefaultPrintTemplate(value: string) {
  try {
    localStorage.setItem(STORAGE_KEY, value);
  } catch {
    // Per-browser convenience only — nothing breaks if this can't persist.
  }
}
