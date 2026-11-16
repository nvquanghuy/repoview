import { defineConfig } from "@playwright/test";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";

const goRun =
  process.platform === "win32" ? "go run .." : "mise x go -- go run ..";

// The auth server keeps its state (sessions, shares) in a config dir; give
// every run a fresh one. The config file is evaluated in each worker too, so
// only create the dir once and pass it down through the environment.
if (!process.env.RV_TEST_CONFIG_DIR) {
  process.env.RV_TEST_CONFIG_DIR = fs.mkdtempSync(
    path.join(os.tmpdir(), "repoview-auth-test-"),
  );
}

const OWNER_PASSWORD = "test-owner-pw";

export default defineConfig({
  testDir: ".",
  testMatch: "*.spec.ts",
  timeout: 30_000,
  use: {
    headless: true,
  },
  projects: [
    {
      // No auth configured: today's local behaviour.
      name: "chromium",
      testMatch: "repoview.spec.ts",
      use: { browserName: "chromium", baseURL: "http://localhost:3123" },
    },
    {
      // Owner password set: login, sharing, share links.
      name: "auth",
      testMatch: "sharing.spec.ts",
      use: { browserName: "chromium", baseURL: "http://localhost:3124" },
    },
  ],
  webServer: [
    {
      command: `${goRun} -port 3123 -no-browser ../testdata`,
      url: "http://localhost:3123",
      reuseExistingServer: !process.env.CI,
      timeout: 15_000,
    },
    {
      command: `${goRun} -port 3124 -no-browser ../testdata`,
      // /login is public; "/" would redirect to it.
      url: "http://localhost:3124/login",
      reuseExistingServer: !process.env.CI,
      timeout: 15_000,
      env: {
        REPOVIEW_PASSWORD: OWNER_PASSWORD,
        REPOVIEW_CONFIG_DIR: process.env.RV_TEST_CONFIG_DIR,
      },
    },
  ],
});
