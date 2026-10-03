# Notifications (Web Push)

The phone can get a lock-screen notification when a coding agent needs you
("claude is waiting for you") or has finished ("web is done · your turn").
Delivery uses standard Web Push: no app store app, no cloud service of ours,
and no terminal text leaves the computer.

## What a notification contains

| Kind | Title | Body |
| --- | --- | --- |
| `waiting` | `<session title> is waiting for you` | `<project> · tap to answer` |
| `done` | `<session title> is done · your turn` | `Worked for N min` |
| test | `Termivoa` | `Notifications are on.` |

The encrypted payload is `{title, body, sessionId, kind}` and nothing else.
It never carries terminal output, a question text, a preview line, a path or a
command. Titles and project names are cleaned (control and format characters
removed) and shortened to 60 characters. Tapping a notification opens
`/sessions/<id>` in the app (`/c/<host>/sessions/<id>` for another computer,
see "Several computers"); the service worker accepts only a lowercase UUID
and a same-origin path.

## Architecture

- **The broker sends.** It owns devices, revocation and session state, so it
  can decide who may receive a notification without asking the gateway. The
  gateway only relays the four authenticated browser requests below. Outbound
  HTTPS goes from the broker straight to the push service.
- **VAPID key.** On first start the broker creates one P-256 key pair and
  saves the 32-byte private key in `push_vapid_key` in its private SQLite
  database (file mode 0600, in the broker data directory). The key type
  redacts itself when printed or logged. The public key is served only to an
  authenticated phone.
- **Subscriptions.** `push_subscriptions` holds endpoint URL, `p256dh` and
  `auth` per paired device. At most 5 per device (the oldest is dropped) and
  64 in total. `pterm revoke`, logout and device pruning delete a device's
  subscriptions in the same SQLite transaction. Delivery also skips devices
  without a live credential and devices revoked in memory.
- **Crypto.** `internal/webpush` implements RFC 8291 (`aes128gcm`) and RFC 8292
  (VAPID, ES256 JWT, `Authorization: vapid t=…, k=…`) with the Go standard
  library only (`crypto/ecdh`, `crypto/hkdf`, `crypto/aes`, `crypto/cipher`,
  `crypto/ecdsa`). The RFC 8291 Appendix A example is a unit test.
- **Limits.** One notification per session and kind per 60 s, at most 12 per
  minute in total (tests included), one test per device per 5 s. `waiting`
  is sent with `Urgency: high` and a 5-minute TTL; `done` with `normal` and
  2 minutes.

## Several computers

Every paired computer sends its own push ([multi-computer.md](multi-computer.md)).
Each computer has its own VAPID key, and one browser subscription has one
key, so the phone holds one subscription per computer. A service-worker
registration must be on the page's origin, so they all live on home's origin
(`web/src/push.ts`, `web/src/sw.js`):

| Computer | Registration | Subscribed with | Sent to |
| --- | --- | --- | --- |
| Home | `/sw.js` at scope `/` | home's key | home, same-origin |
| Peer `<host>` | `/sw.js` at scope `/push/<host>/` | that peer's key (`GET /api/v1/push` on the peer) | `POST /api/v1/push/subscribe` on the peer, through the per-computer client (credentialed CORS, bound origin) |

No host change was needed: the peer admits the push routes like every other
`/api` route from its bound app origin, and the device comes from the
credential.

- **The app never loads under `/push/`.** The gateway answers 404 for every
  path under `/push/` (other unknown paths without an extension get
  `index.html`), and in a peer registration the worker handles no fetch,
  stores no bundle, deletes no cache and claims no client; it only shows
  notifications. If the app did load there, the page would get the peer's
  registration in place of home's. For the same reason the app asks for
  home's registration by its scope (`getRegistration('/')`), never by the
  page's address (`ready`).
