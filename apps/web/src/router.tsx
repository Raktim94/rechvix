import { Suspense } from "react";
import { createRootRoute, createRoute, createRouter, Navigate, Outlet, redirect, useSearch } from "@tanstack/react-router";
import { AppShell } from "./components/AppShell";
import { LoginPage } from "./pages/LoginPage";
import { BootstrapPage } from "./pages/BootstrapPage";
import { ForgotPasswordPage } from "./pages/ForgotPasswordPage";
import { ResetPasswordPage } from "./pages/ResetPasswordPage";
import { PlaceholderPage } from "./pages/PlaceholderPage";
import { readSessionHint } from "./auth/session";
import {
  AccountingPage,
  BillingPage,
  CataloguePage,
  ContactsPage,
  DashboardPage,
  GstPage,
  InventoryPage,
  PricingPage,
  PurchasesPage,
  ReportsPage,
  SalesDetailPage,
  SalesListPage,
  SettingsPage,
} from "./lazyPages";

/**
 * Code-based routing (not TanStack Router's file-based/codegen mode) —
 * simplest option for this pass's route count, no extra Vite plugin or
 * generated route-tree file needed.
 *
 * Every authenticated route is a DIRECT child of the root, each wrapped
 * in <AppShell> individually via `withShell` below, rather than nested
 * under one shared layout route with a shared `beforeLoad`. Root cause of
 * why a shared-layout attempt kept failing (confirmed via a literal
 * `<Link to="/sales">` test, isolated from the nav-list array): the
 * `placeholderRoute(path: string, title: string)` factory's plain
 * `string` parameter widened each route's literal path type before
 * `createRoute` ever saw it — TanStack Router's type system needs the
 * literal ("/sales"), not `string`, to add a path to the router's
 * registered union. Fixed properly below with
 * `placeholderRoute<TPath extends string>(path: TPath, ...)`, which
 * preserves the literal through the call. Kept this flat structure
 * (one `beforeLoad: requireAuth` per route) rather than reintroducing a
 * shared layout parent now that the real fix is known, since it already
 * works and re-nesting isn't worth the risk under this pass's time
 * budget — worth revisiting in a later pass if the per-route repetition
 * becomes annoying.
 *
 * Every feature page below is React.lazy-loaded (Stage 10b-2) — the
 * single-chunk production build had grown past 1.5MB (mostly ECharts,
 * used only by the dashboard) once Sales/Purchases/Inventory/etc. all
 * existed; splitting means visiting one screen no longer pays for every
 * other screen's code, and dashboard's chart library only loads for
 * users who actually see the dashboard.
 */

const rootRoute = createRootRoute({
  component: () => <Outlet />,
});

// Session hint is advisory only (see auth/session.ts) — a stale hint
// just means the first protected API call 401s and the app-level
// listener bounces back to /login. This guard exists purely to avoid
// flashing the authenticated shell for a definitely-logged-out visitor.
function requireAuth() {
  if (!readSessionHint()) {
    throw redirect({ to: "/login" });
  }
}

function withShell(Page: React.ComponentType) {
  return () => (
    <AppShell>
      <Suspense fallback={null}>
        <Page />
      </Suspense>
    </AppShell>
  );
}

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: LoginPage,
});

const bootstrapRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/setup",
  component: BootstrapPage,
});

const forgotPasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/forgot-password",
  component: ForgotPasswordPage,
});

const resetPasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/reset-password",
  validateSearch: (search: Record<string, unknown>): { token?: string } => ({
    token: typeof search.token === "string" ? search.token : undefined,
  }),
  component: ResetPasswordRoute,
});

function ResetPasswordRoute() {
  const { token } = useSearch({ from: resetPasswordRoute.id });
  return <ResetPasswordPage token={token} />;
}

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: requireAuth,
  component: withShell(DashboardPage),
});

function placeholderRoute<TPath extends string>(path: TPath, title: string) {
  return createRoute({
    getParentRoute: () => rootRoute,
    path,
    beforeLoad: requireAuth,
    component: withShell(() => <PlaceholderPage title={title} />),
  });
}

function realRoute<TPath extends string>(path: TPath, component: React.ComponentType) {
  return createRoute({
    getParentRoute: () => rootRoute,
    path,
    beforeLoad: requireAuth,
    component: withShell(component),
  });
}

const salesListRoute = realRoute("/sales", SalesListPage);

const salesNewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sales/new",
  beforeLoad: requireAuth,
  validateSearch: (search: Record<string, unknown>): { resume?: string } => ({
    resume: typeof search.resume === "string" ? search.resume : undefined,
  }),
  component: withShell(() => {
    const { resume } = useSearch({ from: salesNewRoute.id });
    return <BillingPage resumeDocumentId={resume} />;
  }),
});

const salesDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sales/$id",
  beforeLoad: requireAuth,
  component: withShell(() => {
    const { id } = salesDetailRoute.useParams();
    return <SalesDetailPage id={id} />;
  }),
});

const purchasesRoute = realRoute("/purchases", PurchasesPage);
const inventoryRoute = realRoute("/inventory", InventoryPage);
const catalogueRoute = realRoute("/catalogue", CataloguePage);
const pricingRoute = realRoute("/pricing", PricingPage);
const contactsRoute = realRoute("/contacts", ContactsPage);
const accountingRoute = realRoute("/accounting", AccountingPage);
const gstRoute = realRoute("/gst", GstPage);
const reportsRoute = realRoute("/reports", ReportsPage);
const integrationsRoute = placeholderRoute("/integrations", "Integrations");
const settingsRoute = realRoute("/settings", SettingsPage);

const notFoundRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "$",
  component: () => <Navigate to="/" />,
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  bootstrapRoute,
  forgotPasswordRoute,
  resetPasswordRoute,
  dashboardRoute,
  salesListRoute,
  salesNewRoute,
  salesDetailRoute,
  purchasesRoute,
  inventoryRoute,
  catalogueRoute,
  pricingRoute,
  contactsRoute,
  accountingRoute,
  gstRoute,
  reportsRoute,
  integrationsRoute,
  settingsRoute,
  notFoundRoute,
]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
