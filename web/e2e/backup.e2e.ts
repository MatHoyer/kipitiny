import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { newProject } from "./steps";

/** Runs SQL in the open database's console, in write mode when it changes data. */
async function sql(page: Page, query: string, write = false) {
  await page.getByRole("tab", { name: "Data" }).click();
  const panel = page.getByRole("tabpanel", { name: "Data" });
  await panel.getByRole("radio", { name: "Console" }).click();
  const writes = panel.getByRole("radio", { name: "Allow writes" });
  if (write !== (await writes.isChecked())) {
    if (write) {
      await writes.click();
      await page.getByRole("alertdialog", { name: "Allow writes?" }).getByRole("button", { name: "Allow writes" }).click();
    } else await panel.getByRole("radio", { name: "Read only" }).click();
  }
  await expect(writes).toBeChecked({ checked: write });
  await panel.getByRole("textbox", { name: "SQL" }).fill(query);
  await panel.getByRole("button", { name: /^Run/ }).click();
  return panel;
}

test("back up a database, lose a row, restore it", async ({ page, account }) => {
  await newProject(page, account.project("shop"));
  await page.getByRole("main").getByRole("button", { name: "New service" }).first().click();
  await page.getByRole("button", { name: /^PostgreSQL/ }).click();
  const form = page.getByRole("dialog", { name: "New PostgreSQL database" });
  await form.getByLabel("Name").fill("db");
  await form.getByRole("button", { name: "Create & deploy" }).click();
  await expect(page.getByRole("main").getByText("healthy", { exact: true })).toBeVisible({ timeout: 90_000 });

  await sql(page, "create table orders (item text); insert into orders values ('boots');", true);
  await expect(page.getByRole("tabpanel", { name: "Data" }).getByRole("button", { name: /^Run/ })).toBeEnabled();

  await page.getByRole("tab", { name: "Backups" }).click();
  await page.getByRole("button", { name: "Back up now" }).click();
  const backup = page.getByRole("dialog");
  await backup.getByRole("button", { name: /^Back up/ }).click();
  await expect(backup.getByRole("heading", { name: /^Backed up/ })).toBeVisible({ timeout: 60_000 });
  await backup.getByRole("button", { name: "Close" }).first().click();

  await sql(page, "delete from orders;", true);
  const lost = await sql(page, "select count(*) as left from orders;");
  await expect(lost.getByRole("cell", { name: "0", exact: true })).toBeVisible();

  await page.getByRole("tab", { name: "Backups" }).click();
  await page.getByRole("tabpanel", { name: "Backups" }).getByRole("link").first().click();
  await page.getByRole("button", { name: "Restore", exact: true }).click();
  const confirm = page.getByRole("alertdialog");
  await confirm.getByRole("textbox").fill("db");
  await confirm.getByRole("button", { name: "Restore" }).click();
  await expect(page.getByText("Restore started")).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: /^succeeded/ })).toBeVisible({ timeout: 60_000 });

  await page.getByRole("definition").getByRole("link", { name: "db", exact: true }).click();
  const data = await sql(page, "select item from orders;");
  await expect(data.getByRole("cell", { name: "boots" })).toBeVisible();
});
