import { expect, test } from "./fixtures";

test("on a phone the sidebar opens on demand and no page scrolls sideways", async ({ page, phone, account: _ }) => {
  test.skip(!phone, "phone layout");
  const backups = page.getByRole("link", { name: "Backups", exact: true, disabled: false });
  await expect(backups).toBeHidden();
  await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await backups.click();
  await expect(page.getByRole("heading", { name: "Backups", level: 1 })).toBeVisible();
  await expect(backups).toBeHidden();

  for (const path of ["/projects", "/backups", "/account", "/tokens", "/users"]) {
    await page.goto(path);
    await expect(page.getByRole("main").getByRole("heading", { level: 1 })).toBeVisible();
    const width = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(width, `${path} scrolls sideways`).toBeLessThanOrEqual(0);
  }
});
