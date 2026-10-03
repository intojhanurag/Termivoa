/* Termivoa service worker. Built into /sw.js by vite.config.ts.
 * On install it stores this build's complete bundle (the HTML and every
 * file the build made), so the app opens with home offline. It never caches
 * API responses, WebSocket traffic, terminal frames, history, or secrets.
 * A new version waits until the user explicitly chooses to reload. */
const BUNDLE = __BUNDLE__;
const BUILD = '__BUILD__';
const CACHE = `pterm-bundle-${BUILD}`;
const SHELL = '/index.html';
/** How long a page load waits for the network before the cached shell. */
const NAVIGATION_TIMEOUT_MS = 2500;
/** A page load with this query flag waits for the network as long as it takes (the app reloads with it when its CSP is out of date). */
const FRESH_PAGE = 'pterm-fresh';
/** Caches this app made, this build's and older builds'. */
const OWN_CACHE = /^pterm-bundle-/;
const BUNDLE_FILES = new Set(BUNDLE.files);
/* Push from another computer (a peer) comes through its own registration of
 * this file at scope /push/<host>/ (web/src/push.ts). The host is read from
 * that scope, which the app sets from its confirmed list, and never from a
 * push payload. '' in the app's own registration (scope /). A peer
 * registration serves no page (the computer answers 404 under /push/), so it
 * keeps no bundle, deletes no cache and handles no fetch. */
const PEER_HOST = (/^\/push\/([a-z0-9.-]+)\/$/.exec(new URL(self.registration.scope).pathname) || [])[1] || '';

