import { expect, test } from "./fixtures";
import { sidebar } from "./steps";

// Mailpit: one small image, no outside service, and a healthcheck of its own.
test("install a one-click template into a new project and see its service healthy", async ({ page, account }) => {
  const name = account.project("mail");
  await sidebar(page, "Projects");
  await page.getByRole("main").getByRole("button", { name: "From a template" }).first().click();
  const dialog = page.getByRole("dialog", { name: "App templates" });
  await dialog.getByRole("textbox", { name: "Search" }).fill("mail");
  await dialog.getByRole("button", { name: /^Mailpit/ }).click();

  const form = page.getByRole("dialog", { name: "Mailpit" });
  await form.getByRole("textbox", { name: "Project name" }).fill(name);
  await form.getByRole("textbox", { name: "Domain" }).fill(`${account.username}-mail.localhost`);
  await form.getByLabel("Web UI password").fill("e2e-password");
  await form.getByRole("button", { name: "Preview" }).click();
  await form.getByRole("button", { name: "Install and deploy" }).click();

  const installing = page.getByRole("dialog", { name: "Installing Mailpit" });
  await expect(installing.getByText("Everything is up.")).toBeVisible({ timeout: 90_000 });
  await installing.getByRole("button", { name: "Open the project" }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();

  await page.getByRole("main").getByRole("link", { name: /mailpit/ }).first().click();
  await expect(page.getByRole("heading", { name: "mailpit", level: 1 })).toBeVisible();
  await expect(page.getByRole("main").getByText("healthy", { exact: true })).toBeVisible({ timeout: 60_000 });
});
