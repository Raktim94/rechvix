import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useRouterState } from "@tanstack/react-router";
import styles from "./AppShell.module.css";
import { CommandPalette } from "./CommandPalette";
import { Logo } from "./Logo";
import { PlusIcon, SearchIcon } from "./icons";
import { ShortcutsDialog } from "./ShortcutsDialog";
import { useAuth } from "../auth/AuthProvider";
import { useTheme } from "../theme/ThemeProvider";
import { NAV_GROUPS } from "../nav";

export function AppShell({ children }: { children: ReactNode }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const { logout } = useAuth();
  const { theme, toggle } = useTheme();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const createRef = useRef<HTMLDivElement>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);

  useEffect(() => {
    if (!menuOpen && !createOpen) return;
    const onClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false);
      if (createRef.current && !createRef.current.contains(e.target as Node)) setCreateOpen(false);
    };
    // Escape closes whichever popup is open — expected keyboard behavior
    // for menus/listboxes per the ARIA Authoring Practices Guide; without
    // this a keyboard user's only way out is tabbing through every item.
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setMenuOpen(false);
      setCreateOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [menuOpen, createOpen]);

  // Ctrl+K / Cmd+K (and "/", a near-universal alternate) opens the
  // command palette from anywhere in the app — the brief's own
  // expectation (§18's keyboard-shortcuts section). "?" opens the
  // cheatsheet. All three skip while the user is typing in a text
  // field/textarea/select/contentEditable so they don't steal a literal
  // keystroke — "/" and "?" are common in free-text search boxes too.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const isTyping =
        target instanceof HTMLElement &&
        (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT" || target.isContentEditable);

      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        if (isTyping) return;
        e.preventDefault();
        setPaletteOpen(true);
        return;
      }
      if (!isTyping && e.key === "/") {
        e.preventDefault();
        setPaletteOpen(true);
        return;
      }
      if (!isTyping && e.key === "?") {
        e.preventDefault();
        setShortcutsOpen(true);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, []);

  return (
    <div className={styles.shell}>
      <a href="#main-content" className={styles.skipLink}>
        Skip to main content
      </a>
      <nav className={styles.nav} aria-label="Primary">
        <div className={styles.wordmark}>
          <Logo />
          rechvix
        </div>
        <ul className={styles.navList}>
          {NAV_GROUPS.map((group) => (
            <li key={group.title} className={styles.navGroup}>
              <p className={styles.navGroupLabel} aria-hidden="true">
                {group.title}
              </p>
              <ul className={styles.navGroupList}>
                {group.items.map((item) => (
                  <li key={item.to}>
                    <Link
                      to={item.to}
                      className={`${styles.navLink} ${pathname === item.to ? styles.navLinkActive : ""}`.trim()}
                      aria-current={pathname === item.to ? "page" : undefined}
                    >
                      <span className={styles.navIcon}>{item.icon}</span>
                      {item.label}
                    </Link>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
        <p className={styles.navFooter}>
          <a href="https://www.nodedr.com/" target="_blank" rel="noopener noreferrer">
            NodeDR Infotech Private Limited
          </a>
        </p>
      </nav>

      <header className={styles.topbar}>
        <button type="button" className={styles.search} onClick={() => setPaletteOpen(true)}>
          <SearchIcon />
          <span className={styles.searchPlaceholder}>Search customers, products…</span>
          <kbd className={styles.searchHint} aria-hidden="true">
            Ctrl+K
          </kbd>
        </button>
        <div className={styles.topbarSpacer} />
        <div className={styles.primaryActions}>
          <Link to="/sales/new" className={styles.actionPrimary}>
            <span className={styles.actionPlus} aria-hidden="true">
              +
            </span>
            <span className={styles.actionLabel}>Add sale</span>
          </Link>
          <Link to="/purchases" className={styles.actionSecondary}>
            <span className={styles.actionPlus} aria-hidden="true">
              +
            </span>
            <span className={styles.actionLabel}>Add purchase</span>
          </Link>
        </div>
        <div className={styles.userMenu} ref={createRef}>
          <button
            type="button"
            className={styles.iconButton}
            onClick={() => setCreateOpen((v) => !v)}
            aria-expanded={createOpen}
            aria-haspopup="menu"
            aria-label="Create something else"
            title="Create something else"
          >
            <PlusIcon />
          </button>
          {createOpen ? (
            <div className={styles.userDropdown} role="menu">
              <Link to="/contacts" role="menuitem" onClick={() => setCreateOpen(false)}>
                New contact
              </Link>
              <Link to="/catalogue" role="menuitem" onClick={() => setCreateOpen(false)}>
                New product
              </Link>
              <Link to="/pricing" role="menuitem" onClick={() => setCreateOpen(false)}>
                Set a price
              </Link>
              <Link to="/expenses" role="menuitem" onClick={() => setCreateOpen(false)}>
                Record an expense
              </Link>
            </div>
          ) : null}
        </div>
        <button
          type="button"
          className={styles.iconButton}
          onClick={() => setShortcutsOpen(true)}
          aria-label="Keyboard shortcuts"
          title="Keyboard shortcuts (?)"
        >
          ?
        </button>
        <button
          type="button"
          className={styles.iconButton}
          onClick={toggle}
          aria-label={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
        >
          {theme === "dark" ? "☀" : "☾"}
        </button>
        <div className={styles.userMenu} ref={menuRef}>
          <button
            type="button"
            className={styles.userButton}
            onClick={() => setMenuOpen((v) => !v)}
            aria-expanded={menuOpen}
            aria-haspopup="menu"
            aria-label="User menu"
          >
            <span className={styles.avatar} aria-hidden="true">
              U
            </span>
          </button>
          {menuOpen ? (
            <div className={styles.userDropdown} role="menu">
              <Link to="/settings" role="menuitem" onClick={() => setMenuOpen(false)}>
                Settings
              </Link>
              <button type="button" role="menuitem" onClick={() => void logout()}>
                Log out
              </button>
            </div>
          ) : null}
        </div>
      </header>

      <main id="main-content" className={styles.main} tabIndex={-1}>
        {children}
      </main>

      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
      <ShortcutsDialog open={shortcutsOpen} onOpenChange={setShortcutsOpen} />
    </div>
  );
}
