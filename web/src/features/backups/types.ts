import type { ServiceKind } from "@/features/services/types";

export type OpStatus = "running" | "succeeded" | "failed";

export type TargetKindName = "local" | "s3";

export type BackupTarget = {
  id: string;
  name: string;
  kind: TargetKindName;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  accessKey: string;
  secretKey: string;
  useSsl: boolean;
  ageRecipient: string;
  createdAt: string;
};

export type TargetInput = Omit<BackupTarget, "id" | "kind" | "createdAt" | "ageRecipient"> & {
  kind?: TargetKindName;
  encrypt?: boolean;
  /** An existing age key (another manager's) instead of a new one. */
  ageIdentity?: string;
};

/** What scanning a storage for another manager's backups found. */
export type ScanResult = {
  adopted: Backup[];
  /** Adopted backups with no service of the same project, name and kind here. */
  orphans: number;
  known: number;
  /** Files without a record: older backups, manager snapshots, anything else. */
  noRecord: number;
  unreadable: { key: string; error: string }[];
};

/** dump: a MySQL, MariaDB or MongoDB dump (serviceKind tells which). */
export type BackupKind = "postgres" | "dump" | "volume" | "manager";

export type Backup = {
  id: string;
  /** postgres: a pg_dump; volume: an archive of a service's volumes. */
  kind: BackupKind;
  /** The PostgreSQL database dumped; absent on older backups (the service's own). */
  database?: string;
  encrypted: boolean;
  serviceId: string;
  projectId: string;
  serviceName: string;
  projectName: string;
  /** The backed-up service's kind; absent for the manager's own backups. */
  serviceKind?: ServiceKind;
  /** The service's icon hint (its icon, else its image's base name). */
  serviceIcon?: string;
  targetId: string;
  objectKey: string;
  status: OpStatus;
  sizeBytes: number;
  sha256: string;
  pgVersion: string;
  /** The volumes in a volume backup's archive. */
  volumes?: string[];
  /** The backed-up service's image; absent on older backups. */
  image?: string;
  /** PostgreSQL extensions the dump uses (plpgsql aside): a restore needs a server that has them. */
  extensions?: string[];
  durationMs: number;
  error?: string;
  createdAt: string;
  finishedAt?: string;
  verifyStatus?: OpStatus;
  verifyError?: string;
  /** The backup an anonymized copy was made from. */
  sourceId?: string;
  /** Where a running backup (or anonymized copy) is at, and how much it uploaded so far. */
  step?: "prebackup" | "prepare" | "snapshot" | "helper" | "upload" | "image" | "server" | "restore" | "anonymize";
  stepBytes?: number;
  /** Where a running restore test is at. */
  verifyStep?: "queued" | "image" | "server" | "restore" | "read" | "check";
  verifyDetails: { tables: number; rows: number; dbBytes: number; files?: number; durationMs: number };
  verifiedAt?: string;
};

export type ScheduleInput = {
  targetId: string;
  /** One of a PostgreSQL instance's databases; empty follows the service's own. */
  database?: string;
  cron: string;
  keepLast: number;
  keepDaily: number;
  keepWeekly: number;
  keepMonthly: number;
  enabled: boolean;
  verify: boolean;
};

export type Schedule = ScheduleInput & {
  id: string;
  kind: BackupKind;
  serviceId?: string;
  createdAt: string;
  nextRun?: string;
};

export type Restore = {
  id: string;
  backupId: string;
  serviceId: string;
  status: OpStatus;
  error?: string;
  createdAt: string;
  finishedAt?: string;
  /** Where a running restore is at. */
  step?: "stop" | "check" | "scratch" | "load" | "swap" | "extract" | "stopservice" | "replace" | "start";
};
