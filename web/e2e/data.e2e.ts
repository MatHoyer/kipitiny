import type { Locator, Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { newProject } from "./steps";

// Each database's browser edits a value and saves it; the change has to come back after a reload, in the browser and
// through the console.

/** Adds a database of a kind to the open project and waits for it to be healthy. */
async function newDatabase(page: Page, title: string) {
  await page.getByRole("main").getByRole("button", { name: "New service" }).first().click();
  await page.getByRole("button", { name: new RegExp(`^${title} `) }).click();
  const form = page.getByRole("dialog", { name: `New ${title} database` });
  await form.getByLabel("Name").fill("db");
  await form.getByRole("button", { name: "Create & deploy" }).click();
  await expect(page.getByRole("heading", { name: "db", level: 1 })).toBeVisible();
  await expect(page.getByRole("main").getByText("healthy", { exact: true })).toBeVisible({ timeout: 90_000 });
}

/** Opens the data tab on a view (Browse or Console) and returns its panel. */
async function dataView(page: Page, view: "Browse" | "Console") {
  await page.getByRole("tab", { name: "Data", exact: true }).click();
  const panel = page.getByRole("tabpanel", { name: "Data" });
  await panel.getByRole("radio", { name: view }).click();
  return panel;
}

/** Allows writes in the open view, through its confirmation. */
async function allowWrites(page: Page, panel: Locator) {
  await panel.getByRole("radio", { name: "Allow writes" }).click();
  await page.getByRole("alertdialog", { name: "Allow writes?" }).getByRole("button", { name: "Allow writes" }).click();
  await expect(panel.getByRole("radio", { name: "Allow writes" })).toBeChecked();
}

/** Runs a command in the console (SQL, mongosh or Redis), in write mode when it changes data. */
async function run(page: Page, input: string, command: string, write = false) {
  const panel = await dataView(page, "Console");
  if (write) await allowWrites(page, panel);
  await panel.getByRole("textbox", { name: input }).fill(command);
  await panel.getByRole("button", { name: /^Run/ }).click();
  return panel;
}

/** Saves the browser's pending changes through the review dialog. */
async function save(page: Page, panel: Locator) {
  await panel.getByRole("button", { name: /^Save/ }).click();
  await page.getByRole("dialog", { name: "Review changes" }).getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("1 change saved")).toBeVisible();
}

for (const title of ["PostgreSQL", "MySQL"]) {
  test(`edit a ${title} row in the browser`, async ({ page, account }) => {
    await newProject(page, account.project(title.toLowerCase()));
    await newDatabase(page, title);
    const seeded = await run(page, "SQL", "create table items (id int primary key, name text); insert into items values (1, 'boots');", true);
    await expect(seeded.getByText(/^in [\d.]+ m?s$/)).toBeVisible(); // how long it took: it ran

    const browse = await dataView(page, "Browse");
    await expect(browse.getByRole("row", { name: "Open id 1" }).getByRole("cell", { name: "boots" })).toBeVisible();
    await allowWrites(page, browse);
    await browse.getByRole("row", { name: "id 1" }).getByRole("cell", { name: "boots" }).click();
    await browse.getByRole("textbox", { name: "Cell value" }).fill("sandals");
    await browse.getByRole("textbox", { name: "Cell value" }).press("Enter");
    await save(page, browse);

    await page.reload();
    const after = await dataView(page, "Browse");
    await expect(after.getByRole("row", { name: "Open id 1" }).getByRole("cell", { name: "sandals" })).toBeVisible();
    const query = await run(page, "SQL", "select name from items where id = 1;");
    await expect(query.getByRole("cell", { name: "sandals", exact: true })).toBeVisible();
  });
}

test("edit a MongoDB document in the browser", async ({ page, account }) => {
  await newProject(page, account.project("mongodb"));
  await newDatabase(page, "MongoDB");
  const seeded = await run(page, "mongosh command", 'db.items.insertOne({ _id: 1, name: "boots" })', true);
  await expect(seeded.getByText(/insertedId/)).toBeVisible();

  const browse = await dataView(page, "Browse");
  const doc = browse.getByRole("list", { name: "Documents" }).getByRole("listitem").filter({ hasText: '"boots"' });
  await expect(doc).toBeVisible();
  await allowWrites(page, browse);
  await doc.getByRole("button", { name: "Edit this document" }).click();
  const editor = page.getByRole("dialog", { name: "_id 1" });
  await editor.getByRole("textbox", { name: "Document" }).fill('{ "_id": 1, "name": "sandals" }');
  await editor.getByRole("button", { name: /^Done/ }).click();
  await save(page, browse);

  await page.reload();
  const after = await dataView(page, "Browse");
  await expect(after.getByRole("list", { name: "Documents" }).getByRole("listitem").filter({ hasText: '"sandals"' })).toBeVisible();
  const query = await run(page, "mongosh command", "db.items.findOne({ _id: 1 }).name");
  await expect(query.getByText("sandals", { exact: true })).toBeVisible();
});

test("edit a Redis key in the browser", async ({ page, account }) => {
  await newProject(page, account.project("redis"));
  await newDatabase(page, "Redis");
  const seeded = await run(page, "Redis command", "SET greeting hello", true);
  await expect(seeded.getByText("OK", { exact: true })).toBeVisible();

  const browse = await dataView(page, "Browse");
  await expect(browse.getByText("hello", { exact: true })).toBeVisible();
  await allowWrites(page, browse);
  await browse.getByRole("textbox", { name: "Value" }).fill("bonjour");
  await save(page, browse);

  await page.reload();
  const after = await dataView(page, "Browse");
  await expect(after.getByText("bonjour", { exact: true })).toBeVisible();
  const query = await run(page, "Redis command", "GET greeting");
  await expect(query.getByText('"bonjour"', { exact: true })).toBeVisible();
});
