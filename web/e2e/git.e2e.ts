import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";

import type { Page } from "@playwright/test";

import { expect, test as base } from "./fixtures";
import { newProject } from "./steps";

type Repo = {
  /** Where the manager clones it from. */
  url: string;
  /** Commits compose.yaml with this content on main, and returns the commit's hash. */
  commit: (compose: string) => string;
};

// A repository served the way a forge serves a public one, over git's smart HTTP protocol (git http-backend, a CGI),
// from the test's own process: the manager, next to it, clones it without any outside network.
const test = base.extend<{ repo: Repo }>({
  repo: async ({}, use) => {
    const root = mkdtempSync(join(tmpdir(), "kipitiny-git-"));
    const work = join(root, "infra");
    execFileSync("git", ["init", "-q", "-b", "main", work]);
    const git = (...args: string[]) =>
      execFileSync("git", ["-C", work, "-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid", ...args]).toString().trim();
    const server = createServer((req, res) => {
      const url = new URL(req.url ?? "/", "http://localhost");
      const cgi = spawn("git", ["http-backend"], {
        env: {
          ...process.env,
          GIT_PROJECT_ROOT: root,
          GIT_HTTP_EXPORT_ALL: "1",
          PATH_INFO: url.pathname,
          QUERY_STRING: url.search.slice(1),
          REQUEST_METHOD: req.method ?? "GET",
          CONTENT_TYPE: req.headers["content-type"] ?? "",
          HTTP_CONTENT_ENCODING: req.headers["content-encoding"] ?? "",
          GIT_PROTOCOL: String(req.headers["git-protocol"] ?? ""),
        },
      });
      req.pipe(cgi.stdin);
      // The CGI writes its headers, a blank line, then the body.
      let head = Buffer.alloc(0);
      cgi.stdout.on("data", (chunk: Buffer) => {
        if (res.headersSent) return res.write(chunk);
        head = Buffer.concat([head, chunk]);
        const end = head.indexOf("\r\n\r\n");
        if (end < 0) return;
        const headers = Object.fromEntries(
          head.subarray(0, end).toString().split("\r\n").map((l) => [l.slice(0, l.indexOf(":")), l.slice(l.indexOf(":") + 1).trim()]),
        );
        const { Status: status = "200", ...rest } = headers;
        res.writeHead(parseInt(status), rest);
        res.write(head.subarray(end + 4));
      });
      cgi.stdout.on("end", () => res.end());
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const { port } = server.address() as AddressInfo;
    await use({
      url: `http://127.0.0.1:${port}/infra`,
      commit: (compose) => {
        writeFileSync(join(work, "compose.yaml"), compose);
        git("add", "compose.yaml");
        git("commit", "-q", "-m", "compose");
        return git("rev-parse", "HEAD");
      },
    });
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    rmSync(root, { recursive: true, force: true });
  },
});

/** Opens a tab of the open project's page. */
async function tab(page: Page, name: string) {
  await page.getByRole("main").getByRole("tablist").first().getByRole("tab", { name, exact: true }).click();
  return page.getByRole("tabpanel", { name, exact: true });
}

test("link a project to a git repository, deploy its compose file, then sync a new commit", async ({ page, account, repo }) => {
  const project = account.project("infra");
  const first = repo.commit("services:\n  web:\n    image: traefik/whoami:v1.10\n");
  await newProject(page, project);

  const git = await tab(page, "Git");
  await git.getByRole("textbox", { name: "Repository (https)" }).fill(repo.url);
  // By hand only: the poll loop syncing the second commit on its own would make "Sync now" prove nothing.
  await git.getByRole("checkbox", { name: "Sync automatically" }).uncheck();
  await git.getByRole("button", { name: "Preview changes" }).click();
  await git.getByRole("button", { name: "Link and sync" }).click();
  await expect(git.getByText(first.slice(0, 12))).toBeVisible({ timeout: 60_000 });
  await expect((await tab(page, "Services")).getByText("1/1 replica", { exact: true })).toBeVisible({ timeout: 90_000 });

  const second = repo.commit("services:\n  web:\n    image: traefik/whoami:v1.10\n    deploy:\n      replicas: 2\n");
  await tab(page, "Git");
  await git.getByRole("button", { name: "Sync now" }).click();
  await expect(git.getByText(second.slice(0, 12))).toBeVisible({ timeout: 60_000 });
  await expect((await tab(page, "Services")).getByText("2/2 replicas", { exact: true })).toBeVisible({ timeout: 90_000 });
});
