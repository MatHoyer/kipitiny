import type { Page } from "@playwright/test";

import { ANALYTICS_DOMAIN, expect, test } from "./fixtures";
import { newApp, newProject } from "./steps";

// The analytics server global-setup turned on, reached through Traefik's HTTPS port: on this machine with a dind,
// on the runner's host in CI (make e2e). Its certificate is Traefik's own.
const traefik = `https://${process.env.TRAEFIK_HOST ?? "127.0.0.1"}`;
test.use({ ignoreHTTPSErrors: true });

/** Opens the app's networking settings. */
async function networking(page: Page) {
  await page.getByRole("tab", { name: "Settings" }).click();
  await page.getByRole("tab", { name: "Networking" }).click();
  return page.getByRole("tabpanel", { name: "Networking" });
}

test("count an app's visits and see them in its Analytics tab", async ({ page, account }) => {
  const domain = `${account.username}-shop.localhost`;
  await newProject(page, account.project("shop"));
  await newApp(page, "web", "traefik/whoami:v1.10");
  const id = new URL(page.url()).pathname.split("/").pop();

  const settings = await networking(page);
  await settings.getByRole("textbox", { name: "Domain" }).fill(domain);
  await settings.getByRole("spinbutton", { name: "Port" }).first().fill("80");
  await page.getByRole("button", { name: "Save domain" }).click();
  await expect(page.getByText("Domain saved")).toBeVisible();
  await settings.getByRole("switch", { name: "Count visits" }).click();
  await expect(settings.getByRole("switch", { name: "Count visits" })).toBeChecked();

  // A page of the app with the tag the Analytics tab gives, in this browser: its tracker sends the pageview.
  const tag = `<script defer src="https://${ANALYTICS_DOMAIN}/a.js"></script>`;
  await page.route(`https://${domain}/**`, (route) =>
    route.fulfill({ contentType: "text/html", body: `<!doctype html><title>Pricing</title>${tag}<h1>Pricing</h1>` }),
  );
  await page.route(`https://${ANALYTICS_DOMAIN}/**`, async (route) => {
    const url = new URL(route.request().url());
    const response = await route.fetch({ url: traefik + url.pathname, headers: { ...route.request().headers(), host: ANALYTICS_DOMAIN } });
    await route.fulfill({ response });
  });
  // The server learns the app's domain from the reconciler, soon after the switch; until then it drops the visit.
  await expect(async () => {
    const hit = page.waitForResponse(`https://${ANALYTICS_DOMAIN}/e`);
    await page.goto(`https://${domain}/pricing?utm_source=e2e`);
    expect((await hit).status()).toBe(204);
    const stats = await (await page.request.get(`/api/services/${id}/analytics`)).json();
    expect(stats.pageviews).toBeGreaterThan(0);
  }).toPass({ timeout: 30_000 });

  await page.goto(`/services/${id}?tab=analytics`);
  const tab = page.getByRole("tabpanel", { name: "Analytics" });
  // One page per visit: the top page, the entry page and the exit page.
  await expect(tab.getByRole("button", { name: "/pricing", exact: true })).toHaveCount(3);
  await expect(tab.getByRole("button", { name: "e2e", exact: true })).toBeVisible();
});

test("see an error an app reported in its Errors tab, then resolve it", async ({ page, account }) => {
  await newProject(page, account.project("shop"));
  await newApp(page, "web", "traefik/whoami:v1.10");

  const settings = await networking(page);
  await settings.getByRole("switch", { name: "Track errors" }).click();
  await page.getByRole("tab", { name: "Errors" }).click();
  const tab = page.getByRole("tabpanel", { name: "Errors" });
  await expect(tab.getByText("No errors yet")).toBeVisible();
  const dsn = new URL((await tab.getByText(new RegExp(`^https://\\w+@${ANALYTICS_DOMAIN}/\\d+$`)).textContent())!);

  // What a Sentry SDK sends: an envelope with the event, to the DSN's project, authenticated by its key.
  const event = {
    event_id: crypto.randomUUID().replaceAll("-", ""),
    timestamp: Date.now() / 1000,
    platform: "javascript",
    level: "error",
    release: "web@1.0.0",
    environment: "production",
    exception: {
      values: [
        {
          type: "TypeError",
          value: "Cannot read properties of undefined (reading 'price')",
          stacktrace: { frames: [{ filename: "/app/cart.js", function: "total", lineno: 12, in_app: true }] },
        },
      ],
    },
  };
  const envelope = [JSON.stringify({ event_id: event.event_id, dsn: dsn.href }), JSON.stringify({ type: "event" }), JSON.stringify(event)].join("\n");
  // The server learns the app's key from the reconciler, soon after the switch; until then it refuses the event.
  await expect
    .poll(async () => {
      const res = await page.request.post(`${traefik}/api${dsn.pathname}/envelope/`, {
        headers: { host: ANALYTICS_DOMAIN, "content-type": "application/x-sentry-envelope", "x-sentry-auth": `Sentry sentry_version=7, sentry_key=${dsn.username}` },
        data: envelope,
      });
      return res.status();
    }, { timeout: 30_000 })
    .toBe(200);

  await page.reload();
  const issue = tab.getByRole("button", { name: /^TypeError/ });
  await expect(issue).toBeVisible();
  await issue.click();
  const sheet = page.getByRole("dialog", { name: /^TypeError/ });
  // The event, with its stack.
  await expect(sheet.getByRole("heading", { level: 3, name: "TypeError: Cannot read properties of undefined (reading 'price')" })).toBeVisible();
  await expect(sheet.getByText("/app/cart.js:12")).toBeVisible();
  await sheet.getByRole("button", { name: "Resolve" }).click();
  await sheet.getByRole("button", { name: "Close" }).click();
  await expect(tab.getByText("No open issues.")).toBeVisible();
  await tab.getByRole("radio", { name: /^Resolved/ }).click();
  await expect(issue).toBeVisible();
});
