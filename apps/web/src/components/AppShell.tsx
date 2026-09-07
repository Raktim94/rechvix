import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import styles from "./AppShell.module.css";
import { Logo } from "./Logo";
import { useAuth } from "../auth/AuthProvider";
import { useTheme } from "../theme/ThemeProvider";
import { api } from "../lib/api-client";

const NAV_ITEMS = [
  { to: "/", label: "Dashboard" },
  { to: "/sales", label: "Sales" },
  { to: "/purchases", label: "Purchases" },
  { to: "/inventory", label: "Inventory" },
  { to: "/catalogue", label: "Catalogue" },
  { to: "/pricing", label: "Pricing" },
  { to: "/contacts", label: "Contacts" },
  { to: "/accounting", label: "Accounting" },
  { to: "/gst", label: "GST / Tax" },
  { to: "/reports", label: "Reports" },
  { to: "/integrations", label: "Integrations" },
  { to: "/backup", label: "Backup" },
  { to: "/settings", label: "Settings" },
] as const;

interface SearchResult {
  kind: "customer" | "product";
  id: string;
  label: string;
  to: string;
}

export function AppShell({ children }: { children: ReactNode }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const navigate = useNavigate();
  const { logout } = useAuth();
  const { theme, toggle } = useTheme();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const createRef = useRef<HTMLDivElement>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<SearchResult[]>([]);
  const [searchOpen, setSearchOpen] = useState(false);
  const searchRef = useRef<HTMLDivElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!menuOpen && !createOpen && !searchOpen) return;
    const onClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false);
      if (createRef.current && !createRef.current.contains(e.target as Node)) setCreateOpen(false);
      if (searchRef.current && !searchRef.current.contains(e.target as Node)) setSearchOpen(false);
    };
    // Escape closes whichever popup is open — expected keyboard behavior
    // for menus/listboxes per the ARIA Authoring Practices Guide; without
    // this a keyboard user's only way out is tabbing through every item.
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setMenuOpen(false);
      setCreateOpen(false);
      setSearchOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [menuOpen, createOpen, searchOpen]);

  // Ctrl+K / Cmd+K jumps to global search from anywhere in the app — the
  // brief's own expectation (§18's keyboard-shortcuts section) and a
  // near-universal convention by now. Always active (not gated on any
  // popup being open, unlike the Escape handler above), and skipped
  // while the user is already typing in a text field/textarea/select so
  // it doesn't steal a literal "k" keystroke — except the search input
  // itself, since Ctrl+K there is exactly "select all and refocus," a
  // harmless no-op.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || e.key.toLowerCase() !== "k") return;
      const target = e.target as HTMLElement | null;
      const isTypingElsewhere =
        target instanceof HTMLElement &&
        target !== searchInputRef.current &&
        (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT" || target.isContentEditable);
      if (isTypingElsewhere) return;
      e.preventDefault();
      searchInputRef.current?.focus();
      searchInputRef.current?.select();
      setSearchOpen(true);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, []);

  // Global search — customers and products in one combined dropdown
  // (brief §24's "search everything" bar). Sales-document-number search
  // isn't wired here yet — the Sales list's own search covers that case
  // today; combining all three is left for a follow-up pass.
  useEffect(() => {
    const q = searchQuery.trim();
    if (q.length < 2) {
      return;
    }
    const handle = setTimeout(() => {
      Promise.all([
        api.getListField<{ ID: string; LegalName: string }>(`/contacts/parties?q=${encodeURIComponent(q)}`, "parties"),
        api.getListField<{ ID: string; Name: string }>(`/catalogue/products?q=${encodeURIComponent(q)}`, "products"),
      ])
        .then(([parties, products]) => {
          setSearchResults([
            ...parties.slice(0, 5).map((p) => ({ kind: "customer" as const, id: p.ID, label: p.LegalName, to: "/contacts" })),
            ...products.slice(0, 5).map((p) => ({ kind: "product" as const, id: p.ID, label: p.Name, to: "/catalogue" })),
          ]);
        })
        .catch(() => setSearchResults([]));
    }, 200);
    return () => clearTimeout(handle);
  }, [searchQuery]);

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
          {NAV_ITEMS.map((item) => (
            <li key={item.to}>
              <Link
                to={item.to}
                className={`${styles.navLink} ${pathname === item.to ? styles.navLinkActive : ""}`.trim()}
                aria-current={pathname === item.to ? "page" : undefined}
              >
                {item.label}
              </Link>
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
        <div className={styles.search} role="search" ref={searchRef} style={{ position: "relative" }}>
          <span aria-hidden="true">⌕</span>
          <input
            ref={searchInputRef}
            type="search"
            placeholder="Search customers, products…"
            aria-label="Global search"
            value={searchQuery}
            onChange={(e) => {
              setSearchQuery(e.target.value);
              setSearchOpen(true);
            }}
            onFocus={() => setSearchOpen(true)}
          />
          {!searchQuery ? (
            <kbd className={styles.searchHint} aria-hidden="true">
              Ctrl+K
            </kbd>
          ) : null}
          {searchOpen && searchQuery.trim().length >= 2 && searchResults.length > 0 ? (
            <ul
              className={styles.userDropdown}
              role="menu"
              aria-label="Search results"
              style={{ left: 0, right: "auto", top: "calc(100% + 4px)", minWidth: 280 }}
            >
              {searchResults.map((r) => (
                <li key={`${r.kind}-${r.id}`}>
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      setSearchOpen(false);
                      setSearchQuery("");
                      void navigate({ to: r.to });
                    }}
                  >
                    {r.label} <span style={{ color: "var(--color-text-faint)" }}>· {r.kind}</span>
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
        <div className={styles.topbarSpacer} />
        <div className={styles.userMenu} ref={createRef}>
          <button type="button" className={styles.quickCreate} onClick={() => setCreateOpen((v) => !v)} aria-expanded={createOpen} aria-haspopup="menu">
            + Quick create
          </button>
          {createOpen ? (
            <div className={styles.userDropdown} role="menu">
              <Link to="/sales/new" role="menuitem" onClick={() => setCreateOpen(false)}>
                New sale
              </Link>
              <Link to="/purchases" role="menuitem" onClick={() => setCreateOpen(false)}>
                New purchase
              </Link>
              <Link to="/contacts" role="menuitem" onClick={() => setCreateOpen(false)}>
                New contact
              </Link>
              <Link to="/catalogue" role="menuitem" onClick={() => setCreateOpen(false)}>
                New product
              </Link>
              <Link to="/pricing" role="menuitem" onClick={() => setCreateOpen(false)}>
                Set a price
              </Link>
            </div>
          ) : null}
        </div>
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
    </div>
  );
}
