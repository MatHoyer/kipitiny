import type { Backup } from "@/features/backups/types";
import type { Maintenance, ServiceKind } from "@/features/services/types";

export type Project = {
  id: string;
  name: string;
  serverId: string;
  /** Shared variables and secrets; services reference them as {{ project.NAME }}. */
  env: Record<string, string>;
  /** Names of the env entries that are secrets: their values come back masked. */
  secrets: string[];
  /** Set on a preview environment: the project it copies. */
  parentId?: string;
  /** What a preview was last deployed from; empty for other projects. */
  preview?: PreviewInfo;
  /** Seconds without a request to its apps before every service stops; 0 never. */
  sleepAfterSeconds: number;
  /** The page visitors of its apps get while one can't answer, and maintenance mode. */
  maintenance: Maintenance;
  /** Lowercase names grouping projects in the UI. */
  tags: string[];
  createdAt: string;
  updatedAt: string;
};

/** A tag in use, with its color and how many visible projects and services carry it. */
export type TagInfo = {
  name: string;
  color: string;
  /** A user picked the color; else it derives from the name. */
  custom: boolean;
  projects: number;
  services: number;
};

export type TagList = { tags: TagInfo[]; colors: string[] };

export type PreviewInfo = {
  name?: string;
  branch?: string;
  commit?: string;
  /** Closed after this long without a request or a deploy; 0 never. */
  ttlSeconds?: number;
  deployedAt?: string;
};

/** A preview with where its apps are, by service name. */
export type PreviewView = Project & { urls: Record<string, string> };

/** How a project's previews differ from it. */
export type PreviewSettings = {
  /** Previews' apps are at <preview>-<service>.<baseDomain>. */
  baseDomain: string;
  /** Project variables of every preview, instead of the project's; secrets come back masked. */
  env: Record<string, string>;
  secrets: string[];
  /** The project's secrets previews may still use. */
  inherit: string[];
  ttlSeconds: number;
  sleepAfterSeconds: number;
  /** What --seed fills new previews' databases with: nothing, anonymized backups, or production data as is. */
  seed: SeedMode;
};

export type SeedMode = "" | "anonymized" | "raw";

/** A database's anonymize rules, by table: truncate, exclude, or rules by column (keep, null, hash, fixed:<value>, fake:<kind>). */
export type AnonymizeRules = Record<string, string | Record<string, string>>;

/** A database of a project, as previews are seeded with it. */
export type SeedDatabase = {
  serviceId: string;
  name: string;
  kind: ServiceKind;
  /** null: no rules, so no anonymized seeds; {}: nothing personal. */
  rules: AnonymizeRules | null;
  /** Its latest backup a preview can be seeded with. */
  backup?: Backup;
  /** The latest anonymized copy made with the current rules, whatever its status. */
  copy?: Backup;
  /** The copy previews get: the latest good one with the current rules. */
  seeds?: Backup;
};

/** Where a map canvas node was dragged to, relative to its parent frame. */
export type CanvasPoint = { x: number; y: number };

export interface ComposePlan {
  create: string[];
  update: { name: string; fields: string[] }[];
  unchanged: string[];
  delete: string[];
  orphaned: string[];
  variables: string[];
  warnings: string[];
  deploying: string[];
}

/** A one-click app: a compose file and the inputs its variables come from. */
export interface AppTemplate {
  id: string;
  title: string;
  description: string;
  website?: string;
  docs?: string;
  icon?: string;
  /** Markdown shown after the description: paragraphs, lists, **bold**, `code` and [links](…). */
  notes?: string;
  category: string;
  tags: string[];
  inputs: TemplateInput[];
  /** The default project name. */
  project: string;
  compose: string;
}

/** A service logo a template brings, for services whose icon is name or whose image is one of images. */
export interface TemplateLogo {
  name: string;
  label: string;
  color: string;
  images: string[];
  svg: string;
}

export interface TemplateInput {
  name: string;
  label: string;
  type: "text" | "secret" | "domain" | "url" | "select" | "checkbox";
  /** The choices of a select. */
  options?: string[];
  help?: string;
  placeholder?: string;
  default?: string;
  required?: boolean;
  /** Random bytes the manager generates; not asked. */
  generate?: number;
  hex?: boolean;
}

export interface TemplateInstall {
  projectId?: string;
  newProject?: { name: string; serverId?: string };
  values: Record<string, string>;
  dryRun?: boolean;
}

export interface TemplateResult {
  projectId?: string;
  plan: ComposePlan;
}

export interface GitInput {
  repoUrl: string;
  branch: string;
  path: string;
  /** The git provider reading a private repository; "" for a public one. */
  providerId: string;
  autoSync: boolean;
  pollSeconds: number;
}

export interface GitStatus extends GitInput {
  projectId: string;
  webhookSecret: string;
  webhookPath: string;
  lastCommit: string;
  lastSyncedAt?: string;
  lastError: string;
  warnings: string[];
  applied: Record<string, string>;
  /** Services deployed with another image than the file's (a tag from CI). */
  drift: string[];
}
