import { expect, test } from "./fixtures";
import { deployImage, newProject, sidebar } from "./steps";

// Error tracking and analytics have no journey here: both are served by the analytics server, which the manager
// only runs behind Traefik (off in these tests) and reaches on the proxy network, a network inside the test's
// Docker daemon that the manager, run beside the browser, can't reach.

test("set a project's preview domain, open a preview the way CI does, see it run, then close it", async ({ page, account }) => {
  const project = account.project("shop");
  await newProject(page, project);
  const id = new URL(page.url()).pathname.split("/").pop()!;
  await deployImage(page, "web", "traefik/whoami:v1.10");

  await sidebar(page, "Projects");
  await page.getByRole("main").getByRole("link", { name: project, exact: true }).click();
  await page.getByRole("tab", { name: "Previews" }).click();
  const previews = page.getByRole("tabpanel", { name: "Previews" });
  await expect(previews.getByText("No previews")).toBeVisible();
  await previews.getByRole("textbox", { name: "Base domain" }).fill(`${account.username}.localhost`);
  const unsaved = page.getByRole("region", { name: "Unsaved changes" });
  await unsaved.getByRole("button", { name: "Save" }).click();
  await expect(unsaved).toHaveCount(0);

  // CI's `kipitiny preview --name pr-1 --branch feat/checkout --commit abc1234`.
  const res = await page.request.put(`/api/projects/${id}/previews/pr-1`, { data: { branch: "feat/checkout", commit: "abc1234" } });
  expect(res.status(), await res.text()).toBe(202);

  await expect(previews.getByRole("link", { name: "pr-1", exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(previews.getByText("feat/checkout")).toBeVisible();
  await expect(previews.getByText("all running")).toBeVisible({ timeout: 90_000 });

  await previews.getByRole("button", { name: "Close preview pr-1" }).click();
  await page.getByRole("alertdialog", { name: "Close preview pr-1?" }).getByRole("button", { name: "Delete" }).click();
  await expect(previews.getByText("No previews")).toBeVisible();
});
