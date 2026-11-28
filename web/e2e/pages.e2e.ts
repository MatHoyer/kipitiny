import type { Locator, Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { deployImage, newProject, sidebar } from "./steps";

// Every page and every tab, opened once: a render error shows the router's error page instead of throwing, so the
// page fixture's uncaught-error check alone wouldn't see it. The journeys test what pages do; this, that they open.

/** Waits for what's loading in where to settle, then checks the page didn't crash rendering it. */
async function renders(page: Page, where: Locator) {
  await expect(where).toBeVisible();
  await expect(where.getByRole("status", { name: "Loading" })).toHaveCount(0, { timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "Unexpected Application Error!" })).toHaveCount(0);
}

/** Opens every tab of the page's first tab list, in order. */
async function everyTab(page: Page, skip: string[] = []) {
  const tabs = page.getByRole("main").getByRole("tablist").first().getByRole("tab");
  await expect(tabs.first()).toBeVisible();
  for (const name of await tabs.allInnerTexts()) {
    if (skip.includes(name)) continue;
    await page.getByRole("main").getByRole("tablist").first().getByRole("tab", { name, exact: true }).click();
    await renders(page, page.getByRole("tabpanel", { name, exact: true }));
  }
}

/** Adds a database of a kind to the open project, waits for it to be healthy and opens it. */
async function newDatabase(page: Page, title: string) {
  await page.getByRole("main").getByRole("button", { name: "New service" }).first().click();
  await page.getByRole("button", { name: new RegExp(`^${title} `) }).click();
  const form = page.getByRole("dialog", { name: `New ${title} database` });
  await form.getByLabel("Name").fill("db");
  await form.getByRole("button", { name: "Create & deploy" }).click();
  await expect(page.getByRole("heading", { name: "db", level: 1 })).toBeVisible();
  await expect(page.getByRole("main").getByText("healthy", { exact: true })).toBeVisible({ timeout: 90_000 });
}

const pages = [
  "/projects",
  "/map",
  "/backups",
  "/account",
  "/preferences",
  "/servers",
  "/networks",
  "/domains",
  "/storage",
  "/git-providers",
  "/registries",
  "/password-managers",
  "/tokens",
  "/notifications",
  "/email",
  "/analytics",
  "/appearance",
  "/cleanup",
  "/users",
  "/sso",
  "/audit",
  "/about",
];

test("every page opens", async ({ page, account: _ }) => {
  for (const path of pages) {
    await page.goto(path);
    await renders(page, page.getByRole("main").getByRole("heading", { level: 1 }));
  }
  // The local server's own page, from the servers list.
  await page.goto("/servers");
  await page.getByRole("main").getByRole("link").filter({ hasText: /This server/ }).first().click();
  await renders(page, page.getByRole("main").getByRole("heading", { level: 1 }));
});

test("every tab of a project and of an app opens, the 3D map too", async ({ page, account }) => {
  await newProject(page, account.project("shop"));
  await deployImage(page, "web", "traefik/whoami:v1.10");
  await everyTab(page, ["Terminal"]); // a live shell, its own journey

  await sidebar(page, "Projects");
  await page.getByRole("main").getByRole("link", { name: account.project("shop"), exact: true }).click();
  await expect(page.getByRole("heading", { name: account.project("shop"), level: 1 })).toBeVisible();
  await everyTab(page);
  await page.getByRole("main").getByRole("tablist").first().getByRole("tab", { name: "Map", exact: true }).click();
  await page.getByRole("tabpanel", { name: "Map" }).getByRole("button", { name: "3D", exact: true }).click();
  await renders(page, page.getByRole("tabpanel", { name: "Map" }));
});

for (const title of ["PostgreSQL", "MySQL", "MongoDB", "Redis"]) {
  test(`every tab of a ${title} database opens, its data browsed`, async ({ page, account }) => {
    await newProject(page, account.project(title.toLowerCase()));
    await newDatabase(page, title);
    await everyTab(page, ["Terminal"]);
    // The data tab opens on its browser; the console is the backup journey's.
    await page.getByRole("tab", { name: "Data", exact: true }).click();
    const data = page.getByRole("tabpanel", { name: "Data" });
    await data.getByRole("radio", { name: "Browse" }).click();
    await renders(page, data);
  });
}
