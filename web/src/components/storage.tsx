import { Cloud, HardDrive } from "lucide-react";
import type { ReactNode } from "react";
import type { BackupTarget, TargetKindName } from "@/api";
import { CloudflareIcon } from "@/components/brand-icons";

/** Marks by storage kind. */
export const storageIcons: Record<TargetKindName, ReactNode> = {
  local: <HardDrive />,
  s3: <Cloud />,
};

const r2Icon = <CloudflareIcon className="text-[#F38020]" />;

/** A storage's mark: its provider's when the endpoint tells it, else its kind's. */
export function storageIcon(t: BackupTarget) {
  if (t.kind === "s3" && /(^|\.)r2\.cloudflarestorage\.com(:\d+)?$/i.test(t.endpoint)) {
    return r2Icon;
  }
  return storageIcons[t.kind];
}

export function storageLocation(t: BackupTarget) {
  switch (t.kind) {
    case "local":
      return "/data/backups on the manager's volume";
    case "s3":
      return `${t.useSsl ? "https" : "http"}://${t.endpoint}/${t.bucket}/${t.prefix}`;
  }
}
