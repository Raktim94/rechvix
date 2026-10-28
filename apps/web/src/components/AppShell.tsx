import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useRouterState } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import styles from "./AppShell.module.css";
import { CommandPalette } from "./CommandPalette";
import { CompanySwitcher } from "./CompanySwitcher";
import { Logo } from "./Logo";
import { PlusIcon, SearchIcon } from "./icons";
import { ShortcutsDialog } from "./ShortcutsDialog";
import { useAuth } from "../auth/AuthProvider";
import { useTheme } from "../theme/ThemeProvider";
import { LanguageMenuItems } from "./LanguageMenuItems";
import { navLabel } from "./navLabel";
import { setFormatRegion } from "../i18n";
import { useOrgContext } from "../lib/useOrgContext";
import { useRetailMode, visibleNavGroups } from "../lib/retailMode";

export function AppShell({ children }: { children: ReactNode }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const { logout } = useAuth();
  const { t } = useTranslation();
  const retail = useRetailMode();
  const { legalEntity } = useOrgContext();
  const country = legalEntity?.CountryCode;
  // Money/date formatting follows the active company's country (see i18n/index.ts).
  setFormatRegion(country);
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

  // On phones the nav is a horizontally scrolling tab bar: keep the current page's tab in view.
  useEffect(() => {
    document.querySelector<HTMLElement>(`.${styles.navLinkActive}`)?.scrollIntoView({ inline: "center", block: "nearest" });
  }, [pathname]);

  return (
    <div className={styles.shell}>
      <a href="#main-content" className={styles.skipLink}>
        {t("shell.skipToContent")}
      </a>
      <nav className={styles.nav} aria-label={t("nav.primary")}>
        <div className={styles.wordmark}>
          <Logo />
          rechvix
        </div>
        <ul className={styles.navList}>
          {visibleNavGroups(retail).map((group) => (
            <li key={group.titleKey} className={styles.navGroup}>
              <p className={styles.navGroupLabel} aria-hidden="true">
                {t(group.titleKey)}
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
                      {navLabel(t, item, country)}
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
        <button type="button" className={styles.search} onClick={() => setPaletteOpen(true)} aria-label={t("shell.searchPlaceholder")}>
          <SearchIcon />
          <span className={styles.searchPlaceholder}>{t("shell.searchPlaceholder")}</span>
          <kbd className={styles.searchHint} aria-hidden="true">
            Ctrl+K
          </kbd>
        </button>
        <div className={styles.topbarSpacer} />
        <CompanySwitcher />
        <div className={styles.primaryActions}>
          <Link to="/sales/new" className={styles.actionPrimary} aria-label={t("shell.addSale")}>
            <span className={styles.actionPlus} aria-hidden="true">
              +
            </span>
            <span className={styles.actionLabel}>{t("shell.addSale")}</span>
          </Link>
          <Link to="/purchases" className={styles.actionSecondary} aria-label={t("shell.addPurchase")}>
            <span className={styles.actionPlus} aria-hidden="true">
              +
            </span>
            <span className={styles.actionLabel}>{t("shell.addPurchase")}</span>
          </Link>
        </div>
        <div className={styles.userMenu} ref={createRef}>
          <button
            type="button"
            className={styles.iconButton}
            onClick={() => setCreateOpen((v) => !v)}
            aria-expanded={createOpen}
            aria-haspopup="menu"
            aria-label={t("shell.createMore")}
            title={t("shell.createMore")}
          >
            <PlusIcon />
          </button>
          {createOpen ? (
            <div className={styles.userDropdown} role="menu">
              <Link to="/contacts" role="menuitem" onClick={() => setCreateOpen(false)}>
                {t("shell.newContact")}
              </Link>
              <Link to="/catalogue" role="menuitem" onClick={() => setCreateOpen(false)}>
                {t("shell.newProduct")}
              </Link>
              <Link to="/expenses" role="menuitem" onClick={() => setCreateOpen(false)}>
                {t("shell.recordExpense")}
              </Link>
            </div>
          ) : null}
        </div>
        <button
          type="button"
          className={`${styles.iconButton} ${styles.shortcutsButton}`}
          onClick={() => setShortcutsOpen(true)}
          aria-label={t("shell.shortcuts")}
          title={t("shell.shortcutsHint")}
        >
          ?
        </button>
        <button
          type="button"
          className={styles.iconButton}
          onClick={toggle}
          aria-label={theme === "dark" ? t("shell.switchToLight") : t("shell.switchToDark")}
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
            aria-label={t("shell.userMenu")}
          >
            <span className={styles.avatar} aria-hidden="true">
              U
            </span>
          </button>
          {menuOpen ? (
            <div className={styles.userDropdown} role="menu">
              <Link to="/settings" role="menuitem" onClick={() => setMenuOpen(false)}>
                {t("shell.settings")}
              </Link>
              <LanguageMenuItems onChosen={() => setMenuOpen(false)} />
              <button type="button" role="menuitem" onClick={() => void logout()}>
                {t("shell.logout")}
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
