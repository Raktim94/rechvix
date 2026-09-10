import { Command } from "cmdk";
import { useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import styles from "./CommandPalette.module.css";
import { ContactsIcon, PricingIcon, PurchasesIcon, SalesIcon, SearchIcon, SettingsIcon } from "./icons";
import { api } from "../lib/api-client";
import { NAV_GROUPS } from "../nav";

interface SearchResult {
  kind: "customer" | "product";
  id: string;
  label: string;
  to: string;
}

const QUICK_ACTIONS = [
  { to: "/sales/new", label: "New sale", icon: <SalesIcon /> },
  { to: "/purchases", label: "New purchase", icon: <PurchasesIcon /> },
  { to: "/contacts", label: "New contact", icon: <ContactsIcon /> },
  { to: "/settings", label: "Open settings", icon: <SettingsIcon /> },
] as const;

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResult[]>([]);

  useEffect(() => {
    if (!open) setQuery("");
  }, [open]);

  // Same "search everything" combined lookup AppShell used to run inline
  // — moved here now that the palette is the one search surface, still
  // debounced and still skipped under 2 characters to avoid a full-table
  // scan on every keystroke.
  useEffect(() => {
    const q = query.trim();
    if (q.length < 2) {
      setResults([]);
      return;
    }
    const handle = setTimeout(() => {
      Promise.all([
        api.getListField<{ ID: string; LegalName: string }>(`/contacts/parties?q=${encodeURIComponent(q)}`, "parties"),
        api.getListField<{ ID: string; Name: string }>(`/catalogue/products?q=${encodeURIComponent(q)}`, "products"),
      ])
        .then(([parties, products]) => {
          setResults([
            ...parties.slice(0, 5).map((p) => ({ kind: "customer" as const, id: p.ID, label: p.LegalName, to: "/contacts" })),
            ...products.slice(0, 5).map((p) => ({ kind: "product" as const, id: p.ID, label: p.Name, to: "/catalogue" })),
          ]);
        })
        .catch(() => setResults([]));
    }, 200);
    return () => clearTimeout(handle);
  }, [query]);

  function go(to: string) {
    onOpenChange(false);
    void navigate({ to });
  }

  return (
    <Command.Dialog
      open={open}
      onOpenChange={onOpenChange}
      label="Command palette"
      shouldFilter
      className={styles.commandRoot}
      overlayClassName={styles.overlay}
      contentClassName={styles.dialog}
    >
      <div className={styles.inputRow}>
        <SearchIcon />
        <Command.Input
          autoFocus
          value={query}
          onValueChange={setQuery}
          placeholder="Search customers, products, or jump to a page…"
          className={styles.input}
        />
        <kbd className={styles.escHint}>Esc</kbd>
      </div>
      <Command.List className={styles.list}>
        <Command.Empty className={styles.empty}>No matches for "{query}".</Command.Empty>

        {results.length > 0 ? (
          <Command.Group heading="Results">
            {results.map((r) => (
              <Command.Item key={`${r.kind}-${r.id}`} value={`result ${r.label}`} className={styles.item} onSelect={() => go(r.to)}>
                {r.kind === "customer" ? <ContactsIcon /> : <PricingIcon />}
                {r.label}
                <span className={styles.itemMeta}>{r.kind}</span>
              </Command.Item>
            ))}
          </Command.Group>
        ) : null}

        <Command.Group heading="Quick actions">
          {QUICK_ACTIONS.map((a) => (
            <Command.Item key={a.label} value={a.label} className={styles.item} onSelect={() => go(a.to)}>
              {a.icon}
              {a.label}
            </Command.Item>
          ))}
        </Command.Group>

        {NAV_GROUPS.map((group) => (
          <Command.Group key={group.title} heading={`Go to ${group.title}`}>
            {group.items.map((item) => (
              <Command.Item key={item.to} value={`go to ${item.label}`} className={styles.item} onSelect={() => go(item.to)}>
                {item.icon}
                {item.label}
              </Command.Item>
            ))}
          </Command.Group>
        ))}
      </Command.List>
      <div className={styles.footer}>
        <span>
          <kbd>↑↓</kbd>Navigate
        </span>
        <span>
          <kbd>↵</kbd>Select
        </span>
        <span>
          <kbd>Esc</kbd>Close
        </span>
      </div>
    </Command.Dialog>
  );
}
