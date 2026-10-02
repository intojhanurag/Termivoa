// Gate 0 service worker. Served at /sw.js, /a/sw.js and /b/sw.js; each
// registration gets its own push key (check 6). On a push it asks the peer
// what cookie it got, reports to home, and shows a notification (check 5).
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

// Same-origin pass-through only, so the page's requests to the peer stay page requests.
self.addEventListener('fetch', (event) => {
  if (new URL(event.request.url).origin === self.location.origin) event.respondWith(fetch(event.request));
});

self.addEventListener('push', (event) => event.waitUntil(onPush()));

async function onPush() {
  const scope = new URL(self.registration.scope).pathname;
  const pages = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
  const report = { scope, at: Date.now(), openPages: pages.length };
  try {
    const config = await (await fetch('/config', { cache: 'no-store' })).json();
    const response = await fetch(config.peer + '/echo', { credentials: 'include', cache: 'no-store' });
    const echo = await response.json();
    report.received = echo.received;
    report.origin = echo.origin;
  } catch (error) {
    report.error = String(error);
  }
  await fetch('/report', { method: 'POST', body: JSON.stringify(report) }).catch(() => {});
  const body = report.error
    ? 'Error: ' + report.error
    : 'Peer got the cookie: ' + (report.received ? 'yes' : 'NO') + '. Open pages: ' + report.openPages + '.';
  await self.registration.showNotification('Gate 0 push ' + scope, { body, tag: 'gate0' + scope });
}

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  event.waitUntil(self.clients.openWindow('/'));
});
