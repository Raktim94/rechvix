/** Mirrors internal/modules/sales/printing.Theme's 5 values — the visual
 * design (font/color/border treatment) a printed document uses,
 * independent of PrintTemplateOption's page-size/document-type choice
 * (a shop can print an A4 GST invoice in any of these 5 looks). See
 * printing/pdf.go's styleFor for exactly what each one changes. */
export interface PrintThemeOption {
  value: string;
  label: string;
  description: string;
}

export const PRINT_THEMES: PrintThemeOption[] = [
  { value: "CLASSIC", label: "Classic", description: "Black and white, fully bordered tables — the original, plain look." },
  { value: "MODERN", label: "Modern", description: "Sans-serif with a blue accent and clean rule lines instead of a full grid." },
  { value: "MINIMAL", label: "Minimal", description: "The lightest look — thin gray rules only, maximum whitespace." },
  { value: "BOLD", label: "Bold", description: "Solid color header and totals band with reversed white text — the most eye-catching." },
  { value: "ELEGANT", label: "Elegant", description: "A serif font with a deep accent color, for a formal, traditional look." },
];

const STORAGE_KEY = "rechvix.defaultPrintTheme";

export function getDefaultPrintTheme(): string {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored && PRINT_THEMES.some((t) => t.value === stored)) return stored;
  } catch {
    // Storage can throw in a locked-down browser context — falling back
    // to the same default the backend itself uses either way.
  }
  return "CLASSIC";
}

export function setDefaultPrintTheme(value: string) {
  try {
    localStorage.setItem(STORAGE_KEY, value);
  } catch {
    // Per-browser convenience only — nothing breaks if this can't persist.
  }
}
