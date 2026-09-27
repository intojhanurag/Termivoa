# v0 threat model and verification boundaries

Status: private development, updated 2026-09-27. This is a design and test map,
**not a security certification**. Restricted local IPC, terminal replication,
controller leases, process isolation, phone pairing, broker-side credential
checks and revocation, and the authenticated loopback gateway are implemented
with automated tests on macOS; the Linux host runs the same suite in
containers. Real phones, Tailscale HTTPS, native Windows 11, real Linux hosts
and signed installers are not yet verified; see
[owner-verification.md](owner-verification.md). The P4 implementation map is in
[p4-authentication-design.md](p4-authentication-design.md).

## Assets and attacker capabilities

The primary asset is the owner's ability to run commands with their normal user
permissions. Browser credentials and control leases authorize that access. Terminal
contents, project names/paths, environment variables, and local metadata may also
be sensitive. The host must remain logged in and awake; the broker is the live
process boundary, not a durable job-restoration service.

Consider an unpaired tailnet peer, an unrelated website opened in a paired browser,
malicious terminal output, a malformed/slow client, a stale disconnected tab, and
an attacker holding a revoked or expired browser token. A terminal program may
produce arbitrary output and queries; that output cannot be trusted as UI markup
or forwarded drawing instructions.

Same-user malware and a compromised host OS are outside this authentication
boundary. The companion is not a shell sandbox: a shell has the user's filesystem
permissions. Application pairing does not make a compromised same-user process
safe, and private networking does not replace application authentication.

## Trust boundaries

| Boundary | Required control | Current evidence or pending gate |
| --- | --- | --- |
| Phone network → gateway | Loopback listener behind explicitly configured private HTTPS; exact Host/Origin, strict JSON and request header, bounded traffic | Loopback listener, exact Host/Origin, authenticated routes and rate limits implemented with tests (`docs/evidence/p4/gateway-http.md`); real Tailscale HTTPS test pending owner |
| Browser → broker authority | Random hashed credentials, local two-step approval, expiry and broker rechecks | Implemented with replay/concurrency/revocation tests (`docs/evidence/p4/pairing-broker.md`); real-phone pairing pending owner |
| Local client → broker | Current-user socket/pipe permissions, peer check, live generation handshake | Restricted IPC tests; Windows 11 execution pending |
| Viewer → session controller | Explicit lease, generation and epoch; sequential input acceptance; no implicit takeover | Actor/stream tests; `pterm attach`/`pterm new` integrated (`docs/evidence/p3/desktop-attach-command.md`) |
| Child bytes → display | One canonical emulator, normalized cells, validated drawing allowlist, no child ANSI relay | Parser/frame/client fixtures; real application/device width evidence incomplete |
| Slow viewer → child progress | Bounded queues and history, independent emulator/response writer, disconnect slow viewers | Automated fixtures and a Mac soak (`docs/evidence/p7/soak.md`); 10-minute stress run, 24-hour soak and Windows soak pending |
| Gateway lifetime → child lifetime | Independent broker owns PTYs, process cleanup, SQLite | Mac process restart fixture; native Windows 11 pending |
| Dev site → app (dev-server preview) | Dev site served on a separate origin (`https://host:8443`, own loopback listener that never serves the app); app CSP frames only that origin; app rejects requests from the preview origin; every preview request re-authorized by the broker; only OS-verified ports of the session's own process tree; Termivoa cookies and identity headers never forwarded | Unit and loopback tests (`internal/gateway/preview_test.go`, `internal/broker/devpreview_test.go`, `internal/ptyhost/listen_test.go`); Chromium iframe check on the dev profile; iPhone over Tailscale Serve 8443 pending owner |
| Child output/OS → session folder (live folder) | `cwd` from the OS (lsof, /proc) or OSC 7 / OSC 9;9 is display text only: absolute, no `.`/`..`, no control characters, at most 512 bytes, non-printables shown as U+FFFD; the phone renders it as text, never HTML, a link or a URL; the Windows PowerShell hook is session-scoped (`-Command`), edits no profile file, and is never injected on macOS/Linux | Unit tests (`internal/broker/cwd_test.go`, `internal/protocol/cwd_test.go`, `web/src/screens/folder.test.ts`); real shell tests on macOS and Linux; Windows PowerShell pending owner |
| Broker → push services (outbound) | Browser-supplied endpoint limited to exact push hosts over https:443; no redirects, no proxy, public addresses only, 10 s timeout; payload is names and status only, encrypted (RFC 8291) and VAPID-signed; revoke/logout delete subscriptions | Unit tests incl. RFC 8291 vectors and SSRF cases (`internal/webpush`); dev-stack curl check; real iPhone/Android delivery pending owner |
| Phone → project folder (photo upload) | Controller-only, rechecked at commit; magic-byte type check; 5 MiB cap; server-chosen name, `O_EXCL`, 0600/current-user ACL; `os.Root` confinement; symlinked `.termivoa`/`uploads` refused; per-device limits | Unit and RPC tests (`internal/broker/upload_test.go`, gateway and protocol tests); dev-stack curl and browser check; real iPhone pending owner |
| Dependency/build → distributed executable | Exact pins, patches/notices, secret and vulnerability scans, signed artifacts | Private source scans/build checks exist; signing and release review pending |

