import { PASSWORD, adminAPI, expect, otherPage, test } from "./fixtures";
import { deployImage, newProject, sidebar } from "./steps";

test("invite a member to view one project: they see only it, read-only", async ({ page, account, browser, baseURL, errors }) => {
  const other = account.project("other");
  await newProject(page, other);
  const shop = account.project("shop");
  await newProject(page, shop);
  await deployImage(page, "web", "traefik/whoami:v1.10");

  await sidebar(page, "Users");
  await page.getByRole("main").getByRole("button", { name: "Invite user" }).click();
  const form = page.getByRole("dialog", { name: "Invite a user" });
  await expect(form.getByRole("radio", { name: /^Member/ })).toBeChecked();
  await form.getByRole("radiogroup", { name: `Access to ${shop}` }).getByRole("radio", { name: "View" }).click();
  await form.getByRole("button", { name: "Create link" }).click();
  const link = (await page.getByRole("dialog", { name: "Invite link created" }).getByText(/\/invite#/).textContent())!;

  const username = `${account.username}-viewer`;
  const member = await otherPage(browser, errors);
  try {
    await member.goto(link);
    await expect(member.getByText(`You're invited to ${shop}.`)).toBeVisible();
    await member.getByLabel("Username").fill(username);
    await member.getByLabel("Password", { exact: true }).fill(PASSWORD);
    await member.getByLabel("Confirm password").fill(PASSWORD);
    await member.getByRole("button", { name: "Create account" }).click();

    await expect(member.getByRole("heading", { name: "Projects", level: 1 })).toBeVisible();
    const main = member.getByRole("main");
    await expect(main.getByRole("link", { name: shop, exact: true })).toBeVisible();
    await expect(main.getByRole("link", { name: other, exact: true })).toHaveCount(0);
    await expect(main.getByRole("button", { name: "New project" })).toHaveCount(0);

    await main.getByRole("link", { name: shop, exact: true }).click();
    await expect(member.getByRole("heading", { name: shop, level: 1 })).toBeVisible();
    await main.getByRole("link", { name: "web" }).first().click();
    await expect(member.getByRole("heading", { name: "web", level: 1 })).toBeVisible();
    await expect(member.getByRole("tab", { name: "Logs" })).toBeVisible();
    for (const action of ["Deploy", "Restart", "Stop"]) await expect(member.getByRole("button", { name: action, exact: true })).toHaveCount(0);
    for (const tab of ["Terminal", "Files", "Settings"]) await expect(member.getByRole("tab", { name: tab, exact: true })).toHaveCount(0);
    await member.getByRole("tab", { name: "Jobs" }).click();
    await expect(member.getByRole("tabpanel", { name: "Jobs" })).toBeVisible();
    await expect(member.getByRole("button", { name: "New job" })).toHaveCount(0);
  } finally {
    await member.context().close();
    const api = await adminAPI(baseURL);
    const users: { id: string; username: string }[] = await (await api.get("/api/users")).json();
    for (const u of users.filter((u) => u.username === username)) expect((await api.delete(`/api/users/${u.id}`)).ok()).toBeTruthy();
    await api.dispose();
  }
});
