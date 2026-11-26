import type { DeployErrors, Deployment, ErrorEvent, Issue, IssueEvent, IssueStatus } from "@/api";

const now = () => Math.floor(Date.now() / 1000);
const H = 3600;

/** The demo web app's issues, made at load time so their ages read right. */
const issues: Issue[] = [
  {
    id: 14, title: "TypeError: Cannot read properties of undefined (reading 'id')", culprit: "getUser · src/api/users.ts:42", level: "error",
    platform: "node", status: "open", firstSeen: now() - 2 * H, lastSeen: now() - 12, events: 1031, users: 174,
    firstRelease: "sha-a1b2c3", lastRelease: "sha-a1b2c3", spark: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 412, 619],
  },
  {
    id: 9, title: "PrismaClientKnownRequestError: Unique constraint failed on the fields: (`email`)", culprit: "createAccount · src/api/signup.ts:88",
    level: "error", platform: "node", status: "open", firstSeen: now() - 5 * 24 * H, lastSeen: now() - 40 * 60, events: 201, users: 31,
    firstRelease: "sha-0c9d42", lastRelease: "sha-a1b2c3", spark: [1, 0, 2, 1, 0, 0, 3, 1, 0, 2, 1, 1, 0, 0, 2, 1, 0, 1, 2, 0, 1, 1, 0, 1],
  },
  {
    id: 6, title: "Error: Stripe webhook signature mismatch", culprit: "verify · src/billing/webhook.ts:19", level: "error", platform: "node",
    status: "open", firstSeen: now() - 9 * 24 * H, lastSeen: now() - 25 * 60, events: 52, users: 7, firstRelease: "sha-5be210",
    lastRelease: "sha-a1b2c3", resolvedAt: now() - 3 * 24 * H, regressedAt: now() - 2 * H,
    spark: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 2],
  },
  {
    id: 3, title: "AbortError: The operation was aborted", culprit: "fetchRates · src/lib/currency.ts:12", level: "warning", platform: "node",
    status: "resolved", firstSeen: now() - 20 * 24 * H, lastSeen: now() - 6 * 24 * H, events: 88, users: 40, firstRelease: "sha-19aa03",
    lastRelease: "sha-77e01f", resolvedAt: now() - 5 * 24 * H, spark: Array(24).fill(0),
  },
];

const event = (is: Issue): ErrorEvent => ({
  id: `${is.id}e1`, at: is.lastSeen, level: is.level, platform: "node", release: is.lastRelease, environment: "production",
  serverName: "web-2", transaction: "GET /api/profile", user: "u_8f21", url: "https://acme.example/api/profile", method: "GET", country: "FR",
  sdk: "sentry.javascript.node 11.19.0", tags: { route: "/api/profile", plan: "pro" },
  contexts: { runtime: "node v22.12.0", os: "Linux 6.8.0" },
  exceptions: [{
    type: is.title.split(":")[0], value: is.title.split(": ").slice(1).join(": "), mechanism: "onunhandledrejection", handled: false,
    frames: [
      { function: "Layer.handle", file: "node_modules/express/lib/router/layer.js", line: 95 },
      { function: "next", file: "node_modules/express/lib/router/route.js", line: 149 },
      { function: "handler", file: "src/routes/profile.ts", line: 18, inApp: true },
      {
        function: is.culprit.split(" · ")[0], file: is.culprit.split(" · ")[1]?.split(":")[0], line: Number(is.culprit.split(":").pop()), inApp: true,
        context: ["  const session = await getSession(req);", "  const user = await db.user.findUnique({ where: { id: session.userId } });",
          "  return { id: user.profile.id, name: user.name };", "}"], contextLine: 2,
      },
    ],
  }],
  breadcrumbs: [
    { at: is.lastSeen - 2, category: "http", data: { method: "GET", url: "https://acme.example/api/session", status_code: "200" } },
    { at: is.lastSeen - 1, category: "query", message: 'SELECT … FROM "User" WHERE id = $1' },
    { at: is.lastSeen, category: "http", data: { method: "GET", url: "https://acme.example/api/profile" } },
  ],
});

export function demoIssues(q: URLSearchParams) {
  const status = q.get("status") || "";
  const search = (q.get("q") || "").toLowerCase();
  const sort = (q.get("sort") || "lastSeen") as "lastSeen" | "firstSeen" | "events" | "users";
  const matching = issues.filter((is) => !search || (is.title + is.culprit).toLowerCase().includes(search));
  const counts = { open: 0, resolved: 0, ignored: 0 } as Record<IssueStatus, number>;
  for (const is of matching) counts[is.status]++;
  return {
    issues: matching.filter((is) => !status || is.status === status).sort((a, b) => b[sort] - a[sort]),
    counts,
    dsn: "https://3f9a2c71e0b84d55a1c2d3e4f5a6b7c8@stats.acme.example/14",
  };
}

export function demoErrorSeries(q: URLSearchParams) {
  const from = Number(q.get("from")), to = Number(q.get("to")), iv = Number(q.get("interval"));
  const out = [];
  for (let t = from; t < to; t += iv) {
    // A few a bucket, then the bad deploy of 2 hours ago.
    out.push({ t, n: [1, 0, 2, 1, 0, 3][Math.floor(t / iv) % 6] + (to - t <= 2 * H ? Math.round(500 * Math.min(1, iv / H)) : 0) });
  }
  return out;
}

export const demoIssue = (id: string) => issues.find((is) => is.id === Number(id));

export function demoIssueEvent(id: string): IssueEvent | undefined {
  const is = demoIssue(id);
  return is && { event: event(is), index: 1, kept: Math.min(100, is.events), older: undefined, newer: undefined };
}

export function setDemoIssueStatus(id: string, status: IssueStatus) {
  const is = demoIssue(id);
  if (is) Object.assign(is, { status, resolvedAt: status === "resolved" ? now() : undefined, regressedAt: undefined });
  return is;
}

/** The web app's last deploy raised errors: the banner and its rollback. */
export function demoDeployErrors(deps: Deployment[]): DeployErrors {
  const ok = deps.filter((d) => d.status === "succeeded");
  const [cur, prev] = ok;
  const tag = (d?: Deployment) => d?.image.split(":").pop() ?? "";
  if (!cur || !prev) return { deploymentId: "", release: "", minutes: 0, events: 0, newIssues: 0, rate: 0, previousRate: 0, spike: false };
  return {
    deploymentId: cur.id, release: tag(cur), minutes: 60, events: 1031, newIssues: 1, previous: prev.id, previousRelease: tag(prev),
    rate: 17.2, previousRate: 1.2, spike: true,
  };
}
