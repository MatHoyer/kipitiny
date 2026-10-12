import { useEffect, useState } from "react";
import { Stepper, type Step } from "@/components/ui/stepper";
import { databaseLabels } from "@/components/brand-icons";
import { formatBytes } from "@/lib/format";
import type { Backup, DatabaseKind, OpStatus, Restore } from "@/api";

type Planned<Id> = Step & { id: Id };

/**
 * The last step an operation reported while running, kept once it ends (a finished operation reports none), so
 * its steps stay on screen with the outcome. Null when it wasn't seen running here.
 */
export function useLastStep<T>(running: boolean, step: T | undefined): { step?: T } | null {
  const [last, setLast] = useState<{ step?: T } | null>(null);
  useEffect(() => {
    if (running) setLast((l) => ({ step: step ?? l?.step }));
  }, [running, step]);
  return last;
}

/** Where an operation is in plan, from the step the manager reports: all done once it succeeded, a cross where it failed. */
function OpSteps<Id>({ plan, step, status }: { plan: Planned<Id>[]; step?: Id; status?: OpStatus }) {
  const current = status === "succeeded" ? plan.length : Math.max(plan.findIndex((s) => s.id === step), 0);
  return <Stepper orientation="vertical" steps={plan} current={current} busy={status === "running"} failed={status === "failed"} />;
}

/** A backup step by step, with what it uploaded so far; step is the last one seen once it ended. */
export function BackupSteps({ backup: b, step = b.step }: { backup: Backup; step?: Backup["step"] }) {
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
  return <OpSteps plan={plan} step={step} status={b.status} />;
}

/** A restore of backup b, step by step; step is the last one seen once it ended. */
export function RestoreSteps({ restore: r, backup: b, step = r.step }: { restore: Restore; backup: Backup; step?: Restore["step"] }) {
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
  return <OpSteps plan={plan} step={step} status={r.status} />;
}

/** A restore test of b, step by step; step is the last one seen once it ended. */
export function VerifySteps({ backup: b, server, step = b.verifyStep }: { backup: Backup; server: string; step?: Backup["verifyStep"] }) {
  const plan: Planned<NonNullable<Backup["verifyStep"]>>[] =
    b.kind === "volume"
      ? [
          { id: "server", title: "Start a throwaway container", hint: "No network, removed after the test" },
          { id: "read", title: "Download and read the archive", hint: "Decrypt, decompress and list every file" },
        ]
      : [
          { id: "image", title: `Get the ${server} image`, hint: "The one the database runs, or that made the backup" },
          { id: "server", title: `Start a throwaway ${server}`, hint: "No network, 512MB, removed after the test" },
          { id: "restore", title: "Download and load the backup", hint: "Decrypted if needed, then restored as a real restore would" },
          { id: "check", title: "Measure what came back", hint: b.serviceKind === "mongodb" ? "Collections, documents and size" : "Tables, rows and size" },
        ];
  return (
    <div className="grid gap-3">
      {step === "queued" && b.verifyStatus === "running" && (
        <p className="text-sm text-muted-foreground">Waiting for another restore test to finish: they run one at a time.</p>
      )}
      <OpSteps plan={plan} step={step} status={b.verifyStatus} />
    </div>
  );
}
