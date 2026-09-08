import { Link } from "@tanstack/react-router";
import styles from "./QuickAccess.module.css";

// The most common next actions a billing-counter operator takes, as one
// row of large, always-visible tap targets — a faster path than the
// topbar's "+ Quick create" dropdown (AppShell.tsx), which hides the same
// destinations behind a click-to-open menu. Kept short and task-shaped
// (brief §24's "best user flow" direction) rather than mirroring the full
// nav.
const ACTIONS = [
  { to: "/sales/new", label: "New sale", icon: <path d="M4 3h11l4 4v14H4z M15 3v5h5 M9 13h6 M9 17h6" /> },
  { to: "/purchases", label: "New purchase", icon: <path d="M3 6h2l2.4 12h11.2L21 8H7 M9 21a1 1 0 100-2 1 1 0 000 2z M18 21a1 1 0 100-2 1 1 0 000 2z" /> },
  { to: "/contacts", label: "New contact", icon: <path d="M12 12a4 4 0 100-8 4 4 0 000 8z M4 21c1.5-4.5 5-6 8-6s6.5 1.5 8 6" /> },
  { to: "/catalogue", label: "New product", icon: <path d="M21 8l-9-5-9 5 9 5 9-5z M3 8v8l9 5 9-5V8 M12 13v8" /> },
  { to: "/reports", label: "View reports", icon: <path d="M4 20V10 M10 20V4 M16 20v-7 M22 20H2" /> },
] as const;

export function QuickAccess() {
  return (
    <nav className={styles.row} aria-label="Quick access">
      {ACTIONS.map((a) => (
        <Link key={a.to} to={a.to} className={styles.tile}>
          <span className={styles.iconChip} aria-hidden="true">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              {a.icon}
            </svg>
          </span>
          <span>{a.label}</span>
        </Link>
      ))}
    </nav>
  );
}
