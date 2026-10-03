import { Cloud, HardDrive } from "lucide-react";
import type { ReactNode } from "react";
import { GoogleDriveIcon, ProtonDriveIcon } from "@/components/brand-icons";
import type { BackupTarget, TargetKindName } from "@/api";

/** Brand marks by storage kind. */
export const storageIcons: Record<TargetKindName, ReactNode> = {
  local: <HardDrive />,
  s3: <Cloud />,
  gdrive: <GoogleDriveIcon className="text-[#4285F4]" />,
  protondrive: <ProtonDriveIcon className="text-[#EB508D]" />,
};

export function storageLocation(t: BackupTarget) {
  const folder = t.prefix ? `/${t.prefix}` : "/";
  switch (t.kind) {
    case "local":
      return "/data/backups on the manager's volume";
    case "s3":
      return `${t.useSsl ? "https" : "http"}://${t.endpoint}/${t.bucket}/${t.prefix}`;
    case "gdrive":
      return `Google Drive ${folder}`;
    case "protondrive":
      return `Proton Drive ${folder}${t.settings?.account ? ` · ${t.settings.account}` : ""}`;
  }
}
