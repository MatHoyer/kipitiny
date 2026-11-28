import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { firstDeploy, newApp, newProject } from "./steps";

const SECRET = "s3cret-e2e-value";

/** Adds an entry from the environment tab's Add dialog. */
async function addEntry(page: Page, kind: "Variable" | "Secret", name: string, value: string) {
  await page.getByRole("tabpanel", { name: "Environment" }).getByRole("button", { name: "Add" }).click();
  await page.getByRole("dialog", { name: "Add to the environment" }).getByRole("button", { name: new RegExp(`^${kind}`) }).click();
  const form = page.getByRole("dialog", { name: `New ${kind.toLowerCase()}` });
  await form.getByLabel("Name").fill(name);
  await form.getByRole("textbox", { name: "Value" }).fill(value);
  await form.getByRole("button", { name: `Add ${kind.toLowerCase()}` }).click();
  await expect(form).toBeHidden();
}

test("add a variable and a secret, deploy, and the app gets both", async ({ page, account }) => {
  await newProject(page, account.project("env"));
  await newApp(page, "web", "nginx:1.29-alpine");

  await page.getByRole("tab", { name: "Environment" }).click();
  const env = page.getByRole("tabpanel", { name: "Environment" });
  await addEntry(page, "Variable", "GREETING", "hello from e2e");
  await addEntry(page, "Secret", "API_TOKEN", SECRET);
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByText("Environment saved")).toBeVisible();

  // Saved, the secret reads back masked: in what the browser receives, and in both views (the list masks whatever it
  // receives).
  const service = page.waitForResponse((r) => r.request().method() === "GET" && /\/api\/services\/[^/?]+$/.test(r.url()));
  await page.reload();
  expect((await (await service).json()).env).toMatchObject({ API_TOKEN: "********" });
  await expect(env.getByRole("listitem").filter({ hasText: "GREETING" })).toContainText("hello from e2e");
  await expect(env.getByRole("listitem").filter({ hasText: "API_TOKEN" })).toContainText("••••••••");
  await expect(env.getByText(SECRET)).toHaveCount(0);
  await env.getByRole("radio", { name: "Text" }).click();
  await expect(env.getByRole("textbox", { name: "Environment" })).toHaveValue(/^GREETING=hello from e2e$/m);
  await expect(env.getByRole("textbox", { name: "Environment" })).not.toHaveValue(new RegExp(SECRET));

  // The app prints its environment as it starts.
  await page.getByRole("tab", { name: "Settings" }).click();
  await page.getByRole("tab", { name: "Advanced" }).click();
  await page.getByRole("tabpanel", { name: "Advanced" }).getByLabel("Command").fill(`sh -c 'env; exec nginx -g "daemon off;"'`);
  await page.getByRole("button", { name: "Save container settings" }).click();
  await expect(page.getByText("Container settings saved")).toBeVisible();
  await firstDeploy(page);

  await page.getByRole("tab", { name: "Logs" }).click();
  const logs = page.getByRole("tabpanel", { name: "Logs" });
  await expect(logs.getByText("GREETING=hello from e2e")).toBeVisible();
  await expect(logs.getByText(`API_TOKEN=${SECRET}`)).toBeVisible();
});
