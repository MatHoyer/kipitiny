import type { Branding } from "@/features/settings/types";

/**
 * userId owns the token, which never does more than its owner may. A restricted one only gets its features and
 * grants, within its owner's.
 */
export type ApiToken = {
  id: string;
  name: string;
  userId?: string;
  restricted: boolean;
  features: Feature[];
  grants: Grants;
  createdAt: string;
  lastUsedAt?: string;
};

export type TokenInput = Pick<ApiToken, "name" | "restricted" | "features" | "grants">;

/** An OpenID Connect provider users sign in with; clientSecret reads back masked. */
export type SSOProvider = { id: string; name: string; issuer: string; clientId: string; clientSecret: string; createdAt: string };

export type SSOProviderInput = Pick<SSOProvider, "name" | "issuer" | "clientId" | "clientSecret">;

/** Where providers send the browser back after signing in: their redirect URI. */
export const ssoCallbackUrl = () => `${window.location.origin}/api/auth/sso/callback`;

export type AuditEntry = {
  id: string;
  actor: string;
  action: string;
  target: string;
  status: number;
  error?: string;
  createdAt: string;
};

export type Role = "admin" | "member";

/** What a member may do in a project they were granted. Any of them implies view. */
export const perms = [
  "view", "deploy", "settings", "env", "domains", "backups", "restore", "download",
  "data", "data-write", "files", "files-write", "terminal", "git", "delete",
] as const;

export type Perm = (typeof perms)[number];

/** Server-level resources a member may see, read-only and without secrets. */
export const features = [
  "storage", "registries", "git-providers", "secret-providers", "servers", "networks", "notifications", "domains", "create-projects",
] as const;

export type Feature = (typeof features)[number];

/** Project ID to a member's permissions there. */
export type Grants = Record<string, Perm[]>;

export type User = { id: string; username: string; role: Role; features: Feature[]; createdAt: string; preferences?: Preferences };

/** The user's own UI choices; missing values are the defaults. */
export type Preferences = { mapView?: "3d" };

export type UserView = User & { grants: Grants; totpEnabled: boolean };

/** A role, and for a member their features and permissions per project. */
export type UserAccess = { role: Role; features: Feature[]; grants: Grants };

/** A single-use link that creates an account with this access. */
export type Invite = UserAccess & {
  id: string;
  createdBy: string;
  /** Where the link was emailed; absent when copied by hand. */
  email?: string;
  createdAt: string;
  expiresAt: string;
};

/** An invite's access, and optionally where to email it: link is the invite page's URL, the token its fragment. */
export type InviteInput = UserAccess & { email?: string; link?: string };

/** sso: the providers the account can sign in with instead of a password. */
export type InviteInfo = { role: Role; expiresAt: string; projects: string[]; sso: SSOButton[]; ssoRequired: boolean };

/** A password sign-in that still needs a TOTP or recovery code. */
export type MfaChallenge = { mfaRequired: true; ticket: string };

/** A browser that receives push notifications. */
export type PushDevice = { id: string; name: string; endpoint: string; createdAt: string };

export type Passkey = { id: string; name: string; createdAt: string; lastUsedAt?: string };

/** A provider offered on the sign-in page. */
export type SSOButton = { id: string; name: string };

/** An identity at a single sign-on provider linked to the account; email is the provider's. */
export type SSOIdentity = { id: string; providerId: string; providerName: string; email: string; createdAt: string; lastUsedAt?: string };

export type Account = {
  username: string;
  totpEnabled: boolean;
  recoveryCodes: number;
  passkeys: Passkey[];
  /** False for an account that signs in with single sign-on only: a fresh sign-in at its provider confirms its changes, for 10 minutes. */
  hasPassword: boolean;
  /** This session was confirmed that way. */
  confirmed: boolean;
  sso: SSOIdentity[];
  /** The providers an identity can be linked at. */
  ssoProviders: SSOButton[];
  /** Single sign-on is required: passkeys don't sign in until it isn't. */
  ssoRequired: boolean;
  role: Role;
  features: Feature[];
  grants: Grants;
};

export type TotpSetup = { secret: string; uri: string };

export type Ceremony<T> = { ceremony: string; options: T };

/** ssoRequired: password and passkey sign-in are off, the providers are the only way in. */
export type AuthState = { setupRequired: boolean; user?: User; sso: SSOButton[]; ssoRequired: boolean; branding: Branding };
