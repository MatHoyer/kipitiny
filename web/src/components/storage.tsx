import { Cloud, HardDrive } from "lucide-react";
import type { ReactNode } from "react";
import type { BackupTarget, TargetKindName } from "@/api";

/** Marks by storage kind. */
export const storageIcons: Record<TargetKindName, ReactNode> = {
  local: <HardDrive />,
  s3: <Cloud />,
};

export function storageLocation(t: BackupTarget) {
  switch (t.kind) {
    case "local":
      return "/data/backups on the manager's volume";
    case "s3":
      return `${t.useSsl ? "https" : "http"}://${t.endpoint}/${t.bucket}/${t.prefix}`;
  }
}
