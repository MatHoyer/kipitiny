import { Cloud, HardDrive } from "lucide-react";
import type { ReactNode } from "react";
import type { BackupTarget } from "@/api";
import { AmazonS3Icon, BackblazeIcon, CloudflareIcon, HetznerIcon } from "@/components/brand-icons";
import { Mono } from "@/components/common";

/**
 * A well-known S3 provider: the form asks for its location (account,
 * region…) and derives the endpoint. "other" takes any endpoint.
 */
export type StorageProvider = {
  id: "r2" | "aws" | "b2" | "hetzner" | "other";
  label: string;
  icon: ReactNode;
  description: string;
  help: ReactNode;
  /** Endpoints of this provider, for its mark and label. */
  hosts?: RegExp;
  location?: {
    label: string;
    placeholder?: string;
    description?: ReactNode;
    /** A fixed list (a select); free text otherwise. */
    options?: string[];
    pattern?: string;
    /** The endpoint and region a location (and variant) gives. */
    endpoint: (loc: string, variant: string) => string;
    region: (loc: string) => string;
    /** The location and variant an endpoint holds; null when the form can't edit it. */
    parse: (endpoint: string) => { location: string; variant: string } | null;
  };
  /** A second choice shaping the endpoint, e.g. R2's jurisdiction; its first option is the default. */
  variant?: {
    label: string;
    description?: ReactNode;
    options: { value: string; label: string }[];
  };
  accessKey: string;
  secretKey: string;
};

const parser = (re: RegExp) => (endpoint: string) => {
  const m = re.exec(endpoint);
  return m ? { location: m[1], variant: m[2]?.toLowerCase() ?? "default" } : null;
};

export const storageProviders: StorageProvider[] = [
  {
    id: "r2",
    label: "Cloudflare R2",
    icon: <CloudflareIcon className="text-[#F38020]" />,
    description: "Cloudflare's object storage, no egress fees.",
    help: (
      <>
        An R2 API token with <Mono>Object Read & Write</Mono> on the bucket (R2 › Manage API tokens).
      </>
    ),
    hosts: /(^|\.)r2\.cloudflarestorage\.com(:\d+)?$/i,
    location: {
      label: "Account ID",
      placeholder: "0123456789abcdef0123456789abcdef",
      description: "In the R2 overview, or in the S3 API URL.",
      pattern: "[0-9a-fA-F]{32}",
      endpoint: (id, jurisdiction) => `${id.toLowerCase()}${jurisdiction === "default" ? "" : `.${jurisdiction}`}.r2.cloudflarestorage.com`,
      region: () => "auto",
      parse: parser(/^([0-9a-f]{32})(?:\.(eu|fedramp))?\.r2\.cloudflarestorage\.com$/i),
    },
    variant: {
      label: "Jurisdiction",
      description: "As chosen when the bucket was created.",
      options: [
        { value: "default", label: "Default" },
        { value: "eu", label: "European Union" },
        { value: "fedramp", label: "FedRAMP" },
      ],
    },
    accessKey: "Access Key ID",
    secretKey: "Secret Access Key",
  },
  {
    id: "aws",
    label: "AWS S3",
    icon: <AmazonS3Icon className="text-[#569A31]" />,
    description: "Amazon S3, in any region.",
    help: (
      <>
        An IAM access key allowed <Mono>s3:ListBucket</Mono>, <Mono>GetObject</Mono>, <Mono>PutObject</Mono> and{" "}
        <Mono>DeleteObject</Mono> on the bucket.
      </>
    ),
    hosts: /(^|\.)amazonaws\.com(:\d+)?$/i,
    location: {
      label: "Region",
      placeholder: "eu-west-3",
      description: "The bucket's region.",
      pattern: "[a-z]{2}(-[a-z]+)+-\\d",
      endpoint: (r) => `s3.${r}.amazonaws.com`,
      region: (r) => r,
      parse: parser(/^s3\.([a-z0-9-]+)\.amazonaws\.com$/),
    },
    accessKey: "Access key ID",
    secretKey: "Secret access key",
  },
  {
    id: "b2",
    label: "Backblaze B2",
    icon: <BackblazeIcon className="text-[#E21E29]" />,
    description: "Low-cost storage, S3-compatible API.",
    help: "An application key with read and write access to the bucket.",
    hosts: /(^|\.)backblazeb2\.com(:\d+)?$/i,
    location: {
      label: "Region",
      placeholder: "eu-central-003",
      description: (
        <>
          From the bucket's endpoint, <Mono>s3.REGION.backblazeb2.com</Mono>.
        </>
      ),
      pattern: "[a-z]+-[a-z]+-\\d{3}",
      endpoint: (r) => `s3.${r}.backblazeb2.com`,
      region: (r) => r,
      parse: parser(/^s3\.([a-z0-9-]+)\.backblazeb2\.com$/),
    },
    accessKey: "keyID",
    secretKey: "applicationKey",
  },
  {
    id: "hetzner",
    label: "Hetzner",
    icon: <HetznerIcon className="text-[#D50C2D]" />,
    description: "Hetzner Object Storage, in Europe.",
    help: "S3 credentials from the Hetzner Console (Security › S3 credentials).",
    hosts: /(^|\.)your-objectstorage\.com(:\d+)?$/i,
    location: {
      label: "Location",
      description: "The bucket's location.",
      options: ["fsn1", "nbg1", "hel1"],
      endpoint: (l) => `${l}.your-objectstorage.com`,
      region: (l) => l,
      parse: parser(/^([a-z]+\d)\.your-objectstorage\.com$/),
    },
    accessKey: "Access key",
    secretKey: "Secret key",
  },
  {
    id: "other",
    label: "Other S3-compatible",
    icon: <Cloud />,
    description: "MinIO, Garage, Scaleway, OVH… any endpoint.",
    help: "Credentials and endpoint as your provider gives them.",
    accessKey: "Access key",
    secretKey: "Secret key",
  },
];

const otherProvider = storageProviders[storageProviders.length - 1];

/** The provider an S3 storage's endpoint belongs to. */
export function storageProvider(t: Pick<BackupTarget, "endpoint">) {
  return storageProviders.find((p) => p.hosts?.test(t.endpoint)) ?? otherProvider;
}

/**
 * The form editing a storage: its provider's when the provider's form can
 * hold it as it is, the full form otherwise.
 */
export function storageForm(t: BackupTarget): { provider: StorageProvider; location: string; variant: string } {
  const p = storageProvider(t);
  const parsed = p.location?.parse(t.endpoint);
  if (!p.location || !parsed || !t.useSsl || t.region !== p.location.region(parsed.location)) {
    return { provider: otherProvider, location: "", variant: "" };
  }
  return { provider: p, ...parsed };
}

/** A storage's mark: its provider's for S3, else its kind's. */
export function storageIcon(t: BackupTarget) {
  return t.kind === "local" ? <HardDrive /> : storageProvider(t).icon;
}

/** What a storage is, under its name. */
export function storageKind(t: BackupTarget) {
  if (t.kind === "local") return "Built in";
  const p = storageProvider(t);
  return p === otherProvider ? "S3-compatible" : p.label;
}

export function storageLocation(t: BackupTarget) {
  switch (t.kind) {
    case "local":
      return "/data/backups on the manager's volume";
    case "s3":
      return `${t.useSsl ? "https" : "http"}://${t.endpoint}/${t.bucket}/${t.prefix}`;
  }
}
