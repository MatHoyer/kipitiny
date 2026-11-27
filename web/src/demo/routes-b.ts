import { usage } from "./stats";
import type { Restore, EmailSettings, Deployment, ServiceUsage, UptimeCheck, UptimeInput, JobRun } from "@/api";
import { files } from "./files";
import { backingUp, backupKindOf, progressed, restoring, runs, startBackup, tickDeploys, uptime, verifyStarts, verifying, type Route } from "./ops";
import { HttpError, MASK, db, demoTags, emailView, invalid, isDb, jobFrom, jobOf, jobState, jobView, newId, notFound, now, projectOf, save, serviceOf, took, view } from "./state";
import { serviceStats } from "./stats";
import { newBackup, scheduleFrom, viaFiles } from "./views";

export const routesB: Route[] = [
  [
    "PUT",
    /^\/services\/([^/]+)\/tags$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      s.tags = demoTags(body.tags);
      save();
      return view(s);
    },
  ],
  [
    "PUT",
    /^\/services\/([^/]+)\/networks$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      s.networks = (body.networks as string[]).filter((id) => db.networks.some((n) => n.id === id));
      save();
      return view(s);
    },
  ],
  [
    "DELETE",
    /^\/services\/([^/]+)$/,
    ({ m }) => {
      const s = serviceOf(m[1]);
      db.services = db.services.filter((x) => x.id !== s.id);
      save();
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/deploy$/,
    ({ m }): Deployment => {
      const s = serviceOf(m[1]);
      const d: Deployment = { id: newId("dep"), serviceId: s.id, status: "succeeded", image: s.image, triggeredBy: "user:demo", createdAt: now(), finishedAt: now() };
      db.deployments.unshift(d);
      s.currentDeploymentId = d.id;
      s.stopped = false;
      s.routePending = false;
      save();
      return d;
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/(start|stop|restart)$/,
    ({ m }) => {
      const s = serviceOf(m[1]);
      s.stopped = m[2] === "stop";
      save();
      return view(s);
    },
  ],
  ["GET", /^\/services\/([^/]+)\/files$/, ({ m, q }) => viaFiles(() => files.list(serviceOf(m[1]), q.get("path") ?? ""))],
  ["GET", /^\/services\/([^/]+)\/files\/content$/, ({ m, q }) => viaFiles(() => files.read(serviceOf(m[1]), q.get("path") ?? ""))],
  ["PUT", /^\/services\/([^/]+)\/files\/content$/, ({ m, q, body }) => viaFiles(() => files.write(serviceOf(m[1]), q.get("path") ?? "", body, q))],
  ["POST", /^\/services\/([^/]+)\/files\/mkdir$/, ({ m, body }) => viaFiles(() => files.mkdir(serviceOf(m[1]), body.path))],
  ["POST", /^\/services\/([^/]+)\/files\/move$/, ({ m, body }) => viaFiles(() => files.transfer(serviceOf(m[1]), body.from, body.to, false))],
  ["POST", /^\/services\/([^/]+)\/files\/copy$/, ({ m, body }) => viaFiles(() => files.transfer(serviceOf(m[1]), body.from, body.to, true))],
  ["POST", /^\/services\/([^/]+)\/files\/delete$/, ({ m, body }) => viaFiles(() => files.remove(serviceOf(m[1]), body.paths ?? []))],
  ["GET", /^\/services\/([^/]+)\/deployments$/, ({ m }) => (tickDeploys(), db.deployments.filter((d) => d.serviceId === m[1]))],
  ["GET", /^\/deployments\/([^/]+)\/log$/, () => "Pulling image…\nStarting replicas…\nHealthy.\nDeployment succeeded (this is a demo: nothing really ran)."],
  ["GET", /^\/services\/([^/]+)\/stats$/, ({ m }) => serviceStats(serviceOf(m[1]))],
  [
    "GET",
    /^\/stats$/,
    () =>
      Object.fromEntries(
        db.services.filter((s) => !s.stopped).map((s): [string, ServiceUsage] => [s.id, { ...usage(s, Date.now()), projectId: s.projectId, serverId: "local" }]),
      ),
  ],
  ["GET", /^\/services\/([^/]+)\/uptime$/, ({ m }) => uptime(serviceOf(m[1]))],
  [
    "PUT",
    /^\/services\/([^/]+)\/uptime$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      const input = body as UptimeInput;
      const prev = db.uptime.find((c) => c.serviceId === s.id);
      const now = new Date().toISOString();
      const chk: UptimeCheck = { ...input, serviceId: s.id, down: false, changedAt: prev?.changedAt ?? now, createdAt: prev?.createdAt ?? now };
      db.uptime = [...db.uptime.filter((c) => c.serviceId !== s.id), chk];
      save();
      return uptime(s);
    },
  ],
  [
    "DELETE",
    /^\/services\/([^/]+)\/uptime$/,
    ({ m }) => {
      db.uptime = db.uptime.filter((c) => c.serviceId !== m[1]);
      save();
    },
  ],
  ["GET", /^\/services\/([^/]+)\/backups$/, ({ m }) => db.backups.filter((b) => b.serviceId === m[1]).map(backingUp)],
  [
    "POST",
    /^\/services\/([^/]+)\/backups$/,
    ({ m, body }) => {
      const b = startBackup(newBackup(serviceOf(m[1]), body.targetId));
      db.backups.unshift(b);
      save();
      return backingUp(b);
    },
  ],
  [
    "POST",
    /^\/projects\/([^/]+)\/backups$/,
    ({ m, body }) => {
      const made = db.services.filter((s) => s.projectId === projectOf(m[1]).id && (isDb(s) || s.volumes.length > 0)).map((s) => newBackup(s, body.targetId));
      db.backups.unshift(...made);
      save();
      return { backups: made };
    },
  ],
  [
    "POST",
    /^\/manager\/backups$/,
    ({ body }) => {
      const b = startBackup(newBackup(null, body.targetId));
      db.backups.unshift(b);
      save();
      return backingUp(b);
    },
  ],
  ["GET", /^\/backups$/, ({ q }) => (q.get("projectId") ? db.backups.filter((b) => b.projectId === q.get("projectId")) : db.backups).map(backingUp)],
  ["GET", /^\/backups\/([^/]+)$/, ({ m }) => verifying(backingUp(db.backups.find((b) => b.id === m[1]) ?? (() => { throw notFound(); })()))],
  [
    "DELETE",
    /^\/backups\/([^/]+)$/,
    ({ m }) => {
      db.backups = db.backups.filter((b) => b.id !== m[1]);
      save();
    },
  ],
  [
    "POST",
    /^\/backups\/([^/]+)\/verify$/,
    ({ m }) => {
      const b = db.backups.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      verifyStarts.set(b.id, Date.now());
      Object.assign(b, { verifyStatus: "running", verifyError: undefined });
      save();
      return verifying(b);
    },
  ],
  [
    "POST",
    /^\/backups\/([^/]+)\/restore$/,
    ({ m, body }) => {
      const b = db.backups.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      const svc = serviceOf(body.serviceId || b.serviceId);
      if (body.confirm !== svc.name) throw invalid(`type ${svc.name} to confirm`);
      const r: Restore = { id: newId("rs"), backupId: b.id, serviceId: svc.id, status: "running", createdAt: now() };
      const steps: [string, number][] =
        b.kind === "volume"
          ? [["extract", 1500], ["stopservice", 700], ["replace", 1200], ["start", 800]]
          : b.kind === "dump"
            ? [["stop", 700], ["check", 1000], ["scratch", 1500], ["load", 1500], ["start", 700]]
            : [["stop", 700], ["load", 2200], ["swap", 900], ["start", 700]];
      runs.set(r.id, { start: Date.now(), steps });
      db.restores.unshift(r);
      save();
      return restoring(r);
    },
  ],
  ["GET", /^\/services\/([^/]+)\/restores$/, ({ m }) => db.restores.filter((r) => r.serviceId === m[1]).map(restoring)],
  ["GET", /^\/services\/([^/]+)\/jobs$/, ({ m }) => jobState().jobs.filter((j) => j.serviceId === serviceOf(m[1]).id).map(jobView)],
  [
    "POST",
    /^\/services\/([^/]+)\/jobs$/,
    ({ m, body }) => {
      const svc = serviceOf(m[1]);
      if (svc.kind !== "app") throw invalid("only apps run jobs");
      const j = jobFrom(body, { serviceId: svc.id });
      jobState().jobs.push(j);
      save();
      return jobView(j);
    },
  ],
  [
    "PUT",
    /^\/jobs\/([^/]+)$/,
    ({ m, body }) => {
      const { jobs } = jobState();
      const i = jobs.indexOf(jobOf(m[1]));
      jobs[i] = jobFrom(body, jobs[i]);
      save();
      return jobView(jobs[i]);
    },
  ],
  [
    "DELETE",
    /^\/jobs\/([^/]+)$/,
    ({ m }) => {
      jobOf(m[1]);
      db.jobs = db.jobs!.filter((j) => j.id !== m[1]);
      db.jobRuns = db.jobRuns!.filter((r) => r.jobId !== m[1]);
      save();
    },
  ],
  [
    "POST",
    /^\/jobs\/([^/]+)\/run$/,
    ({ m }) => {
      const j = jobOf(m[1]);
      const { runs } = jobState();
      if (runs.some((r) => r.jobId === j.id && r.status === "running")) throw new HttpError(409, `operation in progress: job ${j.name} is running`);
      const r: JobRun = { id: newId("run"), jobId: j.id, status: "running", triggeredBy: "user:demo", createdAt: now() };
      runs.unshift(r);
      save();
      return r;
    },
  ],
  ["GET", /^\/jobs\/([^/]+)\/runs$/, ({ m }) => jobState().runs.filter((r) => r.jobId === jobOf(m[1]).id)],
  [
    "GET",
    /^\/jobs\/([^/]+)\/runs\/([^/]+)\/log$/,
    ({ m }) => {
      const j = jobOf(m[1]);
      const r = jobState().runs.find((r) => r.id === m[2] && r.jobId === j.id) ?? (() => { throw notFound(); })();
      const t = (iso: string) => `[${iso.slice(11, 19)}]`;
      const lines = [`${t(r.createdAt)} Running ${j.command} in ghcr.io/acme/api@sha256:4f1c…`];
      if (r.status === "running") return lines.join("\n") + "\n";
      lines.push(...(r.status === "failed" ? ["Error: SMTP connection refused (smtp.acme.example:587)", "    at report.js:42:11"] : ["done in 1.8s"]));
      lines.push(r.status === "failed" ? `${t(r.finishedAt!)} Failed: ${r.error}` : `${t(r.finishedAt!)} Succeeded in ${took(r)}`);
      return lines.join("\n") + "\n";
    },
  ],
  ["GET", /^\/services\/([^/]+)\/schedules$/, ({ m }) => db.schedules.filter((s) => s.serviceId === m[1])],
  [
    "POST",
    /^\/services\/([^/]+)\/schedules$/,
    ({ m, body }) => {
      const svc = serviceOf(m[1]);
      const s = scheduleFrom(body, { serviceId: svc.id, kind: backupKindOf(svc.kind) });
      db.schedules.push(s);
      save();
      return s;
    },
  ],
  ["GET", /^\/manager\/schedules$/, () => db.schedules.filter((s) => s.kind === "manager")],
  [
    "POST",
    /^\/manager\/schedules$/,
    ({ body }) => {
      const s = scheduleFrom(body, { kind: "manager" });
      db.schedules.push(s);
      save();
      return s;
    },
  ],
  [
    "PUT",
    /^\/schedules\/([^/]+)$/,
    ({ m, body }) => {
      const i = db.schedules.findIndex((s) => s.id === m[1]);
      if (i < 0) throw notFound();
      db.schedules[i] = scheduleFrom(body, db.schedules[i]);
      save();
      return db.schedules[i];
    },
  ],
  [
    "DELETE",
    /^\/schedules\/([^/]+)$/,
    ({ m }) => {
      db.schedules = db.schedules.filter((s) => s.id !== m[1]);
      save();
    },
  ],

  ["GET", /^\/email$/, emailView],
  [
    "PUT",
    /^\/email\/invites$/,
    ({ body }) => {
      if (!db.emailProviders?.some((p) => p.id === body.provider)) throw invalid("unknown email provider");
      db.emailInvites = String(body.provider);
      save();
      return emailView();
    },
  ],
  [
    "POST",
    /^\/email\/providers$/,
    ({ body }) => {
      const s = body as unknown as EmailSettings;
      if (!s.name?.trim() || !s.host || !s.from.includes("@")) throw invalid("name, host and sender are required");
      if (db.emailProviders?.some((p) => p.name === s.name.trim())) throw invalid("an email provider with this name exists");
      const p = { ...s, name: s.name.trim(), id: newId("mail"), createdAt: now() };
      (db.emailProviders ??= []).push(p);
      save();
      return { ...p, password: p.password && MASK };
    },
  ],
  [
    "PUT",
    /^\/email\/providers\/([^/]+)$/,
    ({ m, body }) => {
      const p = db.emailProviders?.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      const s = body as unknown as EmailSettings;
      if (!s.name?.trim() || !s.host || !s.from.includes("@")) throw invalid("name, host and sender are required");
      Object.assign(p, { ...s, name: s.name.trim(), password: s.password === MASK ? p.password : s.password });
      save();
      return { ...p, password: p.password && MASK };
    },
  ],
  [
    "DELETE",
    /^\/email\/providers\/([^/]+)$/,
    ({ m }) => {
      const used = db.channels.filter((c) => c.kind === "email" && c.config.provider === m[1]).map((c) => c.name);
      if (used.length) throw invalid(`notification channels send through it (${used.join(", ")}); pick another provider for them first`);
      db.emailProviders = db.emailProviders?.filter((p) => p.id !== m[1]);
      save();
    },
  ],
  ["POST", /^\/email\/providers\/([^/]+)\/test$/, () => undefined],
  ["GET", /^\/cloudflare$/, () => ({ connected: false, zones: [], tunnels: [] })],
  ["GET", /^\/git-providers$/, () => []],
  ["GET", /^\/sso-providers$/, () => (db.ssoProviders ??= [])],
  ["GET", /^\/sso-required$/, () => ({ required: false })],
  [
    "PUT",
    /^\/sso-required$/,
    () => {
      throw new HttpError(400, "link your own account at a provider first (Account › Single sign-on), so you can still sign in");
    },
  ],
  [
    "POST",
    /^\/sso-providers$/,
    ({ body }) => {
      const p = { id: newId("sso"), name: String(body.name), issuer: String(body.issuer), clientId: String(body.clientId), clientSecret: MASK, createdAt: now() };
      (db.ssoProviders ??= []).push(p);
      save();
      return p;
    },
  ],
  [
    "PUT",
    /^\/sso-providers\/([^/]+)$/,
    ({ m, body }) => {
      const p = db.ssoProviders?.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      Object.assign(p, { name: body.name, issuer: body.issuer, clientId: body.clientId });
      save();
      return p;
    },
  ],
  [
    "DELETE",
    /^\/sso-providers\/([^/]+)$/,
    ({ m }) => {
      db.ssoProviders = db.ssoProviders?.filter((p) => p.id !== m[1]);
      save();
    },
  ],
  ["GET", /^\/registries$/, () => db.registries],
  [
    "POST",
    /^\/registries$/,
    ({ body }) => {
      const r = { id: newId("reg"), host: String(body.host || "docker.io"), username: String(body.username), password: MASK, createdAt: now() };
      db.registries.push(r);
      save();
      return r;
    },
  ],
  [
    "PUT",
    /^\/registries\/([^/]+)$/,
    ({ m, body }) => {
      const r = db.registries.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      Object.assign(r, { host: body.host, username: body.username });
      save();
      return r;
    },
  ],
  [
    "DELETE",
    /^\/registries\/([^/]+)$/,
    ({ m }) => {
      db.registries = db.registries.filter((r) => r.id !== m[1]);
      save();
    },
  ],
  ["POST", /^\/registries\/([^/]+)\/test$/, ({ q }) => (q.has("progress") ? progressed(["login"], undefined) : undefined)],
  ["POST", /^\/git-providers\/([^/]+)\/test$/, ({ q }) => (q.has("progress") ? progressed(["token", "repos"], undefined) : undefined)],
  ["POST", /^\/cloudflare\/test$/, ({ q }) => (q.has("progress") ? progressed(["zones"], undefined) : undefined)],
];
