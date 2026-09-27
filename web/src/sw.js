/* Phone Terminal service worker. Built into /sw.js by vite.config.ts.
 * Caches only immutable, content-hashed /assets/* files. It never caches API
 * responses, WebSocket traffic, terminal frames, history, or secrets. A new
 * version waits until the user explicitly chooses to reload. */
const CACHE = 'pterm-assets-__BUILD__';

const OFFLINE_PAGE = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta http-equiv="refresh" content="10">
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
<div class="actions"><span class="pill">retrying every 10s</span><a href="/">Retry now</a></div>
</body></html>`;

self.addEventListener('install', () => {
  // Deliberately no skipWaiting(): a live session is never switched underneath the user.
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key))))
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
  if (request.method !== 'GET') {
    return;
  }
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) {
    return;
  }
  if (url.pathname.startsWith('/assets/')) {
    event.respondWith(caches.open(CACHE).then(async (cache) => {
      const cached = await cache.match(request);
      if (cached) {
        return cached;
      }
      const response = await fetch(request);
      if (response.ok) {
        await cache.put(request, response.clone());
      }
      return response;
    }));
    return;
  }
  if (request.mode === 'navigate') {
    event.respondWith(fetch(request).catch(() => new Response(OFFLINE_PAGE, {
      status: 503,
      headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' },
    })));
  }
  // Everything else, including /api/*, goes straight to the network.
});
