import { useEffect, useRef, useState } from "react";
import styles from "./PrintTemplateMenu.module.css";
import ui from "./ui.module.css";
import { getDefaultPrintTemplate, PRINT_TEMPLATES, setDefaultPrintTemplate } from "../lib/printTemplates";

/** Backend already renders 11 print layouts (internal/modules/sales/
 * printing) selectable via `?template=`, but nothing in the UI ever
 * exposed that — this was a single hardcoded link. Picking a template
 * here remembers the choice (per-browser) as the new default for next
 * time, so a shop that always prints thermal receipts only has to pick
 * that once. */
export function PrintTemplateMenu({ documentId }: { documentId: string }) {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState(getDefaultPrintTemplate);
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

  const printUrl = (template: string) => `/api/v1/sales/documents/${documentId}/print?template=${encodeURIComponent(template)}`;

  return (
    <div className={styles.wrap} ref={ref}>
      <a href={printUrl(selected)} target="_blank" rel="noopener noreferrer" className={`${ui.btnSecondary} ${styles.printLink}`}>
        Print / Download PDF
      </a>
      <button type="button" className={`${ui.btnSecondary} ${styles.caret}`} aria-haspopup="menu" aria-expanded={open} aria-label="Choose invoice layout" onClick={() => setOpen((v) => !v)}>
        ▾
      </button>
      {open ? (
        <ul className={styles.menu} role="menu" aria-label="Invoice layout">
          {PRINT_TEMPLATES.map((t) => (
            <li key={t.value}>
              <button
                type="button"
                role="menuitemradio"
                aria-checked={t.value === selected}
                className={styles.item}
                onClick={() => {
                  setSelected(t.value);
                  setDefaultPrintTemplate(t.value);
                  setOpen(false);
                  window.open(printUrl(t.value), "_blank", "noopener,noreferrer");
                }}
              >
                <span className={styles.itemLabel}>
                  {t.label}
                  {t.value === selected ? <span className={styles.check} aria-hidden="true">✓</span> : null}
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
