import { lazy } from "react";

/** React.lazy()-wrapped page components, kept in their own module so
 * router.tsx (which also exports the non-component `router` value) stays
 * clean for React Fast Refresh. See router.tsx's top comment for why
 * every feature page is lazy-loaded. */
export const DashboardPage = lazy(() => import("./pages/DashboardPage").then((m) => ({ default: m.DashboardPage })));
export const BillingPage = lazy(() => import("./pages/sales/BillingPage").then((m) => ({ default: m.BillingPage })));
export const SalesDetailPage = lazy(() => import("./pages/sales/SalesDetailPage").then((m) => ({ default: m.SalesDetailPage })));
export const SalesListPage = lazy(() => import("./pages/sales/SalesListPage").then((m) => ({ default: m.SalesListPage })));
export const PurchasesPage = lazy(() => import("./pages/purchases/PurchasesPage").then((m) => ({ default: m.PurchasesPage })));
export const InventoryPage = lazy(() => import("./pages/inventory/InventoryPage").then((m) => ({ default: m.InventoryPage })));
export const ContactsPage = lazy(() => import("./pages/contacts/ContactsPage").then((m) => ({ default: m.ContactsPage })));
export const ContactDetailPage = lazy(() => import("./pages/contacts/ContactDetailPage").then((m) => ({ default: m.ContactDetailPage })));
export const CataloguePage = lazy(() => import("./pages/catalogue/CataloguePage").then((m) => ({ default: m.CataloguePage })));
export const AccountingPage = lazy(() => import("./pages/accounting/AccountingPage").then((m) => ({ default: m.AccountingPage })));
export const GstPage = lazy(() => import("./pages/gst/GstPage").then((m) => ({ default: m.GstPage })));
export const PricingPage = lazy(() => import("./pages/pricing/PricingPage").then((m) => ({ default: m.PricingPage })));
export const ReportsPage = lazy(() => import("./pages/reports/ReportsPage").then((m) => ({ default: m.ReportsPage })));
export const SettingsPage = lazy(() => import("./pages/settings/SettingsPage").then((m) => ({ default: m.SettingsPage })));
