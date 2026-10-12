import { Stepper, type Step } from "@/components/ui/stepper";
import { databaseLabels } from "@/components/brand-icons";
import { formatBytes } from "@/lib/format";
import type { Backup, DatabaseKind, Restore } from "@/api";

type Planned<Id> = Step & { id: Id };

/** Shows where a running operation is in plan, from the step the manager reports. */
function OpSteps<Id>({ plan, step }: { plan: Planned<Id>[]; step?: Id }) {
  const current = plan.findIndex((s) => s.id === step);
  return <Stepper orientation="vertical" steps={plan} current={Math.max(current, 0)} busy />;
}

/** A running backup, step by step, with what it uploaded so far. */
export function BackupSteps({ backup: b }: { backup: Backup }) {
  const uploaded = b.step === "upload" && b.stepBytes ? `${formatBytes(b.stepBytes)} uploaded so far` : undefined;
  const stream = "Streamed straight to the storage, encrypted when it's set to";
  const plan: Planned<NonNullable<Backup["step"]>>[] =
    b.kind === "manager"
      ? [
          { id: "snapshot", title: "Take a snapshot of the manager's database", hint: "A consistent copy, while it keeps running" },
          { id: "upload", title: "Upload it", hint: uploaded ?? stream },
        ]
      : b.kind === "volume"
        ? [
            { id: "prebackup", title: "Run the pre-backup command", hint: "When the service sets one, e.g. to flush to disk" },
            { id: "helper", title: "Start a container reading the volumes", hint: "Read-only, removed afterwards" },
            { id: "upload", title: "Archive and upload them", hint: uploaded ?? stream },
          ]
        : [
            { id: "prepare", title: "Read the server's version", hint: "Recorded, so a restore picks a server that can load it" },
            {
              id: "upload",
              title: `Dump the ${databaseLabels[b.serviceKind as DatabaseKind] ?? "PostgreSQL"} database and upload it`,
              hint: uploaded ?? stream,
            },
          ];
  return <OpSteps plan={plan} step={b.step} />;
}

/** A running restore of backup b, step by step. */
export function RestoreSteps({ restore: r, backup: b }: { restore: Restore; backup: Backup }) {
  const stop = { id: "stop" as const, title: "Stop the apps using it", hint: "So nothing writes while it's replaced" };
  const start = { id: "start" as const, title: "Start them again", hint: "Whether the restore worked or not" };
  const check = { id: "check" as const, title: "Read the backup through", hint: "Checksum, decryption and compression, before touching anything" };
  const plan: Planned<NonNullable<Restore["step"]>>[] =
    b.kind === "volume"
      ? [
          { id: "extract", title: "Extract the archive", hint: "Into a staging volume: the live ones are untouched" },
          { id: "stopservice", title: `Stop ${b.serviceName}`, hint: "Its volumes are about to change" },
          { id: "replace", title: "Replace the volumes' content", hint: "With the staged copy" },
          { id: "start", title: `Start ${b.serviceName} again`, hint: "Whether the restore worked or not" },
        ]
      : b.kind === "dump" && b.serviceKind === "mongodb"
        ? [stop, check, { id: "load", title: "Load it", hint: "Each collection it holds is replaced" }, start]
        : b.kind === "dump"
          ? [
              stop,
              check,
              { id: "scratch", title: "Load it into a scratch database", hint: "A failure here leaves the live one unchanged" },
              { id: "load", title: "Load it into the live database", hint: "Emptied first; its grants stay" },
              start,
            ]
          : [
              stop,
              { id: "load", title: "Load it into a scratch database", hint: "The live one is untouched until it fully loads" },
              { id: "swap", title: "Swap it in", hint: "The old database is dropped once replaced" },
              start,
            ];
  return <OpSteps plan={plan} step={r.step} />;
}
