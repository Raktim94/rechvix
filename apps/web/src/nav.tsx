import type { ReactNode } from "react";
import {
  AccountingIcon,
  BackupIcon,
  CalendarIcon,
  CatalogueIcon,
  ContactsIcon,
  DashboardIcon,
  GstIcon,
  ImportIcon,
  IntegrationsIcon,
  InventoryIcon,
  PurchasesIcon,
  ReportsIcon,
  SalesIcon,
  SettingsIcon,
  StaffIcon,
  WalletIcon,
} from "./components/icons";

export interface NavItem {
  to: string;
  /** i18n key under nav.item.*; `tax` has a `tax_gst` context for India. */
  labelKey: string;
  icon: ReactNode;
}

export interface NavGroup {
  /** i18n key under nav.group.* */
  titleKey: string;
  items: NavItem[];
}

/** Single source of truth for both the sidebar (AppShell) and the
 * command palette's "Go to" list — grouped by the same task clusters a
 * counter operator thinks in (Stage 10b's page inventory), rather than
 * the flat A-Z-ish list this used to be. */
export const NAV_GROUPS: NavGroup[] = [
  {
    titleKey: "nav.group.overview",
    items: [{ to: "/", labelKey: "nav.item.dashboard", icon: <DashboardIcon /> }],
  },
  {
    titleKey: "nav.group.billing",
    items: [
      { to: "/sales", labelKey: "nav.item.sales", icon: <SalesIcon /> },
      { to: "/purchases", labelKey: "nav.item.purchases", icon: <PurchasesIcon /> },
    ],
  },
  {
    titleKey: "nav.group.catalogue",
    items: [
      { to: "/inventory", labelKey: "nav.item.inventory", icon: <InventoryIcon /> },
      { to: "/catalogue", labelKey: "nav.item.catalogue", icon: <CatalogueIcon /> },
    ],
  },
  {
    titleKey: "nav.group.relationships",
    items: [
      { to: "/contacts", labelKey: "nav.item.contacts", icon: <ContactsIcon /> },
      { to: "/crm", labelKey: "nav.item.crm", icon: <ContactsIcon /> },
      { to: "/service", labelKey: "nav.item.service", icon: <SettingsIcon /> },
    ],
  },
  {
    titleKey: "nav.group.finance",
    items: [
      { to: "/accounting", labelKey: "nav.item.accounting", icon: <AccountingIcon /> },
      { to: "/expenses", labelKey: "nav.item.expenses", icon: <WalletIcon /> },
      { to: "/sepa", labelKey: "nav.item.sepa", icon: <WalletIcon /> },
      { to: "/gst", labelKey: "nav.item.tax", icon: <GstIcon /> },
      { to: "/reports", labelKey: "nav.item.reports", icon: <ReportsIcon /> },
    ],
  },
  {
    titleKey: "nav.group.team",
    items: [
      { to: "/staff", labelKey: "nav.item.staff", icon: <StaffIcon /> },
      { to: "/calendar", labelKey: "nav.item.calendar", icon: <CalendarIcon /> },
    ],
  },
  {
    titleKey: "nav.group.setup",
    items: [
      { to: "/integrations", labelKey: "nav.item.integrations", icon: <IntegrationsIcon /> },
      { to: "/import", labelKey: "nav.item.import", icon: <ImportIcon /> },
      { to: "/backup", labelKey: "nav.item.backup", icon: <BackupIcon /> },
      { to: "/settings", labelKey: "nav.item.settings", icon: <SettingsIcon /> },
    ],
  },
];

export const NAV_ITEMS: NavItem[] = NAV_GROUPS.flatMap((g) => g.items);
