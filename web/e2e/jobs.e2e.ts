import { expect, test } from "./fixtures";
import { deployImage, newProject } from "./steps";

test("add a job to an app, run it and read its log", async ({ page, account }) => {
  await newProject(page, account.project("shop"));
  await deployImage(page, "web", "nginx:1.27-alpine");

  // The shell computes the output, so the log can't be mistaken for the command.
  const n = Math.floor(Math.random() * 1e6);
  await page.getByRole("tab", { name: "Jobs" }).click();
  const jobs = page.getByRole("tabpanel", { name: "Jobs" });
  await jobs.getByRole("button", { name: "New job" }).click();
  const form = page.getByRole("dialog", { name: "New job" });
  await form.getByLabel("Name", { exact: true }).fill("report");
  await form.getByLabel("Command").fill(`echo report-$((${n} + 1))`);
  await form.getByRole("button", { name: "Add job" }).click();
  await expect(form).toBeHidden();

  await jobs.getByRole("button", { name: "Run now" }).click();
  await expect(jobs.getByText(/Succeeded in/)).toBeVisible({ timeout: 60_000 });
  await expect(jobs.getByText(`report-${n + 1}`, { exact: true })).toBeVisible();
});
