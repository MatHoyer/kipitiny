// Push notifications only. No fetch handler and nothing cached: the UI always
// comes from the manager, so it never outlives an upgrade.

self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

self.addEventListener("push", (e) => {
  let m = {};
  try {
    m = e.data?.json() ?? {};
  } catch {}
  e.waitUntil(
    self.registration.showNotification(m.title || "kipitiny", {
      body: m.body,
      icon: m.icon || "/icon-192.png",
      data: { url: m.url || "/" },
    }),
  );
});

self.addEventListener("notificationclick", (e) => {
  e.notification.close();
  const url = new URL(e.notification.data?.url || "/", self.location.origin);
  e.waitUntil(
    (async () => {
      const wins = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      const win = wins.find((w) => new URL(w.url).origin === url.origin);
      if (!win) return self.clients.openWindow(url.href);
      await win.focus();
      if (url.origin === self.location.origin) await win.navigate(url.href).catch(() => {});
    })(),
  );
});
