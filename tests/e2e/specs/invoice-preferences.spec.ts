import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// Fictional API fixtures exercise UI contracts; PostgreSQL tests cover posting/RLS.
for (const width of [375, 768, 1440]) {
 test(`invoice preferences at ${width}px`, async ({ page }) => {
  page.on("pageerror", error => console.error(error.message));
  page.on("console", message => { if (message.type() === "error") console.error(message.text()); });
  await page.setViewportSize({ width, height: 1000 });
  await page.addInitScript(() => {
   localStorage.setItem("rechvix:session-hint", JSON.stringify({ organisationId:"org", userId:"user" }));
   localStorage.setItem("rechvix:language", "en");
  });
  let savedLanguage = "";
  let posted: Record<string, unknown> = {};
  const company = { ID:"company", LegalName:"Fixture Industries", CountryCode:"IN", GSTStateCode:"29", BaseCurrencyCode:"INR", USCollectionRegions:[] };
  await page.route("**/api/v1/**", async route => {
   const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
   let body: unknown = [];
   if (path === "/organisation") body = { ID:"org", Name:"Fixture Industries", DefaultCurrencyCode:"INR" };
   else if (path === "/legal-entities") body = { legal_entities:[{ ...company, InvoiceLanguage:savedLanguage }] };
   else if (path === "/branches") body = { branches:[{ ID:"branch", LegalEntityID:"company", Name:"Main" }] };
   else if (path === "/branches/branch/warehouses") body = { warehouses:[{ ID:"warehouse", BranchID:"branch", Name:"Main" }] };
   else if (path === "/reference/currencies") body = [{ code:"INR", name:"Indian rupee" },{ code:"USD", name:"US dollar" }];
   else if (path === "/legal-entities/company/invoice-language") { savedLanguage = route.request().postDataJSON().language; body = company; }
   else if (path === "/pricing/price-lists/ensure-default") body = { ID:"price-list" };
   else if (path === "/contacts/parties") body = { parties:[{ ID:"customer", LegalName:"Fixture Customer" }] };
   else if (path === "/contacts/parties/customer/tax-registrations") body = { tax_registrations:[] };
   else if (path === "/sales/documents" && route.request().method() === "POST") { posted = route.request().postDataJSON(); body = { ID:"invoice", Status:"DRAFT" }; }
   else if (path === "/sales/documents/invoice") body = { document:{ ID:"invoice", Status:"DRAFT", CurrencyCode:posted.currency_code, ExchangeRate:posted.exchange_rate, InvoiceLanguage:posted.invoice_language }, lines:[] };
   else if (path === "/accounting/parties/customer/ageing") body = { Total:{ amount:"0", currency:"INR" } };
   else if (path === "/sales/billing-lookup") body = { results:[] };
   await route.fulfill({ json:body });
  });
  await page.goto("/settings");
  await page.getByLabel("New invoice currency (this browser)").selectOption("USD");
  await expect(page.getByText("Currency preference saved.", { exact:true })).toBeVisible();
  await page.getByLabel("Default invoice language").selectOption("hi");
  await page.getByRole("button", { name:"Save invoice language", exact:true }).click();
  await expect(page.getByText("Saved.", { exact:true })).toBeVisible();
  expect(savedLanguage).toBe("hi");
  await page.goto("/sales/new");
  await expect(page.getByLabel("Invoice currency", { exact:true })).toHaveValue("USD");
  await page.getByLabel("Invoice language", { exact:true }).selectOption("hi");
  await page.getByPlaceholder("Search customer by name or phone…").fill("Fixture");
  await page.getByRole("menuitem", { name:"Fixture Customer" }).click();
  const start = page.getByRole("button", { name:"Start sale", exact:true });
  await expect(start).toBeDisabled();
  await page.getByLabel("Exchange rate: INR per 1 USD").fill("83.50");
  await expect(start).toBeEnabled();
  const audit = await new AxeBuilder({ page }).include("#invoice-language").include("#invoice-currency").include("#invoice-rate").analyze();
  expect(audit.violations.filter(v => v.impact === "serious" || v.impact === "critical")).toEqual([]);
  await page.screenshot({ path:`test-results/invoice-preferences-${width}.png`, fullPage:true });
  await start.click();
  await expect(page.getByLabel("Invoice currency", { exact:true })).toBeDisabled();
  expect(posted).toMatchObject({ currency_code:"USD", base_currency_code:"INR", exchange_rate:"83.50", invoice_language:"hi" });
 });
}
