import { useEffect } from "react";
import styles from "./CommandPalette.module.css";

const SHORTCUTS: { keys: string[]; label: string }[] = [
  { keys: ["Ctrl", "K"], label: "Open command palette" },
  { keys: ["/"], label: "Open command palette" },
  { keys: ["↑", "↓"], label: "Move selection" },
  { keys: ["Enter"], label: "Select / open" },
  { keys: ["Esc"], label: "Close dialog or menu" },
  { keys: ["?"], label: "Show this cheatsheet" },
];

export function ShortcutsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onOpenChange(false);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, onOpenChange]);

  if (!open) return null;

  return (
    <div className={styles.overlay} onClick={() => onOpenChange(false)}>
      <div className={styles.dialog} style={{ maxHeight: "none" }} role="dialog" aria-modal="true" aria-label="Keyboard shortcuts" onClick={(e) => e.stopPropagation()}>
        <div className={styles.dialogHeader}>
          <h2>Keyboard shortcuts</h2>
          <kbd className={styles.escHint}>Esc</kbd>
        </div>
        <div className={styles.shortcutsGrid}>
          {SHORTCUTS.map((s) => (
            <div key={s.label} className={styles.shortcutRow}>
              <span>{s.label}</span>
              <span className={styles.shortcutKeys}>
                {s.keys.map((k) => (
                  <kbd key={k} className={styles.kbd}>
                    {k}
                  </kbd>
                ))}
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
