// kipitiny analytics: no cookies, no storage. Pageviews (SPA navigations
// too), kipitiny.track(name, props) or data-kipitiny-event clicks, and
// kipitiny.identify(id, props) naming the visitor for the rest of the page.
(() => {
  const s = document.currentScript, e = s.src.replace(/\/a\.js(\?.*)?$/, "") + "/e";
  // After a SPA navigation the referrer is the previous page, not the site that sent the visitor.
  let last, ref = document.referrer, id;
  const send = (n, p) => {
    const d = JSON.stringify({ u: location.href, r: ref, t: document.title,
      s: screen.width + "x" + screen.height, l: navigator.language, n, p, i: id });
    if (!(navigator.sendBeacon && navigator.sendBeacon(e, d))) fetch(e, { method: "POST", body: d, keepalive: true });
  };
  const view = () => {
    if (location.href === last) return;
    if (last) ref = last;
    last = location.href;
    send();
  };
  for (const m of ["pushState", "replaceState"]) {
    const o = history[m];
    history[m] = function () { const r = o.apply(this, arguments); view(); return r; };
  }
  addEventListener("popstate", view);
  addEventListener("click", (ev) => {
    const el = ev.target.closest && ev.target.closest("[data-kipitiny-event]");
    if (!el) return;
    const p = {};
    for (const [k, v] of Object.entries(el.dataset))
      if (k.startsWith("kipitinyEvent") && k !== "kipitinyEvent") p[k.slice(13).toLowerCase()] = v;
    send(el.dataset.kipitinyEvent, p);
  }, true);
  window.kipitiny = {
    track: (n, p) => send(n, p),
    identify: (i, p) => { id = i == null ? undefined : String(i); if (id) send("$identify", p); },
  };
  view();
})();