## Pairing and credential acceptance criteria (P4)

The local CLI creates a 256-bit, two-minute grant. Its URL fragment is a temporary
pairing secret, not a reusable login token. The page must remove that fragment
before starting other navigation or requests, then redeem it in a same-origin
HTTPS POST body. No query parameter, log, localStorage value, or analytics event
may contain the grant. SQLite stores only its hash.

Atomic redemption consumes the grant exactly once and creates a pending device
with an independent random poll secret and matching phone/CLI verification code.
Possessing or guessing the displayed code is insufficient: approval must come
through the local CLI's restricted IPC. Polling before approval cannot obtain an
authenticated cookie. Expired, replayed, conflicting, and concurrently redeemed
grants require explicit negative tests.

Approved credentials are random 256-bit values whose SHA-256 hashes are stored.
Production uses `__Host-pterm_session`, Secure, HttpOnly, SameSite=Strict, Path=/,
and no Domain. Absolute expiry is 30 days; inactivity expiry is seven days.
An expired browser pairs again. The development profile uses separate paths,
credentials, and cookie name and is available only through explicit `--dev`.

The broker must decide authorization, including during delayed gateway cleanup.
Revocation invalidates credentials and leases before later input can be accepted,
drops that device's still-pending actor actions, and closes viewers. Bytes already
encoded into the single PTY writer or written to the child cannot be retracted.
Tests must distinguish these stages; closing a WebSocket alone is not revocation.

No browser administrative routes are allowed for approving devices, creating
pairing grants, registering project roots, or changing startup/network settings.
Localhost, a Tailscale header, device label, UUID, or verification code alone never
grants browser access. Gateway IPC must not convert an unpaired request into a
privileged desktop/CLI identity.

## Terminal and input acceptance criteria

Only the broker's emulator responds to child queries. The documented Windows
exception is broker-owned ConPTY responding to queries it consumes; tests still
require exactly one reply. Browser and desktop query responses are never relayed.

Clients send structured keys/text/paste/mouse, never unrestricted terminal bytes.
Committed text rejects control characters. A lease-scoped input sequence rejects
gaps and acknowledges duplicates without enqueueing again. Accepted means broker
receipt, not shell execution. No offline queue, reconnect replay, or automatic
retry after an uncertain acknowledgement is permitted.

