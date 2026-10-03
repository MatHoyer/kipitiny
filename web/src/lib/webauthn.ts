// Bridges the server's WebAuthn JSON (binary fields as base64url) and the
// browser's navigator.credentials API (ArrayBuffers).

const fromB64 = (s: string) => {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(s.length / 4) * 4, "=");
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
};

const toB64 = (buf: ArrayBuffer | null | undefined) =>
  buf ? btoa(String.fromCharCode(...new Uint8Array(buf))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") : undefined;

type Descriptor = { id: string; type: "public-key"; transports?: AuthenticatorTransport[] };
type Json<T, K extends keyof T> = Omit<T, K> & { [P in K]: unknown };

export type CreationOptionsJSON = {
  publicKey: Json<PublicKeyCredentialCreationOptions, "challenge" | "user" | "excludeCredentials"> & {
    challenge: string;
    user: { id: string; name: string; displayName: string };
    excludeCredentials?: Descriptor[];
  };
};

export type RequestOptionsJSON = {
  publicKey: Json<PublicKeyCredentialRequestOptions, "challenge" | "allowCredentials"> & {
    challenge: string;
    allowCredentials?: Descriptor[];
  };
};

const descriptors = (ds?: Descriptor[]) => ds?.map((d) => ({ ...d, id: fromB64(d.id) }));

/** Passkeys need a secure context (HTTPS or localhost) and browser support. */
export const passkeysSupported = () => typeof window !== "undefined" && window.isSecureContext && !!window.PublicKeyCredential;

/** A cancelled or timed-out browser prompt, which isn't worth an error toast. */
export const isCancelled = (err: unknown) => err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "AbortError");

export async function createPasskey({ publicKey }: CreationOptionsJSON) {
  const cred = (await navigator.credentials.create({
    publicKey: {
      ...publicKey,
      challenge: fromB64(publicKey.challenge),
      user: { ...publicKey.user, id: fromB64(publicKey.user.id) },
      excludeCredentials: descriptors(publicKey.excludeCredentials),
    } as PublicKeyCredentialCreationOptions,
  })) as PublicKeyCredential | null;
  if (!cred) throw new DOMException("No passkey was created", "NotAllowedError");
  const res = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: toB64(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      clientDataJSON: toB64(res.clientDataJSON),
      attestationObject: toB64(res.attestationObject),
      transports: res.getTransports?.(),
    },
  };
}

export async function getPasskey({ publicKey }: RequestOptionsJSON) {
  const cred = (await navigator.credentials.get({
    publicKey: {
      ...publicKey,
      challenge: fromB64(publicKey.challenge),
      allowCredentials: descriptors(publicKey.allowCredentials),
    } as PublicKeyCredentialRequestOptions,
  })) as PublicKeyCredential | null;
  if (!cred) throw new DOMException("No passkey was chosen", "NotAllowedError");
  const res = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: toB64(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      clientDataJSON: toB64(res.clientDataJSON),
      authenticatorData: toB64(res.authenticatorData),
      signature: toB64(res.signature),
      userHandle: toB64(res.userHandle),
    },
  };
}
