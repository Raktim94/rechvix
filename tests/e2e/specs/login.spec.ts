import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// No backend is running here — LoginPage's GET /auth/bootstrap fails and
// is caught (fails open), so setupAvailable stays false and the "New to
// Rechvix?" link never renders. Tab order below reflects that.

test("login page has no serious or critical accessibility violations", async ({ page }) => {
  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();

  const results = await new AxeBuilder({ page }).analyze();
  const seriousOrCritical = results.violations.filter((v) => v.impact === "serious" || v.impact === "critical");

  expect(seriousOrCritical, JSON.stringify(seriousOrCritical, null, 2)).toEqual([]);
});

test("keyboard-only tab order moves through the login form in DOM order", async ({ page }) => {
  await page.goto("/login");
  // Playwright's synthetic first Tab is swallowed unless the page/window
  // itself already has focus (no click involved — this isn't focusing any
  // form control, just what a real browser window already has by the time
  // a user starts tabbing).
  await page.evaluate(() => window.focus());

  await page.keyboard.press("Tab");
  await expect(page.getByLabel("Email", { exact: true })).toBeFocused();

  await page.keyboard.press("Tab");
  // exact: true — plain getByLabel("Password") also substring-matches the
  // adjacent "Show password" toggle button's aria-label.
  await expect(page.getByLabel("Password", { exact: true })).toBeFocused();

  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "Show password" })).toBeFocused();

  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "Forgot password?" })).toBeFocused();

  await page.keyboard.press("Tab");
  const submit = page.getByRole("button", { name: "Sign in" });
  await expect(submit).toBeFocused();
  await expect(submit).toBeVisible();
});
