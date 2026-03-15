import { defineConfig } from "@playwright/test";

const command =
  process.platform === "win32"
    ? "go run .. -port 3123 -no-browser ../testdata"
    : "mise x go -- go run .. -port 3123 -no-browser ../testdata";

export default defineConfig({
  testDir: ".",
  testMatch: "*.spec.ts",
  timeout: 30_000,
  use: {
    baseURL: "http://localhost:3123",
    headless: true,
  },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
  webServer: {
    command,
    url: "http://localhost:3123",
    reuseExistingServer: !process.env.CI,
    timeout: 15_000,
  },
});
