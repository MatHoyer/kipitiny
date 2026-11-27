import { Cloud, HardDrive } from "lucide-react";
import type { ReactNode } from "react";
import type { BackupTarget } from "@/api";
import { AmazonS3Icon, BackblazeIcon, CloudflareIcon, HetznerIcon } from "@/components/brand-icons";
import { Mono } from "@/components/common";
import type { HowToStep } from "@/components/how-to";

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
  /** Brand color, tinting the guide. */
  accent?: string;
  /** How to set up the bucket and its keys, for a given bucket name. */
  guide?: (bucket: string) => HowToStep[];
};

const parser = (re: RegExp) => (endpoint: string) => {
  const m = re.exec(endpoint);
  return m ? { location: m[1], variant: m[2]?.toLowerCase() ?? "default" } : null;
};

export const storageProviders: StorageProvider[] = [
  {
    id: "r2",
    accent: "#F38020",
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
    guide: () => [
      {
        title: "Create the bucket",
        text: "In R2 Object Storage, create a bucket. Its jurisdiction (default, EU, FedRAMP) is the one to pick below.",
        link: { href: "https://dash.cloudflare.com/?to=/:account/r2/overview", label: "Open R2" },
      },
      {
        title: "Create an API token",
        text: (
          <>
            R2 › Manage API tokens › Create API token, with the <Mono>Object Read & Write</Mono> permission, applied to this bucket only.
          </>
        ),
      },
      {
        title: "Copy its keys",
        text: (
          <>
            Cloudflare shows the Access Key ID and Secret Access Key once. The Account ID is the first part of the S3 endpoint it shows,{" "}
            <Mono>https://ACCOUNT_ID.r2.cloudflarestorage.com</Mono>.
          </>
        ),
      },
    ],
  },
  {
    id: "aws",
    accent: "#569A31",
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
    guide: (bucket) => [
      {
        title: "Create the bucket",
        text: "In the S3 console, create a bucket with public access blocked. Its region goes below.",
        link: { href: "https://console.aws.amazon.com/s3/bucket/create", label: "Create a bucket" },
      },
      {
        title: "Create a user for kipitiny",
        text: (
          <>
            In IAM › Users, create a user without console access and add this inline policy{bucket ? "" : <> (type the bucket below to fill it in)</>}.
          </>
        ),
        link: { href: "https://console.aws.amazon.com/iam/home#/users/create", label: "Create a user" },
        copy: awsPolicy(bucket || "BUCKET"),
      },
      {
        title: "Create an access key",
        text: "On the user, Security credentials › Create access key, for an application running outside AWS. The secret shows once.",
      },
    ],
  },
  {
    id: "b2",
    accent: "#E21E29",
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
    guide: () => [
      {
        title: "Create the bucket",
        text: (
          <>
            A private bucket. Its card shows the endpoint, <Mono>s3.REGION.backblazeb2.com</Mono>: the region goes below.
          </>
        ),
        link: { href: "https://secure.backblaze.com/b2_buckets.htm", label: "Open Buckets" },
      },
      {
        title: "Add an application key",
        text: "In Application Keys, add one allowed access to this bucket only, with Read and Write.",
        link: { href: "https://secure.backblaze.com/app_keys.htm", label: "Open Application Keys" },
      },
      { title: "Copy its keys", text: "Backblaze shows the keyID and applicationKey once." },
    ],
  },
  {
    id: "hetzner",
    accent: "#D50C2D",
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
    guide: () => [
      {
        title: "Create the bucket",
        text: "In your project's Object Storage, create a bucket. Its location goes below.",
        link: { href: "https://console.hetzner.com/", label: "Open the Console" },
      },
      { title: "Generate S3 credentials", text: "Security › S3 credentials › Generate credentials." },
      { title: "Copy its keys", text: "The Console shows the secret key once." },
    ],
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

/** The least an IAM user needs on bucket: list it, read, write and delete its objects. */
function awsPolicy(bucket: string) {
  return JSON.stringify({
    Version: "2012-10-17",
    Statement: [
      { Effect: "Allow", Action: "s3:ListBucket", Resource: `arn:aws:s3:::${bucket}` },
      { Effect: "Allow", Action: ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"], Resource: `arn:aws:s3:::${bucket}/*` },
    ],
  });
}
