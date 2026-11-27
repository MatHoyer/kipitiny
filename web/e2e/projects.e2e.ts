import { expect, test } from "./fixtures";
import { deployImage, newProject, sidebar } from "./steps";

test("switch between projects, each with its own services", async ({ page, account }) => {
  const shop = account.project("shop");
  const blog = account.project("blog");
  await newProject(page, shop);
  await deployImage(page, "web", "traefik/whoami:v1.10");
  await newProject(page, blog);
  await expect(page.getByText("No services yet")).toBeVisible();

  // From the blog, back to the shop through the Projects page, then to the blog from the shop's app.
  await sidebar(page, "Projects");
  await page.getByRole("main").getByRole("link", { name: shop, exact: true }).click();
  await expect(page.getByRole("heading", { name: shop, level: 1 })).toBeVisible();
  await page.getByRole("tabpanel", { name: "Services" }).getByRole("link", { name: /^web/ }).click();
  await expect(page.getByRole("heading", { name: "web", level: 1 })).toBeVisible();
  await sidebar(page, "Projects");
  await page.getByRole("main").getByRole("link", { name: blog, exact: true }).click();
  await expect(page.getByRole("heading", { name: blog, level: 1 })).toBeVisible();
  await expect(page.getByText("No services yet")).toBeVisible();
});
