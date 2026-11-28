import { request, type FullConfig } from "@playwright/test";

import { SETUP_TOKEN } from "../playwright.config";
import { ADMIN, ANALYTICS_DOMAIN, PASSWORD } from "./fixtures";

// Creates the first admin on the run's fresh database; tests get their accounts from its invites. Turns analytics on,
// for the server's apps: one setting every test shares, so no test turns it off.
export default async function globalSetup(config: FullConfig) {
  const api = await request.newContext({ baseURL: config.projects[0].use.baseURL });
  const res = await api.post("/api/auth/setup", { data: { setupToken: SETUP_TOKEN, username: ADMIN, password: PASSWORD } });
  if (!res.ok()) throw new Error(`setup: ${res.status()} ${await res.text()}`);
  const on = await api.put("/api/analytics", { data: { enabled: true, domain: ANALYTICS_DOMAIN, retentionDays: 365 } });
  if (!on.ok()) throw new Error(`analytics: ${on.status()} ${await on.text()}`);
  await api.dispose();
}
