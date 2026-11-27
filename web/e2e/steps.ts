import type { Page } from "@playwright/test";

import { expect } from "./fixtures";

/** Follows a sidebar link, opening the sidebar first where it's collapsed (on a phone). */
export async function sidebar(page: Page, name: string | RegExp) {
  const nav = page.getByRole("link", { name, exact: true, disabled: false }).or(page.getByRole("button", { name }));
  if (!(await nav.filter({ visible: true }).count())) await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await nav.filter({ visible: true }).first().click();
}

/** Creates a project from the Projects page and opens it. */
export async function newProject(page: Page, name: string) {
  await sidebar(page, "Projects");
  await page.getByRole("main").getByRole("button", { name: "New project" }).first().click();
  const dialog = page.getByRole("dialog", { name: "New project" });
  await dialog.getByLabel("Name").fill(name);
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
}

/** Adds an app from a Docker image to the open project, deploys it and waits for it to run. */
export async function deployImage(page: Page, name: string, image: string) {
  await page.getByRole("main").getByRole("button", { name: "New service" }).first().click();
  await page.getByRole("button", { name: /^Docker image/ }).click();
  const form = page.getByRole("dialog", { name: "New app from a Docker image" });
  await form.getByLabel("Name").fill(name);
  await form.getByLabel("Image").fill(image);
  await form.getByRole("button", { name: "Create & deploy" }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  await expect(page.getByRole("main").getByText("1/1")).toBeVisible({ timeout: 90_000 });
}
