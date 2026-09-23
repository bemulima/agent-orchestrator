import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  // Local Codex hosts provide Chrome but not necessarily Playwright's downloaded
  // headless shell. CI continues to use its provisioned Playwright browser.
  use: { baseURL: "http://127.0.0.1:3011", trace: "retain-on-failure", channel: process.env.PLAYWRIGHT_CHANNEL || (process.platform === "darwin" ? "chrome" : undefined) },
  webServer: {
    command: "ORCHESTRATOR_API_URL=http://127.0.0.1:8080 NEXT_PUBLIC_ORCHESTRATOR_EVENTS_URL=http://127.0.0.1:8080/api/v1/events npm run dev -- -p 3011",
    url: "http://127.0.0.1:3011",
    reuseExistingServer: true,
    timeout: 120_000,
  },
});
