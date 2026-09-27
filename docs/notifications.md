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
| test | `Phone Terminal` | `Notifications are on.` |

The encrypted payload is `{title, body, sessionId, kind}` and nothing else.
It never carries terminal output, a question text, a preview line, a path or a
command. Titles and project names are cleaned (control and format characters
removed) and shortened to 60 characters. Tapping a notification opens
`/sessions/<id>` in the app; the service worker accepts only a lowercase UUID
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

## The hook for agent-state detection

Nothing sends `waiting` or `done` yet. The agent-state detector calls:

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
deliveries are still running. The broker's notifier is
`host.BrokerReady.Push` / `ManagementServerConfig.Push`.
