import { useEffect, useRef, useState } from "react";
import styles from "./PrintTemplateMenu.module.css";
import ui from "./ui.module.css";
import { getDefaultPrintTemplate, PRINT_TEMPLATES, setDefaultPrintTemplate } from "../lib/printTemplates";
import { getDefaultPrintTheme, PRINT_THEMES, setDefaultPrintTheme } from "../lib/printThemes";

/** Backend already renders 11 print layouts (internal/modules/sales/
 * printing) selectable via `?template=`, but nothing in the UI ever
 * exposed that — this was a single hardcoded link. Picking a template
 * here remembers the choice (per-browser) as the new default for next
 * time, so a shop that always prints thermal receipts only has to pick
 * that once. The same menu also picks a visual Theme (`?theme=`) —
 * layout (page size/columns) and theme (font/color/border treatment)
 * are independent choices, so this is two small radio-groups in one
 * dropdown rather than a combinatorial list. */
export function PrintTemplateMenu({ documentId }: { documentId: string }) {
  const [open, setOpen] = useState(false);
  const [selectedTemplate, setSelectedTemplate] = useState(getDefaultPrintTemplate);
  const [selectedTheme, setSelectedTheme] = useState(getDefaultPrintTheme);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  const printUrl = (template: string, theme: string) =>
    `/api/v1/sales/documents/${documentId}/print?template=${encodeURIComponent(template)}&theme=${encodeURIComponent(theme)}`;

  return (
    <div className={styles.wrap} ref={ref}>
      <a href={printUrl(selectedTemplate, selectedTheme)} target="_blank" rel="noopener noreferrer" className={`${ui.btnSecondary} ${styles.printLink}`}>
        Print / Download PDF
      </a>
      <button type="button" className={`${ui.btnSecondary} ${styles.caret}`} aria-haspopup="menu" aria-expanded={open} aria-label="Choose invoice layout and design" onClick={() => setOpen((v) => !v)}>
        ▾
      </button>
      {open ? (
        <ul className={styles.menu} role="menu" aria-label="Invoice layout and design">
          <li className={styles.sectionTitle} role="presentation">
            Layout
          </li>
          {PRINT_TEMPLATES.map((t) => (
            <li key={t.value}>
              <button
                type="button"
                role="menuitemradio"
                aria-checked={t.value === selectedTemplate}
                className={styles.item}
                onClick={() => {
                  setSelectedTemplate(t.value);
                  setDefaultPrintTemplate(t.value);
                  setOpen(false);
                  window.open(printUrl(t.value, selectedTheme), "_blank", "noopener,noreferrer");
                }}
              >
                <span className={styles.itemLabel}>
                  {t.label}
                  {t.value === selectedTemplate ? <span className={styles.check} aria-hidden="true">✓</span> : null}
                </span>
                <span className={styles.itemDescription}>{t.description}</span>
              </button>
            </li>
          ))}
          <hr className={styles.sectionDivider} role="presentation" />
          <li className={styles.sectionTitle} role="presentation">
            Design
          </li>
          {PRINT_THEMES.map((t) => (
            <li key={t.value}>
              <button
                type="button"
                role="menuitemradio"
                aria-checked={t.value === selectedTheme}
                className={styles.item}
                onClick={() => {
                  setSelectedTheme(t.value);
                  setDefaultPrintTheme(t.value);
                  setOpen(false);
                  window.open(printUrl(selectedTemplate, t.value), "_blank", "noopener,noreferrer");
                }}
              >
                <span className={styles.itemLabel}>
                  {t.label}
                  {t.value === selectedTheme ? <span className={styles.check} aria-hidden="true">✓</span> : null}
                </span>
                <span className={styles.itemDescription}>{t.description}</span>
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
