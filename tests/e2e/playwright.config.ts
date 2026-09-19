import { defineConfig, devices } from "@playwright/test";

// Runs against apps/web's real build output (vite build + vite preview),
// not the dev server — closer to what actually ships, and LoginPage fails
// open on GET /auth/bootstrap (see LoginPage.tsx), so it renders a usable
// sign-in form with no backend running at all. That's what makes this a
// viable static smoke test with no server/Postgres dependency.
export default defineConfig({
  testDir: "./specs",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    // vite preview only binds the ::1 (IPv6) loopback by default —
    // 127.0.0.1 gets connection-refused, "localhost" resolves either way.
    baseURL: "http://localhost:4319",
    trace: "on-first-retry",
  },
  webServer: {
    command: "npm run build && npm run preview -- --port 4319 --strictPort",
    cwd: "../../apps/web",
    url: "http://localhost:4319",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
