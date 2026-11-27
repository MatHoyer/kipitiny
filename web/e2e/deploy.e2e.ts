import { expect, test } from "./fixtures";
import { deployImage, newProject } from "./steps";

test("deploy an app from an image, read its logs, then scale and rename it", async ({ page, account }) => {
  await newProject(page, account.project("shop"));
  await deployImage(page, "web", "traefik/whoami:v1.10");

  await page.getByRole("tab", { name: "Logs" }).click();
  await expect(page.getByRole("tabpanel", { name: "Logs" }).getByText(/Starting up on port 80/)).toBeVisible();

  await page.getByRole("tab", { name: "Deployments" }).click();
  await expect(page.getByRole("tabpanel", { name: "Deployments" }).getByRole("button", { name: /^traefik\/whoami:v1\.10 current/ })).toBeVisible();

  await page.getByRole("tab", { name: "Settings" }).click();
  const general = page.getByRole("tabpanel", { name: "General" });
  await general.getByRole("spinbutton", { name: "Replicas" }).fill("2");
  await page.getByRole("button", { name: "Save" }).click();
  await general.getByRole("textbox", { name: "Name" }).fill("front");
  await general.getByRole("button", { name: "Rename" }).click();
  await expect(page.getByRole("heading", { name: "front", level: 1 })).toBeVisible();
  await expect(page.getByRole("main").getByText("2/2")).toBeVisible({ timeout: 60_000 });
});