const OFFLINE_PAGE = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta http-equiv="refresh" content="5">
<meta name="theme-color" content="#0B0E14">
<title>Connection unavailable</title>
<style>
body{margin:0;min-height:100vh;display:flex;flex-direction:column;box-sizing:border-box;padding:max(28px,env(safe-area-inset-top)) 24px max(32px,env(safe-area-inset-bottom));background:#0B0E14;color:#D5DCE5;font-family:system-ui,-apple-system,sans-serif}
.label{display:flex;align-items:center;gap:8px;margin:0;font:500 13px ui-monospace,Menlo,monospace;letter-spacing:.08em;color:#8A94A6}
.label i{width:8px;height:8px;border-radius:4px;background:#F2B544}
.art{display:flex;justify-content:center;padding:72px 0 40px}
h1{margin:0;font-size:34px;line-height:38px;font-weight:800;letter-spacing:-.03em}
p.lead{margin:0;padding:10px 0 28px;color:#8A94A6;font-size:16px;line-height:23px}
ul{display:flex;flex-direction:column;gap:12px;margin:0;padding:16px;list-style:none;border:1px solid #252D3D;border-radius:12px;background:#080A0F;font:14px ui-monospace,Menlo,monospace}
b{display:inline-block;width:20px;margin-right:12px;color:#F2B544}
.actions{display:flex;gap:10px;margin-top:auto;padding-top:24px}
.pill{flex:1;display:flex;align-items:center;height:52px;padding:0 16px;box-sizing:border-box;border:1px solid #252D3D;border-radius:26px;color:#8A94A6;font:14px ui-monospace,Menlo,monospace}
a{display:flex;align-items:center;height:52px;padding:0 24px;border-radius:26px;background:#64D8E8;color:#061418;font-weight:700;text-decoration:none}
</style>
</head><body>
<p class="label"><i></i>TERMIVOA · OFFLINE</p>
<div class="art"><svg width="300" height="96" viewBox="0 0 300 96" fill="none"><rect x="18" y="14" width="42" height="70" rx="9" stroke="#D5DCE5" stroke-width="2.5"/><rect x="32" y="20" width="14" height="3" rx="1.5" fill="#D5DCE5"/><rect x="224" y="20" width="62" height="42" rx="5" stroke="#D5DCE5" stroke-width="2.5"/><path d="M212 70h86" stroke="#D5DCE5" stroke-width="2.5" stroke-linecap="round"/><path d="M76 49h46" stroke="#64D8E8" stroke-width="2.5" stroke-linecap="round" stroke-dasharray="2 8"/><path d="M178 49h32" stroke="#252D3D" stroke-width="2.5" stroke-linecap="round" stroke-dasharray="2 8"/><circle cx="150" cy="49" r="17" fill="rgba(242,181,68,.14)" stroke="#F2B544" stroke-width="2"/><path d="M144 43l12 12M156 43l-12 12" stroke="#F2B544" stroke-width="2.5" stroke-linecap="round"/></svg></div>
<h1>Can't reach<br>your computer</h1>
<p class="lead">Your sessions are safe. They keep running.</p>
<ul><li><b>?</b>tailscale on, both devices</li><li><b>?</b>computer awake, not asleep</li></ul>
<div class="actions"><span class="pill">retrying every 5s</span><a href="/">Retry now</a></div>
</body></html>`;

/** The build ID in an index.html's pterm-build meta, or undefined. */
function buildOf(html) {
  const match = /<meta name="pterm-build" content="([^"]*)"/.exec(html);
  return match ? match[1] : undefined;
}

/**
 * Whether a page response may replace the cached shell: only an OK HTML
 * page of this same build. A newer build's HTML points to chunks this cache
 * does not have, so it is served but never stored.
 */
function shouldCacheShell(status, contentType, html, build) {
  return status >= 200 && status < 300 && /^text\/html/i.test(contentType || '') && buildOf(html) === build;
}

/** Settles like promise, or rejects when ms pass first. */
function withTimeout(promise, ms) {
  let timer;
  const timeout = new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('timeout')), ms); });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

/** Stores every bundle file, or nothing: a half-filled cache is deleted and install fails. */
async function precache() {
  const cache = await caches.open(CACHE);
  const results = await Promise.allSettled(BUNDLE.files.map(async (path) => {
    // 'reload' skips the HTTP cache, so no file of another build comes in.
    const response = await fetch(path, { cache: 'reload' });
    if (!response.ok) {
      throw new Error(`${path}: HTTP ${response.status}`);
    }
    if (path === SHELL && buildOf(await response.clone().text()) !== BUILD) {
      throw new Error('the server already moved to another build');
    }
    await cache.put(path, response);
  }));
  const failed = results.find((result) => result.status === 'rejected');
  if (failed) {
    await caches.delete(CACHE);
    throw failed.reason;
  }
}

function offlinePage() {
  return new Response(OFFLINE_PAGE, {
    status: 503,
    headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' },
  });
}

async function cachedShell() {
  try {
    return (await (await caches.open(CACHE)).match(SHELL)) || offlinePage();
  } catch {
    return offlinePage();
  }
}

async function refreshShell(response) {
  const type = response.headers.get('Content-Type');
  if (!response.ok || !/^text\/html/i.test(type || '')) {
    return;
  }
  if (shouldCacheShell(response.status, type, await response.clone().text(), BUILD)) {
    await (await caches.open(CACHE)).put(SHELL, response);
  }
}

/** Network first, so the CSP follows the computer list; the cached shell after a timeout (none when `fresh`), error or 5xx. */
function navigate(event, fresh) {
  const network = fetch(event.request);
  // Clone before the page reads the body. A late answer may still refresh the shell.
  const refresh = network.then((response) => refreshShell(response.clone())).catch(() => undefined);
  if (typeof event.waitUntil === 'function') {
    event.waitUntil(refresh);
  }
  return (fresh ? network : withTimeout(network, NAVIGATION_TIMEOUT_MS))
    .then((response) => (response.status >= 500 ? cachedShell() : response))
    .catch(() => cachedShell());
}

/** Bundle files come from this build's cache; a miss goes to the network and is not stored. */
async function fromBundle(request, pathname) {
  try {
    const cached = await (await caches.open(CACHE)).match(pathname);
    if (cached) {
      return cached;
    }
  } catch {
    // No cache: use the network.
  }
  return fetch(request);
}

self.addEventListener('install', (event) => {
  // Deliberately no skipWaiting(): a live session is never switched underneath the user.
  if (!PEER_HOST) event.waitUntil(precache());
});

self.addEventListener('activate', (event) => {
  // A peer registration of a newer build must not delete the cache the open app still uses.
  if (!PEER_HOST) event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((key) => key !== CACHE && OWN_CACHE.test(key)).map((key) => caches.delete(key))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'skip-waiting') {
    self.skipWaiting();
  }
});

self.addEventListener('fetch', (event) => {
  const request = event.request;
  // A peer registration serves nothing; the computer answers 404 under /push/.
  if (PEER_HOST || request.method !== 'GET') {
    return;
  }
  const url = new URL(request.url);
  // Other origins and /api/* go straight to the network.
  if (url.origin !== self.location.origin || url.pathname.startsWith('/api/')) {
    return;
  }
  if (request.mode === 'navigate') {
    event.respondWith(navigate(event, url.searchParams.has(FRESH_PAGE)));
    return;
  }
  if (BUNDLE_FILES.has(url.pathname)) {
    event.respondWith(fromBundle(request, url.pathname));
  }
});

/* Push. A payload carries only a session name, a status and a session id,
 * never terminal text. Every push shows a notification: the subscription is
 * userVisibleOnly, and iOS revokes push for a worker that shows nothing. */
const SESSION_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const KINDS = ['waiting', 'done', 'test'];
const text = (value, max) => (typeof value === 'string' ? value.slice(0, max) : '');

self.addEventListener('push', (event) => {
  let data = {};
  try {
    const parsed = event.data ? event.data.json() : {};
    if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
      data = parsed;
    }
  } catch {
    // Not JSON: fall back to the generic notification below.
  }
  const sessionId = typeof data.sessionId === 'string' && SESSION_ID.test(data.sessionId) ? data.sessionId : '';
  const kind = KINDS.includes(data.kind) ? data.kind : 'test';
  const body = text(data.body, 160);
  const title = text(data.title, 120) || 'Termivoa';
  // A peer's notification opens /c/<host>/sessions/<id>. Its title and body
  // both start with its computer (the first DNS label of the scope's host):
  // the peer writes the rest, so it could otherwise name another computer.
  const peer = PEER_HOST && `/c/${PEER_HOST}`;
  const label = PEER_HOST.split('.')[0];
  event.waitUntil(self.registration.showNotification(PEER_HOST ? `${label} · ${title}` : title, {
    body: PEER_HOST ? [label, body].filter(Boolean).join(' · ') : body,
    tag: `${peer}${sessionId || 'pterm'}:${kind}`,
    icon: '/icon-192.png',
    data: { url: sessionId ? `${peer}/sessions/${sessionId}` : '/' },
  }));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const data = event.notification.data;
  const path = data && typeof data.url === 'string' && data.url.startsWith('/') && !data.url.startsWith('//') ? data.url : '/';
  // '/\host' would parse as another origin, so check the resolved origin too.
  const resolved = new URL(path, self.location.origin);
  const url = resolved.origin === self.location.origin ? resolved.href : `${self.location.origin}/`;
  // The open app routes to this path itself (App.tsx checks it again).
  const open = { type: 'open', path: new URL(url).pathname };
  event.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(async (windows) => {
    // With several windows, the one the user sees: visible and focused, then visible, then any.
    const ours = windows.filter((item) => new URL(item.url).origin === self.location.origin);
    const client = ours.find((item) => item.visibilityState === 'visible' && item.focused)
      || ours.find((item) => item.visibilityState === 'visible') || ours[0];
    if (!client) {
      return self.clients.openWindow(url);
    }
    // A tap allows one window action, and focus() is it: never openWindow() after it.
    // A peer registration does not control the window (home's does), so
    // navigate() would reject there: the page gets the path as a message.
    if (PEER_HOST) {
      client.postMessage(open);
      return client.focus();
    }
    await client.focus();
    try {
      if (await client.navigate(url)) {
        return undefined;
      }
    } catch {
      // A window this worker does not control yet cannot be navigated.
    }
    client.postMessage(open);
    return undefined;
  }));
});
