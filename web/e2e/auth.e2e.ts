import { ADMIN, PASSWORD, expect, test, totp } from "./fixtures";
import { sidebar } from "./steps";

test("turn on two-factor authentication, then sign in with a code", async ({ page, account }) => {
  await page.goto("/account");
  await page.getByRole("button", { name: "Turn on", exact: true }).click();
  const setup = page.getByRole("dialog");
  await setup.getByLabel("Password").fill(PASSWORD);
  await setup.getByRole("button", { name: "Continue" }).click();
  await expect(setup.getByRole("heading", { name: "Scan with your authenticator app" })).toBeVisible();
  const secret = (await setup.getByText(/^[A-Z2-7]{4}( [A-Z2-7]{1,4})+$/).textContent())!.replace(/ /g, "");
  const turnedOnWith = totp(secret);
  await setup.getByRole("textbox", { name: "Code" }).fill(turnedOnWith);
  await setup.getByRole("button", { name: "Turn on", exact: true }).click();
  await expect(setup.getByRole("heading", { name: "Two-factor authentication is on" })).toBeVisible();
  await setup.getByRole("button", { name: "I saved them" }).click();
  await expect(setup).toBeHidden();

  await sidebar(page, new RegExp(account.username));
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await page.getByLabel("Username").fill(account.username);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();

  // A wrong code is refused, so is the one that turned it on (each code works once); the next one signs in, the clock
  // drift allowed taking it before its time.
  for (const [code, why] of [["000000", "Wrong code."], [turnedOnWith, /already used/]] as const) {
    await page.getByRole("textbox", { name: "Code" }).fill(code);
    await page.getByRole("button", { name: "Verify" }).click();
    await expect(page.getByText(why)).toBeVisible();
  }
  await page.getByRole("textbox", { name: "Code" }).fill(totp(secret, Date.now() + 30_000));
  await page.getByRole("button", { name: "Verify" }).click();
  await expect(page.getByRole("heading", { name: "Account", level: 1 })).toBeVisible(); // back where they signed out
  await expect(page.getByText(/^Two-factor authentication\s*On$/)).toBeVisible();
});

test("a wrong password doesn't sign in", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Username").fill(ADMIN);
  await page.getByLabel("Password").fill("not the password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByText("Couldn't sign in")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Projects", level: 1 })).toBeHidden();
});
