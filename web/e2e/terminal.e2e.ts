import { expect, test } from "./fixtures";
import { deployImage, newProject } from "./steps";

test("open a shell in an app and run a command", async ({ page, account }) => {
  await newProject(page, account.project("shop"));
  await deployImage(page, "web", "nginx:1.27-alpine");

  await page.getByRole("tab", { name: "Terminal" }).click();
  const panel = page.getByRole("tabpanel", { name: "Terminal" });
  await panel.getByRole("button", { name: "Connect" }).click();
  const terminal = panel.getByRole("region", { name: "Terminal" });
  const output = terminal.getByRole("list");
  await expect(output).toContainText("#"); // the shell's prompt: it reads input from now on

  // The shell computes the output, so it can't be mistaken for the command echoed back.
  const n = Math.floor(Math.random() * 1e6);
  const input = terminal.getByRole("textbox", { name: "Terminal input" });
  await input.pressSequentially(`echo hello-$((${n} + 1))`);
  await input.press("Enter");
  await expect(output).toContainText(`hello-${n + 1}`);
});
