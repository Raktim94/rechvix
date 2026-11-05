import { useEffect, useRef, useState } from "react";
import { Link, useRouterState } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import styles from "./MobileNav.module.css";
import { navLabel } from "./navLabel";
import { useRetailMode, visibleNavGroups } from "../lib/retailMode";
import { useOrgContext } from "../lib/useOrgContext";
import type { NavItem } from "../nav";

/** Routes that get a permanent slot in the phone bottom bar, in the order a
 * counter operator reaches for them. Everything else lives under "More". */
const PRIMARY = ["/", "/sales", "/catalogue", "/contacts"];

function isActive(pathname: string, to: string): boolean {
  return to === "/" ? pathname === "/" : pathname === to || pathname.startsWith(`${to}/`);
}

/** Phone navigation: four primary destinations plus a "More" sheet with the
 * full grouped menu. The desktop rail stays untouched; this renders only
 * below 768px (CSS), replacing the old 16-item horizontal strip. */
export function MobileNav() {
  const { t } = useTranslation();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const retail = useRetailMode();
  const country = useOrgContext().legalEntity?.CountryCode;
  const groups = visibleNavGroups(retail);
  const all = groups.flatMap((g) => g.items);
  const primary = PRIMARY.map((to) => all.find((i) => i.to === to)).filter((i): i is NavItem => !!i);
  const moreActive = !primary.some((i) => isActive(pathname, i.to));
  const [open, setOpen] = useState(false);
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const d = dialogRef.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);

  // Close after navigating, whatever link was used.
  useEffect(() => {
    setOpen(false);
  }, [pathname]);

  return (
    <>
      <nav className={styles.bar} aria-label={t("nav.primary")}>
        {primary.map((item) => (
          <Link key={item.to} to={item.to} className={styles.tab} aria-current={isActive(pathname, item.to) ? "page" : undefined}>
            <span className={styles.icon}>{item.icon}</span>
            <span className={styles.label}>{navLabel(t, item, country)}</span>
          </Link>
        ))}
        <button
          type="button"
          className={styles.tab}
          aria-haspopup="dialog"
          aria-expanded={open}
          data-active={moreActive}
          onClick={() => setOpen(true)}
        >
          <span className={styles.icon} aria-hidden="true">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
              <circle cx="5" cy="12" r="1.8" />
              <circle cx="12" cy="12" r="1.8" />
              <circle cx="19" cy="12" r="1.8" />
            </svg>
          </span>
          <span className={styles.label}>{t("nav.more")}</span>
        </button>
      </nav>

      <dialog
        ref={dialogRef}
        className={styles.sheet}
        aria-label={t("nav.more")}
        onClose={() => setOpen(false)}
        onClick={(e) => {
          // A click on the dialog element itself is the backdrop.
          if (e.target === e.currentTarget) setOpen(false);
        }}
      >
        <div className={styles.sheetHead}>
          <strong>{t("nav.more")}</strong>
          <button type="button" className={styles.close} onClick={() => setOpen(false)} aria-label="Close">
            ×
          </button>
        </div>
        {groups.map((g) => (
          <section key={g.titleKey} className={styles.group}>
            <h2>{t(g.titleKey)}</h2>
            <ul>
              {g.items.map((item) => (
                <li key={item.to}>
                  <Link to={item.to} className={styles.row} aria-current={isActive(pathname, item.to) ? "page" : undefined}>
                    <span className={styles.icon}>{item.icon}</span>
                    {navLabel(t, item, country)}
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </dialog>
    </>
  );
}
