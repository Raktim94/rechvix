import { test, expect, type Page } from "@playwright/test";

// Fictional API fixtures exercise the UI contract only: a logged-in user can reach the
// invoice form in one click, and existing invoices stay openable.
async function stub(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("rechvix:session-hint", JSON.stringify({ organisationId: "org", userId: "user" }));
    localStorage.setItem("rechvix:language", "en");
  });
  const company = { ID: "company", LegalName: "Fixture Industries", CountryCode: "IN", GSTStateCode: "29", BaseCurrencyCode: "INR", USCollectionRegions: [] };
  const money = (amount: string) => ({ amount, currency: "INR" });
  const doc = (ID: string, Status: string, n: string) => ({ ID, Status, DocumentType: "TAX_INVOICE", DocumentNumber: n, CustomerPartyID: "11111111-1111-4111-8111-111111111111", LegalEntityID: "company", CurrencyCode: "INR", GrandTotalAmount: money("118"), IssueDate: "2026-11-01T00:00:00Z", CreatedAt: "2026-11-01T00:00:00Z" });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/reports/dashboard") body = { TodaySales: money("0"), TodayCollections: money("0"), TodayPurchases: money("0"), OutstandingReceivable: money("0"), OutstandingPayable: money("0"), CurrentStockValue: money("0"), LowStockCount: 0, OverdueReceivable: money("0") };
    else if (path === "/reports/sales/summary") body = { title: "Sales", headers: ["Day", "Documents", "Taxable", "Tax", "Total"], rows: [] };
    else if (path === "/organisation") body = { ID: "org", Name: "Fixture Industries", DefaultCurrencyCode: "INR" };
    else if (path === "/legal-entities") body = { legal_entities: [company] };
    else if (path === "/branches") body = { branches: [{ ID: "branch", LegalEntityID: "company", Name: "Main" }] };
    else if (path === "/branches/branch/warehouses") body = { warehouses: [{ ID: "warehouse", BranchID: "branch", Name: "Main" }] };
    else if (path === "/reference/currencies") body = [{ code: "INR", name: "Indian rupee" }];
    else if (path === "/pricing/price-lists/ensure-default") body = { ID: "price-list" };
    else if (path === "/contacts/parties") body = { parties: [{ ID: "11111111-1111-4111-8111-111111111111", LegalName: "Fixture Customer" }] };
    else if (path === "/contacts/parties/11111111-1111-4111-8111-111111111111") body = { ID: "11111111-1111-4111-8111-111111111111", LegalName: "Fixture Customer", PartyType: "CUSTOMER" };
    else if (path === "/contacts/parties/22222222-2222-4222-8222-222222222222") body = { ID: "22222222-2222-4222-8222-222222222222", LegalName: "Fixture Supplier", PartyType: "SUPPLIER" };
    else if (path === "/contacts/parties/11111111-1111-4111-8111-111111111111/tax-registrations") body = { tax_registrations: [] };
    else if (path === "/sales/documents") body = { documents: [doc("d1", "DRAFT", "DRAFT-1"), doc("d2", "FINALIZED", "INV-0002")] };
    else if (path === "/sales/documents/d2") body = { document: doc("d2", "FINALIZED", "INV-0002"), lines: [] };
    else if (path.startsWith("/accounting/parties/") && path.endsWith("/ageing")) body = { Current: money("0"), Days1To30: money("0"), Days31To60: money("0"), Days61To90: money("0"), Days90Plus: money("0"), Total: money("0") };
    await route.fulfill({ json: body });
  });
}

for (const width of [375, 768, 1440]) {
  test(`Create Invoice button reaches the form at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await stub(page);

    await page.goto("/");
    await page.waitForLoadState("networkidle");
    const dash = page.getByRole("link", { name: "Create Invoice" });
    await expect(dash.first()).toBeVisible();
    await page.screenshot({ path: `test-results/create-invoice-dashboard-${width}.png` });
    // Dashboard header button (the page's main landmark), not just the top bar.
    await page.getByRole("main").getByRole("link", { name: "Create Invoice" }).first().click();
    await expect(page).toHaveURL(/\/sales\/new$/);
    await expect(page.getByPlaceholder("Search customer by name or phone…")).toBeVisible();

    // Top-bar action is available from other screens too.
    await page.goto("/sales");
    await page.getByRole("link", { name: "Create Invoice" }).first().click();
    await expect(page).toHaveURL(/\/sales\/new$/);

    // Existing invoice records still open.
    await page.goto("/sales");
    await page.getByText("INV-0002").first().click();
    await expect(page).toHaveURL(/\/sales\/d2$/);
  });
}

test("contact page offers Create Invoice with the customer pre-filled, not for suppliers", async ({ page }) => {
  await stub(page);
  await page.goto("/contacts/11111111-1111-4111-8111-111111111111");
  await expect(page.getByRole("heading", { name: "Fixture Customer" })).toBeVisible();
  await page.getByRole("main").getByRole("link", { name: "Create Invoice" }).click();
  await expect(page).toHaveURL(/\/sales\/new\?.*customer=11111111-1111-4111-8111-111111111111/);
  await expect(page.getByText("Fixture Customer").first()).toBeVisible();

  await page.goto("/contacts/22222222-2222-4222-8222-222222222222");
  await expect(page.getByRole("heading", { name: "Fixture Supplier" })).toBeVisible();
  await expect(page.getByRole("main").getByRole("link", { name: "Create Invoice" })).toHaveCount(0);
});
