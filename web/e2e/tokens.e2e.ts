import { expect, test } from "./fixtures";
import { newProject, sidebar } from "./steps";

test("create a restricted token, see it once, use it on the API, revoke it", async ({ page, account, request }) => {
  const other = account.project("other");
  await newProject(page, other);
  const otherId = page.url().split("/").pop();
  const shop = account.project("shop");
  await newProject(page, shop);
  const shopId = page.url().split("/").pop();

  const name = `ci-${account.username}`;
  await sidebar(page, "API tokens & MCP");
  await page.getByRole("main").getByRole("button", { name: "Create token" }).first().click();
  const form = page.getByRole("dialog", { name: "New API token" });
  await form.getByLabel("Name").fill(name);
  await form.getByRole("radio", { name: /^Restricted/ }).click();
  await form.getByRole("radiogroup", { name: `Access to ${shop}` }).getByRole("radio", { name: "View" }).click();
  await form.getByRole("button", { name: "Create token" }).click();
  const created = page.getByRole("dialog", { name: `Token ${name} created` });
  const token = (await created.getByText(/^kpt_/).textContent())!;
  await created.getByRole("button", { name: "Done" }).click();
  await expect(created).toBeHidden();

  // Shown once: neither the list nor a reload shows it again.
  const card = page.getByRole("listitem").filter({ hasText: name });
  await expect(card).toBeVisible();
  await expect(page.getByText(token)).toHaveCount(0);
  await page.reload();
  await expect(card).toBeVisible();
  await expect(page.getByText(token)).toHaveCount(0);

  // On the API it sees its one project and may view it, nothing more.
  const auth = { headers: { Authorization: `Bearer ${token}` } };
  const projects: { name: string }[] = await (await request.get("/api/projects", auth)).json();
  expect(projects.map((p) => p.name)).toEqual([shop]);
  expect((await request.get(`/api/projects/${shopId}`, auth)).status()).toBe(200);
  expect((await request.put(`/api/projects/${shopId}/name`, { ...auth, data: { name: `${shop}-renamed` } })).status()).toBe(403);
  expect((await request.get(`/api/projects/${otherId}`, auth)).status()).toBe(404); // not even shown to exist

  await card.getByRole("button", { name: "Revoke" }).click();
  const revoke = page.getByRole("alertdialog", { name: `Revoke token ${name}?` });
  await revoke.getByRole("button", { name: "Revoke" }).click();
  // The dialog hides the page from the accessibility tree while it's open: the card is gone once the list refreshed.
  await expect(revoke).toBeHidden();
  await expect(card).toBeHidden();
  expect((await request.get(`/api/projects/${shopId}`, auth)).status()).toBe(401);
});