Cells and labels cannot inject ESC, OSC clipboard, images, title changes, arbitrary
links, or HTML into clients. Links may be explicitly user-opened http/https only,
with opener isolation. History is bounded, volatile, normal-screen-only data;
selection freezes a consistent snapshot instead of splicing revisions together.
Browser history, service-worker caches, logs, and SQLite must not persist terminal
contents. P5 must verify these browser behaviors on real devices as well as tests.

## Required adversarial evidence

- Two simultaneous grant redeemers; replay after success and after expiry; wrong
  poll secret; approval of the wrong pending ID; revoked/expired token reuse.
- Forged, missing, and near-match Host/Origin; forwarded identity headers; non-JSON
  mutations; missing application header; WebSocket origin mismatch; development
  credentials presented to production and vice versa.
- Revocation while user actions are waiting in an actor queue, while a gateway
  notification is delayed, and while already-written child input is in flight.
- Stale lease/generation, duplicate and missing sequences, guessed lease, multiple
  tabs on one device, heartbeat expiry, and disconnected input/drafts.
- Split/malformed JSON and Unicode, terminal control injection, oversized paste,
  dimensions, frames, history pages and queues; snapshot interruption and resync.
- Slow/no viewers during flood, gateway kill/restart, broker restart with lost
  state, explicit termination, descendant cleanup, and bounded resource growth.
- Sanitized logs, browser storage/cache inspection, staged and full-history secret
  scans, dependency findings review, and installer/uninstall permission checks.

Each result needs its source/build, environment, exact command, expected/actual
outcome and evidence under `docs/evidence`. A skipped or unavailable test remains
pending. Real phones, native Windows 11, signed installers and the full soak cannot
be inferred from Mac unit tests or cross-compilation.

## Push notifications

Web Push adds the only outbound connection the broker makes on its own. See
`docs/notifications.md`.

- **SSRF.** The endpoint URL is attacker-influenced (any paired browser can
  send one). The broker accepts only `https` on port 443 to
  `fcm.googleapis.com`, `updates.push.services.mozilla.com`,
  `web.push.apple.com`, `*.push.apple.com` or `*.notify.windows.com`, matched
  by whole DNS labels. It follows no redirects, uses no proxy and refuses to
  dial loopback, private, link-local, CGNAT/Tailscale or unspecified
  addresses. A paired browser can therefore make the broker send at most a
  small encrypted POST to a real push service, rate limited to 12 per minute.
- **Payload privacy.** Only the session title, project name, a status and a
  duration are sent, end-to-end encrypted to the phone's key; the push
  service sees the endpoint, timing, size and the VAPID public key. Terminal
  output, prompts and previews are never included; plan.md's rule that
  terminal-triggered (OSC) notifications stay disabled is unchanged —
  notifications come only from the broker's own state, never from child
  output.
- **Keys.** The VAPID private key lives in the 0600 broker database and is
  redacted in formatting and logs. Its VAPID subject names the project, not
  the tailnet host or the user. Subscription `auth`/`p256dh` values are
  stored per device and deleted on revoke, logout and device pruning.
- **Revocation.** A revoked or logged-out phone gets nothing: rows are
  deleted in the revocation transaction, delivery also requires a live
  credential, and the in-memory revocation set is checked at send time.
  A notification already accepted by a push service before revocation may
  still arrive.

## Photo uploads

A photo upload is the only browser action that writes a file on the host
other than through the terminal. See `protocol/v1/http-api.md` (Image
uploads) and `protocol/v1/authentication-rpc.md`.

- **Who.** Only the device that holds the session's controller lease may
  upload; the broker checks it when the upload starts and again just before
  it writes. The controller can already type any shell command, so this adds
  no new power; a paired phone that only watches cannot write files. The
  request never names a path: the broker picks the session's project folder.
