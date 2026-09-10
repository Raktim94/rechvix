import type { ReactNode } from "react";
import {
  AccountingIcon,
  BackupIcon,
  CalendarIcon,
  CatalogueIcon,
  ContactsIcon,
  DashboardIcon,
  GstIcon,
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
  label: string;
  icon: ReactNode;
}

export interface NavGroup {
  title: string;
  items: NavItem[];
}

/** Single source of truth for both the sidebar (AppShell) and the
 * command palette's "Go to" list — grouped by the same task clusters a
 * counter operator thinks in (Stage 10b's page inventory), rather than
 * the flat A-Z-ish list this used to be. */
export const NAV_GROUPS: NavGroup[] = [
  {
    title: "Overview",
    items: [{ to: "/", label: "Dashboard", icon: <DashboardIcon /> }],
  },
  {
    title: "Billing",
    items: [
      { to: "/sales", label: "Sales", icon: <SalesIcon /> },
      { to: "/purchases", label: "Purchases", icon: <PurchasesIcon /> },
    ],
  },
  {
    title: "Catalogue",
    items: [
      { to: "/inventory", label: "Inventory", icon: <InventoryIcon /> },
      { to: "/catalogue", label: "Catalogue", icon: <CatalogueIcon /> },
    ],
  },
  {
    title: "Relationships",
    items: [{ to: "/contacts", label: "Contacts", icon: <ContactsIcon /> }],
  },
  {
    title: "Finance",
    items: [
      { to: "/accounting", label: "Accounting", icon: <AccountingIcon /> },
      { to: "/expenses", label: "Expenses", icon: <WalletIcon /> },
      { to: "/gst", label: "GST / Tax", icon: <GstIcon /> },
      { to: "/reports", label: "Reports", icon: <ReportsIcon /> },
    ],
  },
  {
    title: "Team",
    items: [
      { to: "/staff", label: "Staff", icon: <StaffIcon /> },
      { to: "/calendar", label: "Calendar", icon: <CalendarIcon /> },
    ],
  },
  {
    title: "Setup",
    items: [
      { to: "/integrations", label: "Integrations", icon: <IntegrationsIcon /> },
      { to: "/backup", label: "Backup", icon: <BackupIcon /> },
      { to: "/settings", label: "Settings", icon: <SettingsIcon /> },
    ],
  },
];

export const NAV_ITEMS: NavItem[] = NAV_GROUPS.flatMap((g) => g.items);
