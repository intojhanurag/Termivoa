/* Phone Terminal service worker. Built into /sw.js by vite.config.ts.
 * Caches only immutable, content-hashed /assets/* files. It never caches API
 * responses, WebSocket traffic, terminal frames, history, or secrets. A new
 * version waits until the user explicitly chooses to reload. */
const CACHE = 'pterm-assets-__BUILD__';

const OFFLINE_PAGE = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>Connection unavailable</title>
<style>body{margin:0;padding:2rem 1.25rem;background:#0B0E14;color:#D5DCE5;font-family:system-ui,sans-serif}</style>
</head><body><h1>Connection unavailable</h1>
<p>This phone cannot reach your computer right now. Nothing can run until the connection returns.</p>
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
