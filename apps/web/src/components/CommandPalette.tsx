import { Command } from "cmdk";
import { useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import styles from "./CommandPalette.module.css";
import { ContactsIcon, PricingIcon, PurchasesIcon, SalesIcon, SearchIcon, SettingsIcon } from "./icons";
import { api } from "../lib/api-client";
import { useRetailMode, visibleNavGroups } from "../lib/retailMode";
import { navLabel } from "./navLabel";
import { useOrgContext } from "../lib/useOrgContext";

interface SearchResult {
  kind: "customer" | "product";
  id: string;
  label: string;
  to: string;
}

const QUICK_ACTIONS = [
  { to: "/sales/new", labelKey: "palette.newSale", icon: <SalesIcon /> },
  { to: "/purchases", labelKey: "palette.newPurchase", icon: <PurchasesIcon /> },
  { to: "/contacts", labelKey: "palette.newContact", icon: <ContactsIcon /> },
  { to: "/settings", labelKey: "palette.openSettings", icon: <SettingsIcon /> },
] as const;

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const retail = useRetailMode();
  const country = useOrgContext().legalEntity?.CountryCode;
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
      label={t("palette.label")}
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
          placeholder={t("palette.placeholder")}
          className={styles.input}
        />
        <kbd className={styles.escHint}>Esc</kbd>
      </div>
      <Command.List className={styles.list}>
        <Command.Empty className={styles.empty}>{t("palette.noMatches", { query })}</Command.Empty>

        {results.length > 0 ? (
          <Command.Group heading={t("palette.results")}>
            {results.map((r) => (
              <Command.Item key={`${r.kind}-${r.id}`} value={`result ${r.label}`} className={styles.item} onSelect={() => go(r.to)}>
                {r.kind === "customer" ? <ContactsIcon /> : <PricingIcon />}
                {r.label}
                <span className={styles.itemMeta}>{t(`palette.${r.kind}`)}</span>
              </Command.Item>
            ))}
          </Command.Group>
        ) : null}

        <Command.Group heading={t("palette.quickActions")}>
          {QUICK_ACTIONS.map((a) => (
            <Command.Item key={a.to + a.labelKey} value={t(a.labelKey)} className={styles.item} onSelect={() => go(a.to)}>
              {a.icon}
              {t(a.labelKey)}
            </Command.Item>
          ))}
        </Command.Group>

        {visibleNavGroups(retail).map((group) => (
          <Command.Group key={group.titleKey} heading={t("palette.goTo", { group: t(group.titleKey) })}>
            {group.items.map((item) => (
              <Command.Item key={item.to} value={t("palette.goToItem", { item: navLabel(t, item, country) })} className={styles.item} onSelect={() => go(item.to)}>
                {item.icon}
                {navLabel(t, item, country)}
              </Command.Item>
            ))}
          </Command.Group>
        ))}
      </Command.List>
      <div className={styles.footer}>
        <span>
          <kbd>↑↓</kbd>{t("palette.navigate")}
        </span>
        <span>
          <kbd>↵</kbd>{t("palette.select")}
        </span>
        <span>
          <kbd>Esc</kbd>{t("palette.close")}
        </span>
      </div>
    </Command.Dialog>
  );
}