- **The host comes from the scope, never from the payload.** The worker reads
  `<host>` from its own registration scope (`[a-z0-9.-]+`, exact
  `/push/<host>/`). A peer notification's title and body both start with the
  first DNS label (`vps · <title> is waiting for you`,
  `vps · <project> · tap to answer`), its tag carries the host, and a tap
  opens `/c/<host>/sessions/<id>`.
- **What a peer can still do (trust statement).** A peer's payload is
  untrusted. The peer writes all the text after the `vps · ` prefix: up to
  120 characters of title and 160 of body, in any words, also words that
  name home or another computer ("vps · mac · …"). The prefix is the only
  part the phone vouches for. A compromised peer can also flood the phone:
  the broker's rate limits are the peer's own, so it can ignore them, and
  each random session UUID makes a new tag, so the notifications do not
  replace each other. Only the push service's own limits apply. A tap still
  opens only `/c/<that host>/sessions/<uuid>` in the app. To stop it: remove
  the computer (or turn notifications off); both unsubscribe in the browser,
  so the push service refuses that peer from then on.
- **Allow-list.** On every load `removeStrayWorkers` keeps only `/sw.js` at
  scope `/` and `/sw.js` at the exact `/push/<host>/` of each peer in this
  phone's stored list. Any other registration (another script, another host,
  a near-miss scope) is unregistered, and that load redeems no grant
  (`workersSafe`). The stored list is in `localStorage`, which a compromised
  home can write. So a `/sw.js` registration that such a home left at
  `/push/<host>/` (with its own push key) for a host it also wrote into the
  stored list passes this first cleanup. It lives until home's list answers
  and replaces the stored one; the sync below then removes it. While home is
  offline it stays. It runs only the app's own `/sw.js`: it can show
  notifications with the limits above, nothing more. The recovery steps in
  [multi-computer.md](multi-computer.md) (delete the app's data) remove it.
- **Off wins over a slow peer.** A peer can hold its answers for up to 15 s.
  After the peer's key arrives, and again after the subscription is sent, the
  sync checks that push is still on and the peer is still in the stored list;
  if not, it makes no registration, or drops the one it made. So turning
  notifications off, removing the computer or logging out during a held call
  is not undone.
- **One switch.** Turning notifications on (Sessions card or Settings)
  subscribes home, then every peer at once. Only home can fail the switch; a
  peer that fails is named ("No notifications from vps.") and the others keep
  working. Turning off unsubscribes on every reachable computer and
  unregisters the peer registrations. **Send a test notification** asks every
  computer and names the peers that sent none.
- **Sync** (`syncPeerPush`, on every load, when the list changes, when a peer
  starts or stops asking to pair again, and when Settings opens). With push on,
  each listed peer is subscribed; a peer added later needs no prompt, because
  permission is already granted. Every other peer registration is
  unsubscribed and unregistered: a removed computer, log out, push off. A
  peer that answers 401 ("pair again") loses its registration until it is
  paired again. A subscription made under another key is replaced.
- **The peer's side.** Remove computer and Log out call the peer's logout,
  which deletes that device's subscriptions there. If the peer cannot be
  reached, the browser subscription is still removed, so the push service
  answers `410` and the peer deletes the endpoint.
- **Not verified:** several scoped registrations in an iOS Home Screen app
  (Gate 0 check 5; `docs/owner-verification.md`). If a scoped registration or
  subscribe fails there, home push is not affected and Settings names the
  computers without notifications.

## Browser API

| Method and path | Request | Success |
| --- | --- | --- |
| `GET /api/v1/push` | – | `200 HTTPPushConfigResponse` (VAPID public key) |
| `POST /api/v1/push/subscribe` | `HTTPPushSubscribeRequest` | `204` |
| `POST /api/v1/push/unsubscribe` | `HTTPPushUnsubscribeRequest` | `204` |
| `POST /api/v1/push/test` | `{}` | `200 HTTPPushTestResponse` (`sent` count) |

All need the session cookie; the POSTs follow the normal mutation rules
(exact Origin, `X-Pterm-Request: 1`, strict JSON). See
`protocol/v1/http-api.md`.

## SSRF guard

The endpoint URL comes from the browser, so the broker treats it as untrusted:

- `https` only, port 443 only, no user info, no fragment, printable ASCII,
  at most 2048 bytes.
- The host must be exactly `fcm.googleapis.com`,
  `updates.push.services.mozilla.com` or `web.push.apple.com`, or end in
  `.push.apple.com` or `.notify.windows.com` after at least one valid DNS
  label. Matching is by whole labels, never by substring; uppercase, IP
  literals, `%` escapes and trailing dots are refused.
- The HTTP client follows no redirects, uses no proxy, times out after 10 s,
  and refuses to connect to loopback, private, link-local, CGNAT
  (`100.64.0.0/10`, Tailscale) or unspecified addresses even if DNS points
  there.
- A `404` or `410` answer deletes the subscription. Errors never echo the
  endpoint (it is a capability URL).

## Phone setup

- **iPhone / iPad:** Web Push works only in the Home Screen app (iOS 16.4+).
  In a normal Safari tab, Settings shows "Add to Home Screen to get
  notifications" and the Sessions card is not shown.
- **Android Chrome, desktop Chrome, Edge, Firefox:** use the Sessions card
  ("Get a buzz when an agent needs you") or the Settings switch.
- Settings has **Send a test notification** to check the whole path.

## When notifications fire

The broker's agent-state detector (`docs/agent-state.md`) decides. Every
running session checks its state after each screen scan, on the session
actor; the notification hook never blocks it. A goroutine reads the session
title (read at send time, so a rename applies) and the project name from
SQLite, then calls `Notifier.Notify`, which sends in the background.

| Kind | Fires when |
| --- | --- |
| `waiting` | The state becomes `waiting` with a question id not yet notified in this busy period. A prompt that flickers away and back does not buzz twice. After the next `idle`, the same question may notify again. |
| `done` | The state becomes `idle` after a busy period of at least 20 s (`agentDoneAfter` in `internal/broker/agent.go`). The busy period starts at the first `working` after the last `idle` and includes any `waiting` in between; "Worked for N min" is its length. Shorter blips (a shell command, typing) stay silent. |

- The first `working` of a new session is its startup output (the shell
  prompt). It starts no busy period.
- Exited and lost sessions never notify: scans run only while the session is
  `running`. There is no "exited" notification.
- An answer sent from the phone does not stop the next notifications: the
  next question has a new id, and "done" still fires (you may have left).
- The rate limits above still apply: one per session and kind per 60 s.

**Viewers do not suppress notifications.** A phone that has the session open
also gets the banner. The broker cannot tell an open screen from a locked
phone: when iOS suspends a Home Screen app, its WebSocket is often not
closed, and the viewer stays attached until the broker notices. Suppressing on
"a viewer is attached" would drop the buzz in exactly the locked-phone case,
and a browser tab left open on another computer would silence the phone too.

## The notifier API

```go
// internal/webpush
func (n *Notifier) Notify(ctx context.Context, event webpush.Event) bool

type Event struct {
	SessionID    string        // canonical lowercase UUID
	SessionTitle string        // the session's own title, never terminal text
	ProjectName  string        // saved project name
	Kind         webpush.Kind  // webpush.KindWaiting or webpush.KindDone
	Duration     time.Duration // how long the agent worked (done)
}
```

`Notify` returns at once and delivers in the background (detached from
`ctx` cancellation, 60 s deadline). It returns `false` when the event is
invalid, coalesced, over the rate limit, or dropped because earlier
deliveries are still running. The host passes `Notifier.Notify` to the
session registry as `RegistryConfig.Notify`; the same notifier is
`host.BrokerReady.Push` / `ManagementServerConfig.Push`.
