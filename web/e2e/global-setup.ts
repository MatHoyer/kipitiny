import { request, type FullConfig } from "@playwright/test";

import { SETUP_TOKEN } from "../playwright.config";
import { ADMIN, PASSWORD } from "./fixtures";

// Creates the first admin on the run's fresh database; tests get their accounts from its invites.
export default async function globalSetup(config: FullConfig) {
  const api = await request.newContext({ baseURL: config.projects[0].use.baseURL });
  const res = await api.post("/api/auth/setup", { data: { setupToken: SETUP_TOKEN, username: ADMIN, password: PASSWORD } });
  if (!res.ok()) throw new Error(`setup: ${res.status()} ${await res.text()}`);
  await api.dispose();
}
