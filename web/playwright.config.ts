import { defineConfig, devices } from "@playwright/test";

const port = 8093;
export const SETUP_TOKEN = "e2e-setup-token";

// UI tests drive the real manager (Go binary, embedded build, SQLite) in a real browser: WebKit at phone size and
// Chromium at desktop size. Run them with `make e2e`, which builds bin/kipitiny first. Each run starts the manager on a
// fresh database. It deploys on the Docker daemon of DOCKER_HOST: one of its own, since its reconciler removes the
// kipitiny containers it doesn't know (make e2e refuses a daemon that has any).
export default defineConfig({
  testDir: "e2e",
  testMatch: "*.e2e.ts",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0, // a flaky test is a bug to fix, not to retry
  timeout: 120_000, // a deploy pulls an image
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  globalSetup: "./e2e/global-setup.ts",
  use: {
    baseURL: `http://localhost:${port}`,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "iphone", use: { ...devices["iPhone 13"] } },
    { name: "desktop", use: { ...devices["Desktop Chrome"] } },
  ],
  webServer: {
    command: "rm -rf e2e-data && exec ../bin/kipitiny",
    url: `http://localhost:${port}/api/health`,
    reuseExistingServer: false,
    timeout: 120_000,
    env: {
      KIPITINY_ADDR: `127.0.0.1:${port}`,
      KIPITINY_DATA_DIR: "e2e-data",
      KIPITINY_SETUP_TOKEN: SETUP_TOKEN,
      KIPITINY_TRAEFIK: "false",
      KIPITINY_UPDATE_CHECK: "off",
      KIPITINY_LOG_LEVEL: "warn",
    },
  },
});
