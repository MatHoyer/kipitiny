import { createHmac } from "node:crypto";

import { test as base, expect, request, type APIRequestContext } from "@playwright/test";

export { expect };

export const PASSWORD = "correct horse battery";
export const ADMIN = "admin";

// Every uncaught error in a page fails the test that caused it. Except one: WebKit reports each request still in
// flight when a test navigates away (page.goto) as "… due to access control checks", same-origin ones included.
// shortcut: matched by message; revisit if a Playwright or WebKit update changes the wording.
const cancelledByNavigation = /due to access control checks\.$/;

/** A signed-in API session of the first admin. */
export async function adminAPI(baseURL: string | undefined): Promise<APIRequestContext> {
  const api = await request.newContext({ baseURL });
  const res = await api.post("/api/auth/login", { data: { username: ADMIN, password: PASSWORD } });
  expect(res.ok(), "admin sign-in").toBeTruthy();
  return api;
}

/** An invite token the first admin made, for an admin account. */
export async function inviteToken(baseURL: string | undefined): Promise<string> {
  const api = await adminAPI(baseURL);
  const res = await api.post("/api/invites", { data: { role: "admin" } });
  expect(res.ok(), `invite: ${await res.text()}`).toBeTruthy();
  const { token } = await res.json();
  await api.dispose();
  return token;
}

/** The TOTP code of a base32 secret at a time (RFC 6238: SHA-1, 6 digits, 30 s). */
export function totp(secret: string, at = Date.now()): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const ch of secret.replace(/=+$/, "").toUpperCase()) bits += alphabet.indexOf(ch).toString(2).padStart(5, "0");
  const key = Buffer.from(bits.match(/.{8}/g)!.map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 30_000)));
  const h = createHmac("sha1", key).update(counter).digest();
  const o = h[h.length - 1] & 0xf;
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}

type Account = {
  username: string;
  /** A project name of this test's own: the account deletes its projects, and their containers, when the test ends. */
  project: (name: string) => string;
};

type Fixtures = {
  /** A fresh admin account of its own, joined through an invite and signed in on page. */
  account: Account;
  /** Whether the project runs at phone size. */
  phone: boolean;
};

export const test = base.extend<Fixtures & { errors: string[] }>({
  errors: async ({}, use) => {
    const errors: string[] = [];
    await use(errors);
    expect(errors, "uncaught errors in the page").toEqual([]);
  },
  context: async ({ context }, use) => {
    // Tests stay offline: whatever the manager's UI would fetch from elsewhere gets a local 404.
    await context.route(
      (url) => url.hostname !== "localhost",
      (route) => route.fulfill({ status: 404, body: "" }),
    );
    await use(context);
  },
  page: async ({ page, errors }, use) => {
    page.on("pageerror", (e) => {
      if (!cancelledByNavigation.test(e.message)) errors.push(e.message);
    });
    await use(page);
  },
  phone: async ({ isMobile }, use) => use(isMobile),
  account: async ({ page, baseURL }, use) => {
    const username = `e2e-${crypto.randomUUID().slice(0, 8)}`;
    const res = await page.request.post("/api/auth/invite/accept", {
      data: { token: await inviteToken(baseURL), username, password: PASSWORD },
    });
    expect(res.ok(), `accept invite: ${await res.text()}`).toBeTruthy();
    await page.goto("/");
    await use({ username, project: (name) => `${username}-${name}` });

    const api = await adminAPI(baseURL);
    const projects: { id: string; name: string }[] = await (await api.get("/api/projects")).json();
    for (const p of projects.filter((p) => p.name.startsWith(`${username}-`))) {
      const res = await api.delete(`/api/projects/${p.id}?confirm=${encodeURIComponent(p.name)}`);
      expect(res.ok(), `delete ${p.name}: ${await res.text()}`).toBeTruthy();
    }
    await api.dispose();
  },
});
