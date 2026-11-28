import type { Locator, Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { firstDeploy, newApp, newProject } from "./steps";

/** Opens an entry of the files tab's folder: a volume, a folder, or a file in its dialog. */
async function open(files: Locator, name: string) {
  await files.getByRole("button", { name, exact: true }).click();
}

/** Picks an action from an entry's menu. */
async function action(page: Page, files: Locator, name: string, item: string) {
  await files.getByRole("button", { name: `Actions for ${name}` }).click();
  await page.getByRole("menuitem", { name: item }).click();
}

/** Opens a text file, checks what it holds and closes it. */
async function holds(page: Page, files: Locator, name: string, text: string) {
  await open(files, name);
  const dialog = page.getByRole("dialog", { name: new RegExp(`/${name}$`) });
  await expect(dialog.getByRole("textbox", { name: `Contents of ${name}` })).toHaveText(text);
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toBeHidden();
}

/** Goes to a folder from the top of the files tab, following its path. */
async function folder(files: Locator, ...path: string[]) {
  await files.getByRole("navigation", { name: "Folder" }).getByRole("button", { name: "Files", exact: true }).click();
  for (const name of path) await open(files, name);
}

test("upload a file into a volume, edit it, move it, delete it", async ({ page, account }) => {
  await newProject(page, account.project("files"));
  await newApp(page, "web", "nginx:1.29-alpine");
  await page.getByRole("tab", { name: "Settings" }).click();
  await page.getByRole("tab", { name: "Storage" }).click();
  const storage = page.getByRole("tabpanel", { name: "Storage" });
  await storage.getByRole("button", { name: "Add volume" }).click();
  await storage.getByLabel("Name").fill("site");
  await storage.getByLabel("Mount path").fill("/srv/site");
  await page.getByRole("button", { name: "Save volumes" }).click();
  await expect(page.getByText("Volumes saved")).toBeVisible();
  await firstDeploy(page);

  await page.getByRole("tab", { name: "Files" }).click();
  const files = page.getByRole("tabpanel", { name: "Files" });
  await open(files, "site");
  const chooser = page.waitForEvent("filechooser");
  await files.getByRole("button", { name: "Upload" }).click();
  await page.getByRole("menuitem", { name: "Files" }).click();
  await (await chooser).setFiles({ name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("first draft\n") });
  await expect(page.getByText("File uploaded")).toBeVisible();
  await holds(page, files, "notes.txt", "first draft");

  await action(page, files, "notes.txt", "Edit");
  const edit = page.getByRole("dialog", { name: "site/notes.txt" });
  await edit.getByRole("textbox", { name: "Contents of notes.txt" }).fill("final version\n");
  await edit.getByRole("button", { name: "Save", exact: true }).click();
  await expect(edit.getByText("Saved", { exact: true })).toBeVisible();
  await edit.getByRole("button", { name: "Close" }).click();
  await page.reload();
  await holds(page, files, "notes.txt", "final version");

  await files.getByRole("button", { name: "New folder" }).click();
  const mkdir = page.getByRole("dialog", { name: "New folder" });
  await mkdir.getByLabel("Name").fill("archive");
  await mkdir.getByRole("button", { name: "Create" }).click();
  await action(page, files, "notes.txt", "Move…");
  const move = page.getByRole("dialog", { name: "Move to" });
  await move.getByLabel("Folder (volume/path)").fill("site/archive");
  await move.getByRole("button", { name: "Move" }).click();
  await expect(page.getByText("Moved to site/archive")).toBeVisible();
  await expect(files.getByRole("button", { name: "archive", exact: true })).toBeVisible();
  await expect(files.getByRole("button", { name: "notes.txt", exact: true })).toBeHidden();
  await open(files, "archive");
  await holds(page, files, "notes.txt", "final version");

  // The running container sees the file where the volume is mounted.
  await folder(files, "Container filesystem", "srv", "site", "archive");
  await holds(page, files, "notes.txt", "final version");

  await folder(files, "site", "archive");
  await action(page, files, "notes.txt", "Delete…");
  await page.getByRole("alertdialog", { name: "Delete notes.txt?" }).getByRole("button", { name: "Delete" }).click();
  await expect(page.getByText("Deleted notes.txt")).toBeVisible();
  await expect(files.getByText(/^Empty folder\./)).toBeVisible();
  await folder(files, "Container filesystem", "srv", "site", "archive");
  await expect(files.getByText(/^Empty folder\./)).toBeVisible();
});
