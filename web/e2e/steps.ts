import type { Page } from "@playwright/test";

import { expect } from "./fixtures";

/** Follows a sidebar link, opening the sidebar first where it's collapsed (on a phone). */
export async function sidebar(page: Page, name: string | RegExp) {
  const nav = page.getByRole("link", { name, exact: true, disabled: false }).or(page.getByRole("button", { name }));
  const toggle = page.getByRole("button", { name: "Toggle sidebar" });
  // The layout first: counting links on a page still loading would collapse a desktop's open sidebar.
  await expect(toggle).toBeVisible();
  if (!(await nav.filter({ visible: true }).count())) await toggle.click();
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

/** Adds an app from a Docker image to the open project, deploys it and waits for the deploy to finish. */
export async function deployImage(page: Page, name: string, image: string) {
  await page.getByRole("main").getByRole("button", { name: "New service" }).first().click();
  await page.getByRole("button", { name: /^Docker image/ }).click();
  const form = page.getByRole("dialog", { name: "New app from a Docker image" });
  await form.getByLabel("Name").fill(name);
  await form.getByLabel("Image").fill(image);
  await form.getByRole("button", { name: "Create & deploy" }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  await expect(page.getByRole("main").getByText("1/1")).toBeVisible({ timeout: 90_000 });
  // The replica runs before the deploy ends (it waits for the replica to stay up), and the deploy holds the service.
  await expect(page.getByRole("button", { name: "Deploy", exact: true })).toBeEnabled({ timeout: 30_000 });
}

/** Adds an app from a Docker image to the open project without deploying it, to set it up first. */
export async function newApp(page: Page, name: string, image: string) {
  await page.getByRole("main").getByRole("button", { name: "New service" }).first().click();
  await page.getByRole("button", { name: /^Docker image/ }).click();
  const form = page.getByRole("dialog", { name: "New app from a Docker image" });
  await form.getByLabel("Name").fill(name);
  await form.getByLabel("Image").fill(image);
  await form.getByRole("button", { name: "Create", exact: true }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
}

/** Deploys the open app for the first time and waits for the deploy to finish. */
export async function firstDeploy(page: Page) {
  await page.getByRole("button", { name: "Deploy", exact: true }).click();
  await expect(page.getByRole("main").getByText("1/1")).toBeVisible({ timeout: 90_000 });
  await expect(page.getByRole("button", { name: "Deploy", exact: true })).toBeEnabled({ timeout: 30_000 });
}
