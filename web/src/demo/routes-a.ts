import { helperSteps } from "./ops";
import { SERVER } from "./state";
import type { Account, AuthState, SecretConnection, SecretItem, CanvasPoint, AnonymizeRules, Project, Status, Theme } from "@/api";
import { autoTagColor, normalizeTag, tagColors } from "@/lib/tags";
import { service as newService } from "./seed";
import { demoLogos, demoTemplates } from "./templates";

import { connection, passwordManager, progressed, scheduled, secretConnections, tickDeploys, vaults, type Route } from "./ops";
import { cleanupStart, cleanupView } from "./routes-c";
import { HttpError, MASK, db, dbDemo, demoTagList, demoTags, invalid, isDb, nameRe, newId, notFound, now, projectOf, save, serviceOf, view } from "./state";
import { hostStats, securityReport, topology } from "./stats";
import { compose, demoNotices, demoPreviewSettings, demoSeedRules, previewSeed, previewSettings } from "./views";

export const routesA: Route[] = [
  [
    "GET",
    /^\/auth\/state$/,
    (): AuthState => ({
      setupRequired: false,
      user: { id: "demo", username: "demo", role: "admin", features: [], createdAt: now(), preferences: db.preferences ?? {} },
      sso: [],
      ssoRequired: false,
      branding: db.branding ?? {},
    }),
  ],
  [
    "PUT",
    /^\/branding$/,
    ({ body }) => {
      const theme: Theme = {};
      for (const k of ["accent", "accentDark", "tint"] as const) {
        const v = String(body[k] ?? "").toLowerCase();
        if (v && !/^#[0-9a-f]{6}$/.test(v)) throw invalid(`the ${k} must look like #4f46e5`);
        theme[k] = v || undefined;
      }
      const choices = { radius: ["none", "small", "medium", "large", "full"], font: ["geist", "system", "serif", "mono"], density: ["comfortable", "compact"] };
      for (const [k, options] of Object.entries(choices) as [keyof typeof choices, string[]][]) {
        const v = String(body[k] ?? "");
        if (v && !options.includes(v)) throw invalid(`${k} must be one of ${options.join(", ")}`);
        (theme as Record<string, string | undefined>)[k] = v || undefined;
      }
      const logo = body.logo === undefined ? db.branding?.logo : String(body.logo) || undefined;
      db.branding = { ...theme, name: String(body.name ?? "").trim() || undefined, logo };
      return db.branding;
    },
  ],
  ["DELETE", /^\/branding$/, () => (db.branding = {})],
  ["PUT", /^\/account\/preferences$/, ({ body }) => ((db.preferences = body.mapView === "3d" ? { mapView: "3d" } : {}), save(), db.preferences)],
  [
    "GET",
    /^\/account$/,
    (): Account => ({
      username: "demo",
      totpEnabled: false,
      recoveryCodes: 0,
      passkeys: [],
      hasPassword: true,
      confirmed: false,
      sso: [],
      ssoProviders: (db.ssoProviders ?? []).map((p) => ({ id: p.id, name: p.name })),
      ssoRequired: false,
      role: "admin",
      features: [],
      grants: {},
    }),
  ],
  // A phone already gets pushes; this browser can't subscribe without a real manager's key.
  ["GET", /^\/account\/push$/, () => ({ publicKey: "", devices: db.pushRemoved ? [] : [{ id: "push1", name: "Chrome on Android", endpoint: "https://fcm.googleapis.com/fcm/send/demo", createdAt: now() }] })],
  [
    "POST",
    /^\/account\/push$/,
    () => {
      throw invalid("this is a demo: push notifications need a real manager");
    },
  ],
  ["DELETE", /^\/account\/push\/[^/]+$/, () => ((db.pushRemoved = true), save())],
  [
    "GET",
    /^\/status$/,
    (): Status => ({
      version: "demo",
      // An update is always on offer, to show its checks; installing it is refused.
      update: { current: "demo", latest: "demo-next", available: true, canApply: true, notices: demoNotices, updating: false },
      docker: { version: "27.3.1", apiVersion: "1.47", os: "linux", arch: "amd64" },
      database: { sizeBytes: 3_457_024, walBytes: 129_832 },
    }),
  ],
  ["POST", /^\/auth\/logout$/, () => undefined],

  ["GET", /^\/servers$/, () => [{ ...SERVER, projects: db.projects.length }]],
  ["GET", /^\/servers\/[^/]+\/stats$/, hostStats],
  ["GET", /^\/servers\/[^/]+\/security$/, securityReport],
  ["POST", /^\/servers\/[^/]+\/security\/check$/, () => ({ ...securityReport(), checkedAt: new Date().toISOString() })],
  [
    "POST",
    /^\/servers\/[^/]+\/security\/updates$/,
    () => {
      throw new Error("this is a demo: it doesn't install anything on its host");
    },
  ],
  [
    "POST",
    /^\/servers\/[^/]+\/reboot$/,
    () => {
      throw new Error("this is a demo: it can't reboot its host");
    },
  ],
  [
    "POST",
    /^\/servers$/,
    ({ q }) => {
      const refused = new Error("this is a demo: it can't reach other servers (ssh: connect to host: connection refused)");
      if (!q.has("progress")) throw invalid(refused.message);
      return progressed(["connect"], refused);
    },
  ],
  ["POST", /^\/update$/, () => { throw invalid("this is a demo running in your browser: there's nothing to install"); }],
  ["POST", /^\/update\/check$/, () => ({ current: "demo", latest: "demo-next", available: true, canApply: true, notices: demoNotices, updating: false, checkedAt: now() })],
  [
    "GET",
    /^\/update\/preflight$/,
    () => {
      cleanupView(); // moves a running cleanup along
      const busy = cleanupStart !== null;
      return {
        ready: !busy,
        checks: [
          { id: "version", state: "ok", detail: "demo → demo-next" },
          busy
            ? { id: "tasks", state: "block", detail: "in progress: the cleanup. The restart would cut it short" }
            : { id: "tasks", state: "ok", detail: "no deploy, backup, restore or cleanup running" },
          { id: "docker", state: "ok", detail: "Docker 27.3.1 answers" },
          { id: "backup", state: "warn", detail: "the last manager backup is 4 days old: back it up first to have a way back" },
        ],
      };
    },
  ],
  ["GET", /^\/ssh-key$/, () => ({ publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDemoDemoDemoDemoDemoDemoDemoDemoDemoDemo kipitiny" })],
  ["GET", /^\/domains$/, () => db.domains],
  ["GET", /^\/secret-providers$/, () => [passwordManager()]],
  ["GET", /^\/secret-connections$/, () => secretConnections()],
  [
    "POST",
    /^\/secret-connections$/,
    ({ q, body }) => {
      if (body.kind !== "protonpass") throw notFound();
      const name = String(body.name ?? "").trim();
      if (name && !/^[a-z][a-z0-9-]{0,30}$/.test(name)) throw invalid("name must be lowercase letters, digits and dashes, starting with a letter");
      if (secretConnections().some((c) => c.kind === body.kind && c.name === name)) {
        throw invalid(name ? `Proton Pass already has a connection named ${name}` : "Proton Pass already has a default connection: name this one");
      }
      if (!String(body.token ?? "").trim()) throw invalid("a token is required");
      const c: SecretConnection = {
        id: newId("sc"),
        kind: "protonpass",
        name,
        scheme: name ? `pass+${name}` : "pass",
        providerName: "Proton Pass",
        createdAt: new Date().toISOString(),
      };
      db.secretConnections = [...secretConnections(), c].sort((a, b) => a.name.localeCompare(b.name));
      save();
      return q.has("progress") ? progressed([...helperSteps(), "login", "verify"], c) : c;
    },
  ],
  [
    "DELETE",
    /^\/secret-connections\/([^/]+)$/,
    ({ m }) => {
      db.secretConnections = secretConnections().filter((c) => c.id !== m[1]);
      save();
    },
  ],
  [
    "POST",
    /^\/secret-connections\/([^/]+)\/test$/,
    ({ m, q }) => (connection(m[1]), q.has("progress") ? progressed([...helperSteps(), "session"], undefined) : undefined),
  ],
  ["GET", /^\/secret-connections\/([^/]+)\/vaults$/, ({ m }) => (connection(m[1]), Object.keys(vaults))],
  [
    "GET",
    /^\/secret-connections\/([^/]+)\/items$/,
    ({ m, q }): SecretItem[] => {
      const { scheme } = connection(m[1]);
      const vault = q.get("vault") ?? "";
      return (vaults[vault] ?? []).map(([title, fields]) => ({ title, fields: fields.map((name) => ({ name, ref: `${scheme}://${vault}/${title}/${name}` })) }));
    },
  ],
  ["GET", /^\/topology$/, () => topology()],
  ["GET", /^\/projects\/([^/]+)\/topology$/, ({ m }) => topology(m[1])],
  ["GET", /^\/canvas$/, () => ({ positions: db.canvas })],
  [
    "PUT",
    /^\/canvas$/,
    ({ body }) => {
      db.canvas = { ...db.canvas, ...(body.positions as Record<string, CanvasPoint>) };
      save();
    },
  ],
  [
    "DELETE",
    /^\/canvas$/,
    () => {
      db.canvas = {};
      save();
    },
  ],

  ["GET", /^\/projects$/, () => db.projects],
  [
    "POST",
    /^\/projects$/,
    ({ body }) => {
      const name = String(body.name ?? "").trim();
      if (!nameRe.test(name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      if (db.projects.some((p) => p.name === name)) throw new HttpError(409, "already exists (name or domain taken)");
      const p: Project = { id: newId("p"), name, serverId: "local", env: {}, secrets: [], sleepAfterSeconds: 0, maintenance: { enabled: false }, tags: [], createdAt: now(), updatedAt: now() };
      db.projects.push(p);
      save();
      return p;
    },
  ],
  ["GET", /^\/templates$/, () => demoTemplates.map(({ services: _, ...t }) => t)],
  ["GET", /^\/templates\/logos$/, () => demoLogos],
  [
    "POST",
    /^\/templates\/([^/]+)\/install$/,
    ({ m, body }) => {
      const t = demoTemplates.find((t) => t.id === m[1]);
      if (!t) throw notFound();
      const values: Record<string, string> = body.values ?? {};
      const missing = t.inputs.filter((i) => i.required && (i.type === "checkbox" ? values[i.name] !== "true" : !values[i.name]?.trim()));
      if (missing.length) throw invalid(missing.map((i) => (i.type === "checkbox" ? `${i.label} must be checked` : `${i.label} is required`)).join("; "));
      const name = String(body.newProject?.name ?? "").trim();
      if (!body.projectId && !nameRe.test(name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      const taken = body.projectId ? db.services.filter((s) => s.projectId === projectOf(body.projectId).id).map((s) => s.name) : [];
      const clash = t.services.find((s) => taken.includes(s.name));
      if (clash) throw invalid(`service ${clash.name}: already exists`);
      const names = t.services.map((s) => s.name);
      const plan = { create: names, update: [], unchanged: [], delete: [], orphaned: [], variables: [], warnings: [], deploying: body.dryRun ? [] : names };
      if (body.dryRun) return { projectId: body.projectId, plan };
      let projectId = body.projectId;
      if (!projectId) {
        const p: Project = { id: newId("p"), name, serverId: "local", env: {}, secrets: [], sleepAfterSeconds: 0, maintenance: { enabled: false }, tags: [], createdAt: now(), updatedAt: now() };
        db.projects.push(p);
        projectId = p.id;
      }
      // Databases deploy first, then the apps once they're up, as the manager does.
      const dbsFor = t.services.some(isDb) ? 2600 : 0;
      for (const { domainInput, ...svc } of t.services) {
        const s = newService({
          ...svc,
          id: newId("s"),
          projectId,
          domain: domainInput ? (values[domainInput] ?? "") : "",
          currentDeploymentId: "",
          createdAt: now(),
          updatedAt: now(),
        });
        db.services.push(s);
        const start = Date.now() + (isDb(s) ? 0 : dbsFor);
        scheduled.push({ serviceId: s.id, start, end: start + (isDb(s) ? 2400 : 3000) });
      }
      tickDeploys();
      return { projectId, plan };
    },
  ],
  ["GET", /^\/projects\/([^/]+)$/, ({ m }) => projectOf(m[1])],
  ["GET", /^\/projects\/([^/]+)\/git$/, () => { throw notFound(); }],
  // One preview, opened by CI for a stack of pull requests; its project isn't simulated.
  [
    "GET",
    /^\/projects\/([^/]+)\/previews$/,
    ({ m }) => {
      const p = projectOf(m[1]);
      if (p.id !== "acme") return [];
      const preview = { name: "pr-42", branch: "feat/checkout-v2", commit: "9f2c4e1a", ttlSeconds: 7 * 86_400, deployedAt: now() };
      return [{ ...p, id: "acme-pr-42", name: "acme-pr-42", parentId: p.id, preview, urls: { api: "https://pr-42-api.preview.acme.example", web: "https://pr-42-web.preview.acme.example" } }];
    },
  ],
  ["DELETE", /^\/projects\/([^/]+)\/previews\/([^/]+)$/, () => null],
  ["GET", /^\/projects\/([^/]+)\/preview-settings$/, ({ m }) => previewSettings(m[1])],
  [
    "PUT",
    /^\/projects\/([^/]+)\/preview-settings$/,
    ({ m, body }) => {
      const old = previewSettings(m[1]);
      const env = Object.fromEntries(Object.entries((body.env ?? {}) as Record<string, string>).map(([k, v]) => [k, v === MASK ? (old.env[k] ?? "") : v]));
      demoPreviewSettings.set(m[1], { ...old, ...body, env });
      return previewSettings(m[1]);
    },
  ],
  ["GET", /^\/projects\/([^/]+)\/preview-seed$/, ({ m }) => previewSeed(m[1])],
  [
    "PUT",
    /^\/projects\/([^/]+)\/preview-seed\/([^/]+)$/,
    ({ m, body }) => {
      demoSeedRules.set(m[2], (body.rules ?? null) as AnonymizeRules | null);
      return previewSeed(m[1]).find((d) => d.name === m[2]);
    },
  ],
  ["POST", /^\/projects\/([^/]+)\/preview-seed\/([^/]+)\/anonymize$/, ({ m }) => previewSeed(m[1]).find((d) => d.name === m[2])?.seeds],
  ["GET", /^\/projects\/([^/]+)\/compose$/, ({ m, q }) => compose(m[1], q.get("service") ?? undefined)],
  // The preview's services: the parent's, asleep (its project isn't simulated).
  [
    "GET",
    /^\/projects\/acme-pr-42\/services$/,
    () =>
      db.services
        .filter((s) => s.projectId === "acme")
        .map((s) => ({ ...view(s), id: `${s.id}-pr-42`, projectId: "acme-pr-42", sleeping: true, containers: [] })),
  ],
  ["GET", /^\/projects\/([^/]+)\/services$/, ({ m }) => db.services.filter((s) => s.projectId === projectOf(m[1]).id).map(view)],
  [
    "POST",
    /^\/projects\/([^/]+)\/services$/,
    ({ m, body }) => {
      const p = projectOf(m[1]);
      const name = String(body.name ?? "").trim();
      if (!nameRe.test(name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      if (db.services.some((s) => s.projectId === p.id && s.name === name)) throw new HttpError(409, "already exists (name or domain taken)");
      const kind = body.kind ?? "app";
      const image = body.image || (dbDemo[kind]?.image ?? "");
      if (!image) throw invalid("image: repository name must have at least one component");
      const s = newService({
        id: newId("s"),
        name,
        kind,
        image,
        projectId: p.id,
        replicas: kind === "app" ? Number(body.replicas) || 1 : 1,
        port: Number(body.port) || 0,
        domain: body.domain ?? "",
        env: body.env ?? {},
        secrets: body.secrets ?? [],
        memoryMb: Number(body.memoryMb) || 0,
        currentDeploymentId: "",
        createdAt: now(),
        updatedAt: now(),
      });
      db.services.push(s);
      save();
      return view(s);
    },
  ],
  [
    "PUT",
    /^\/projects\/([^/]+)\/env$/,
    ({ m, body }) => {
      const p = projectOf(m[1]);
      p.env = body.env ?? {};
      p.secrets = body.secrets ?? [];
      save();
      return p;
    },
  ],
  [
    "PUT",
    /^\/projects\/([^/]+)\/maintenance$/,
    ({ m, body }) => {
      const p = projectOf(m[1]);
      if (body.enabled && !db.services.some((s) => s.projectId === p.id && s.domain && s.port))
        throw invalid("maintenance mode needs an app with a domain and a container port");
      p.maintenance = body;
      save();
      return p;
    },
  ],
  [
    "PUT",
    /^\/projects\/([^/]+)\/tags$/,
    ({ m, body }) => {
      const p = projectOf(m[1]);
      p.tags = demoTags(body.tags);
      save();
      return p;
    },
  ],
  ["GET", /^\/tags$/, () => ({ tags: demoTagList(), colors: tagColors })],
  [
    "PUT",
    /^\/tags\/([^/]+)$/,
    ({ m, body }) => {
      const name = normalizeTag(decodeURIComponent(m[1]));
      const color = String(body.color ?? "");
      if (color && !(tagColors as readonly string[]).includes(color)) throw invalid(`color must be one of ${tagColors.join(", ")}`);
      const colors = (db.tagColors ??= {});
      if (color) colors[name] = color;
      else delete colors[name];
      save();
      return demoTagList().find((t) => t.name === name) ?? { name, color: color || autoTagColor(name), custom: !!color, projects: 0, services: 0 };
    },
  ],
  [
    "PUT",
    /^\/projects\/([^/]+)\/sleep$/,
    ({ m, body }) => {
      const p = projectOf(m[1]);
      const secs = Number(body.sleepAfterSeconds) || 0;
      if (secs && (secs < 300 || secs > 30 * 86400)) throw invalid("sleep after must be between 5 minutes and 30 days");
      const svcs = db.services.filter((s) => s.projectId === p.id);
      if (secs && !svcs.some((s) => s.domain && s.port)) throw invalid("only a project with an app on a domain can sleep");
      p.sleepAfterSeconds = secs;
      if (!secs) for (const s of svcs) s.sleeping = false;
      save();
      return p;
    },
  ],

  ["GET", /^\/services\/([^/]+)$/, ({ m }) => view(serviceOf(m[1]))],
  [
    "PATCH",
    /^\/services\/([^/]+)$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      const patch = { ...body };
      if (patch.env) patch.env = Object.fromEntries(Object.entries(patch.env as Record<string, string>).map(([k, v]) => [k, v === MASK ? (s.env[k] ?? "") : v]));
      delete patch.middlewares;
      const routed = `${s.domain}:${s.port}`;
      Object.assign(s, patch, { updatedAt: now() });
      if (`${s.domain}:${s.port}` !== routed && s.currentDeploymentId && !s.stopped) s.routePending = true;
      save();
      return view(s);
    },
  ],
  [
    "PUT",
    /^\/services\/([^/]+)\/name$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      if (!nameRe.test(body.name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      s.name = body.name;
      save();
      return view(s);
    },
  ],
];
