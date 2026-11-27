/**
 * A file, folder or symlink in a service's volumes or container; at the root, a volume or the container.
 * Paths are "<volume>/<path>", or "@container/<absolute path>" ("@<container ID>/..." for a given replica).
 */
export type VolumeEntry = {
  name: string;
  /** "container": at the root, the running container's own filesystem (path "@container"). */
  type: "volume" | "container" | "dir" | "file" | "link" | "other";
  size: number;
  /** Absent for volumes. */
  modified?: string;
  /** Permission bits, octal. */
  mode?: string;
  uid: number;
  gid: number;
  /** A symlink's, not followed. */
  target?: string;
  /** Where a volume is mounted in the service's containers. */
  mountPath?: string;
};

export type VolumeListing = { path: string; readOnly: boolean; entries: VolumeEntry[]; truncated: boolean };

export type VolumeFile = VolumeEntry & { path: string; readOnly: boolean; content: string };
