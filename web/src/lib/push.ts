import { api } from "@/api";
import { fromB64 } from "@/lib/webauthn";

/** Push needs a secure context (HTTPS or localhost) and a browser with the Push API. */
export const pushSupported = () => window.isSecureContext && "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;

/** iPhone and iPad have push only in the app added to the home screen. */
export const pushNeedsInstall = () => /iPad|iPhone|iPod/.test(navigator.userAgent) && !("PushManager" in window);

/** This browser's push subscription, if it has one. */
export async function currentPushSubscription() {
  if (!pushSupported()) return null;
  const reg = await navigator.serviceWorker.getRegistration("/");
  return (await reg?.pushManager.getSubscription()) ?? null;
}

/** Asks for the permission, subscribes this browser with the manager's key and registers it. */
export async function enablePush(publicKey: string) {
  if ((await Notification.requestPermission()) !== "granted") {
    throw new Error("Notifications are blocked for this site: allow them in the browser's site settings.");
  }
  await navigator.serviceWorker.register("/sw.js");
  const reg = await navigator.serviceWorker.ready;
  const key = fromB64(publicKey);
  let sub = await reg.pushManager.getSubscription();
  const old = sub?.options.applicationServerKey;
  // made with another key (the manager's data was reset): unusable
  if (sub && (!old || !sameBytes(new Uint8Array(old), key))) {
    await sub.unsubscribe();
    sub = null;
  }
  sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
  return api.subscribePush(deviceName(), sub.toJSON());
}

const sameBytes = (a: Uint8Array, b: Uint8Array) => a.length === b.length && a.every((v, i) => v === b[i]);

/** "Chrome on Android", to tell devices apart. */
function deviceName() {
  const ua = navigator.userAgent;
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /Firefox\/|FxiOS/.test(ua)
      ? "Firefox"
      : /Chrome\/|CriOS/.test(ua)
        ? "Chrome"
        : /Safari\//.test(ua)
          ? "Safari"
          : "Browser";
  const os = /iPhone|iPad|iPod/.test(ua)
    ? "iOS"
    : /Android/.test(ua)
      ? "Android"
      : /Mac OS X/.test(ua)
        ? "macOS"
        : /Windows/.test(ua)
          ? "Windows"
          : /Linux|CrOS/.test(ua)
            ? "Linux"
            : "";
  return os ? `${browser} on ${os}` : browser;
}