- **Where.** The file goes to `<project>/.termivoa/uploads/`. All steps go
  through Go's `os.Root`, so no path can leave the project folder. A
  `.termivoa` or `uploads` that is a symlink, junction or file is refused, and
  the opened directory must be the one that was checked. The name is
  `<yyyyMMdd-HHmmss>-<8 random hex>.<ext>`, chosen by the broker; the phone's
  file name is never sent. Files are created with `O_EXCL` (never
  overwritten) and mode 0600, or a current-user-only ACL on Windows.
  `.termivoa/.gitignore` with `*` is created when missing, so uploads are not
  committed; an existing `.gitignore` there is left unchanged.
- **What.** The type comes from magic bytes only (JPEG, PNG, WebP, GIF);
  SVG, HTML and anything else is refused. The broker never decodes or runs the
  image. At most 5 MiB decoded.
- **How much.** The gateway accepts at most two upload bodies at once (about
  7 MiB each); the broker one upload per device, four in total, and 12 per
  minute per device. Chunks travel on a dedicated IPC connection with every
  message under 256 KiB; a stalled upload is dropped after 30 seconds, and
  nothing reaches the disk before commit. Uploads are not deleted
  automatically; removing an attachment on the phone keeps the file.
- **Privacy.** The phone redraws each photo on a canvas before upload, which
  drops EXIF and GPS metadata and caps the long edge at 2048 px, because the
  agent may later send the image to an AI service. The broker trusts the
  bytes it receives and does not strip metadata itself; a client that skips
  the canvas step can upload an original file.

## Voice dictation

Voice uses the browser's Web Speech API. On iPhone, Safari may send audio to
Apple's speech service; the app sends no audio anywhere itself. Text is only
added to the draft; nothing is sent to the terminal until the user taps
**Send**. Listening stops when the panel or sheet closes or the page is
hidden.

## Dev-server preview

See `docs/preview.md` for the full design.

- **Origin isolation.** The dev site is arbitrary code from the owner's
  project and its dependencies. It runs on the preview origin, never on the
  app origin, so it cannot read the app's DOM, storage, API responses or
  terminal. The app's CSP `frame-src` names only the preview origin.
- **Shared cookie.** Cookies are host-scoped, so the dev site's requests to
  the app carry the owner's session cookie. The app answers 403 to every
  request whose `Origin` is the preview origin (reads and WebSockets too), and
  mutations and WebSockets still need the exact app origin. The dev site can
  still make cookie-bearing top-level navigations or `no-cors` GETs to the
  app; those read nothing and change nothing.
- **Port ownership.** Only ports that the OS reports listening on loopback or
  wildcard addresses and owned by the session's process tree can be proxied,
  checked again on every request (scan at most 2 s old). Databases, admin
  panels and other local services outside the session are never offered or
  proxied, even if their port number is sent by hand.
- **Revocation.** A revoked or expired device gets 401 on its next preview
  request; open preview WebSockets and streams close within 5 s.
- **Proxy hygiene.** No Termivoa cookie, `X-Pterm-*`, `Tailscale-*` or
  forwarded header reaches the dev server; the dev server cannot set a
  Termivoa cookie; redirects are never followed by the gateway.
- **Known limits.** Previews of different sessions share one origin; a large
  cookie set by a dev site can break app requests until cleared; a same-user
  process binding a more specific address on the same port is out of scope.
- **Network.** The owner runs `tailscale serve --bg --https=8443
  http://127.0.0.1:8789` only after a separate `y` in `pterm setup` (or by
  hand). Never Funnel.

## Operational and release limits

Do not install startup tasks, configure Tailscale Serve/HTTPS, use a coding-agent
account, sign, publish, or change visibility without the corresponding authority.
Never enable Funnel. Existing Serve configuration must be inspected and conflicts
reported, not overwritten. Running processes remain unprivileged user processes.

The private-to-public review must include source and reachable history, PR/issue
discussions, logs, screenshots and build artifacts. A discovered credential is
revoked/rotated before any cleanup proposal; deletion alone is not remediation.
Only the owner manually makes the repository public, and that action does not itself
authorize publishing installers or packages.
