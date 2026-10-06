# v0 threat model and verification boundaries

Status: private development, updated 2026-10-03. This is a design and test map,
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
| Dev site → app (dev-server preview) | Dev site served on a separate origin (`https://host:8443`, own loopback listener that never serves the app); app CSP frames only preview origins (its own, and those of the phone's confirmed computers); app rejects requests from the preview origin; the preview listener admits the selection only with the `Referer` of its own or a bound app origin, and every other request only from the preview origin itself (rule L1); every preview request re-authorized by the broker; only OS-verified ports of the session's own process tree; Termivoa cookies and identity headers never forwarded | Unit and loopback tests (`internal/gateway/preview_test.go`, `internal/broker/devpreview_test.go`, `internal/ptyhost/listen_test.go`); Chromium iframe check on the dev profile; iPhone over Tailscale Serve 8443 pending owner |
| Child output/OS → session folder (live folder) | `cwd` from the OS (lsof, /proc) or OSC 7 / OSC 9;9 is display text only: absolute, no `.`/`..`, no control characters, at most 512 bytes, non-printables shown as U+FFFD; the phone renders it as text, never HTML, a link or a URL; the Windows PowerShell hook is session-scoped (`-Command`), edits no profile file, and is never injected on macOS/Linux | Unit tests (`internal/broker/cwd_test.go`, `internal/protocol/cwd_test.go`, `web/src/screens/folder.test.ts`); real shell tests on macOS and Linux; Windows PowerShell pending owner |
| Broker → push services (outbound) | Browser-supplied endpoint limited to exact push hosts over https:443; no redirects, no proxy, public addresses only, 10 s timeout; payload is names and status only, encrypted (RFC 8291) and VAPID-signed; revoke/logout delete subscriptions | Unit tests incl. RFC 8291 vectors and SSRF cases (`internal/webpush`); dev-stack curl check; real iPhone/Android delivery pending owner |
| Phone → project folder (photo upload) | Controller-only, rechecked at commit; magic-byte type check; 5 MiB cap; server-chosen name, `O_EXCL`, 0600/current-user ACL; `os.Root` confinement; symlinked `.termivoa`/`uploads` refused; per-device limits | Unit and RPC tests (`internal/broker/upload_test.go`, gateway and protocol tests); dev-stack curl and browser check; real iPhone pending owner |
| Home app origin → peer API (multi-computer, next beta) | Per-device origin binding in the broker: a cross-origin request passes only from the app origin bound to that request's own credential, grant or pending pairing; exact credentialed CORS for bound origins only; `Sec-Fetch-Mode: no-cors` and Origin-less requests without `Sec-Fetch-Site: same-origin` refused on `/api`; per-request WebSocket origin checks; strict origin parsing (https, default port, no trailing dot, never `null`) | Unit and loopback tests (`internal/gateway/multicomputer_test.go`, `internal/broker/computers_test.go`, `internal/protocol/computers_test.go`); local Chrome and WebKit cookie check (`scripts/gate0/README.md`); Gate 0 on a real iPhone and Android and the Playwright integration tests pending |
| Tailscale identity → peer trust (multi-computer) | At `pterm pair --via` the peer resolves home only among own-tailnet MagicDNS first labels (ambiguity refused), requires the same owner or an explicit `--allow-tagged`, and pins the node ID; every bound request reads an immutable snapshot (60 s cache, 10-minute grace, then refuse); owners are compared by stable Tailscale user ID, never by login name; a changed node ID revokes the bound devices; a node whose tagged state changed since pairing is refused (not revoked); a new snapshot closes open streams that lost trust at once; a status that is not `Running` (restart, logout) keeps the last good snapshot under the grace rules. Narrowing only, never authentication | `internal/tailnet/tailnet_test.go`, `internal/broker/computers_test.go` (near-miss, `null`, tagged, other owner, ambiguous name, node change, grace); real `tailscale status --json` owners and tags on a team tailnet pending |
| Terminal output → add-computer link (multi-computer) | Only a link the app itself would accept is made tappable; a tap opens a confirm sheet drawn outside the terminal; home checks the host in its own Tailscale status (same tailnet, same owner or tagged) before the sheet offers **Add**; the grant stays in the URL fragment and is removed at once | `web/src/pairing/addComputer.test.ts`, `web/src/terminal/pathLinks.test.ts`, `web/src/screens/AddComputer.test.tsx`; real iPhone pending |
| New computer → home (offer; Found on your network, ADR 0007) | `POST /api/v1/computers/offer` has no authentication, so admission comes first: any `Origin` or `Sec-Fetch-*` header is `403` at the top of `/api` admission, no CORS header and no preflight answer ever, the cookie is never read and no origin is forwarded (no browser page can reach it); `X-Pterm-Request: 1`, JSON, at most 1 KiB, strict decoding; its own token bucket (10, then 1 per s), which never takes redeem or poll tokens but is one bucket for all senders behind Tailscale Serve (`429 "too many offers"`); `204` for every well-formed offer, kept or dropped, and nothing echoed; home reads only its latest Tailscale snapshot (never waits on `tailscale`; at most one background refresh per 5 s, as for every per-request check) and keeps an offer only from an untagged peer of its own owner in its own tailnet, not itself; memory only, 60 s, at most 16 origins, newest per origin wins. The sender is the new computer's broker: TLS to home's pinned Tailscale IP on 443 with home's certificate name and system roots, no proxy, no redirect, 3 s, at most 2 KiB of the answer read and never shown. Accepted: anyone who reaches home's port 443 (also other users of a team tailnet or a share) can post junk or replacement offers, or empty the bucket, so **Add** fails for a while; a junk grant fails on redeem, and the QR code and link still work | `internal/gateway/multicomputer_test.go`, `internal/broker/offers_test.go`, `internal/broker/offer_test.go`; multi-computer harness checks `F-offer`, `F-dead`, `F-flood` (`scripts/e2e-multicomputer/README.md`); Serve forwarding and the background-process TLS dial on real computers pending owner |
| Home → paired devices (found list) | `GET /api/v1/computers/found` needs the cookie and home's own origin (a bound app origin is refused by the broker); it lists unexpired offers whose host still has the stored node ID and passes the owner rule, with labels from home's own status. It carries grants: every device paired with home can read them, and each such device is treated as the owner's (it already has a shell on home). A row still needs **Add**, the confirm sheet, the code and `y` on the new computer, whose prompt names THIS computer | `internal/broker/offers_test.go`, `internal/gateway/multicomputer_test.go`, `web/src/computers.test.ts`; harness check `F-add` (Chrome and WebKit); real iPhone and Android pending owner |
| Peer responses → home app (multi-computer) | Lenient precompiled schema validation of every HTTP body and WebSocket frame (no `unsafe-eval`), per-item list validation, `redirect: 'error'`, 1 MiB response cap, peer error text replaced by generic text. The preview frame is the one place where a peer chooses what shows (next row) | `web/src/protocol/*.test.ts`, `web/src/api/http.test.ts` |
| Peer preview origin → home app's frame (peer preview, ADR 0006) | The peer's listener admits the selection only with home's `Referer`, forwarded to the peer's broker and checked against the request's own credential (per-device origin binding); every other request only from the preview origin itself; every preview check of a bound device also needs Tailscale trust of the pinned node and tags; home's CSP holds `https://<peer>:8443` in `frame-src` and `connect-src` for confirmed computers only, never pending, never `wss:`; the origin is computed by the app, never read from a peer; the frame is sandboxed (no top navigation, popups, full screen or device features); the bar always shows the computer's label and **untrusted page**; the frame is removed when that computer refuses the phone, cannot be reached or must update. Not limited: what the framed page shows, and the cookies it sets (see "Dev-server preview", "A peer's frame") | `internal/gateway/preview_test.go`, `internal/gateway/multicomputer_test.go`, `internal/broker/computers_test.go`, `web/src/screens/Preview.test.tsx`, `web/src/screens/App.test.ts`; Chrome and Playwright WebKit in the multi-computer harness (`scripts/e2e-multicomputer/README.md`; results in `docs/progress.md`); real iPhone and Android pending owner |
| Phone → herdr on a computer (herdr provider, ADR 0008) | The provider routes pass the same `admitAPI` and broker `authorize` as `/api/v1/sessions` (cookie, exact own or bound `Origin`, `no-store`); `{provider}`, target and group IDs are checked by pattern and against the latest snapshot; the herdr binary and socket are checked before use (next section); no herdr title or screen text leaves the host; a per-device limiter; never `--takeover`; tests cannot reach the owner's herdr | `internal/herdr/*_test.go`, `internal/broker/providers_test.go`, `internal/gateway` route tests; harness checks `H-*` with `scripts/fakeherdr`; a real herdr and a real iPhone pending owner |
| Dependency/build → distributed executable | Exact pins, patches/notices, secret and vulnerability scans, signed artifacts | Private source scans/build checks exist; signing and release review pending |

## Pairing and credential acceptance criteria (P4)

The local CLI creates a 256-bit, two-minute grant. Its URL fragment is a temporary
pairing secret, not a reusable login token. Until Found on your network (ADR
0007), a grant's value went only to the restricted local CLI. Now a grant bound
to another computer's app (`pterm pair --via`) also goes from the new computer
to that home, over TLS to home's pinned Tailscale IP with home's certificate
name; home keeps it in memory for at most 60 s per send and gives it to every
device paired with home (each treated as the owner's). The grant still works
only on the computer that made it, only from the bound app origin, and only
after the owner compares the code and types `y` there. The page must remove that fragment
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
One exception (ADR 0010): the GitHub setup route may register one project root,
`~/<name>`, after the broker itself cloned that repository there, or after it
checked that the folder already is that repository. The phone sends only
`owner/name`, never a path.
Localhost, a Tailscale header, device label, UUID, or verification code alone never
grants browser access. Gateway IPC must not convert an unpaired request into a
privileged desktop/CLI identity.

Guessing a credential online is prevented by the 256-bit credential space, not
by a rate limit. The gateway does not count failed credential attempts (#149):
behind Tailscale Serve every client reaches it from `127.0.0.1`, forwarded
headers are not read, and one shared count let any tailnet machine lock out
every paired phone with about 20 bad requests. A request with a well-formed
credential is always judged by the broker. A tailnet machine can still send
many requests (each well-formed one costs one broker lookup), but it cannot
lock out a paired phone, also not after a gateway restart.

Known limit: the pairing routes keep their gateway limits (redeem: 5, then 1
per 12 s; poll: 10, then 1 per s), and so does the offer route of Found on
your network (10, then 1 per s, its own bucket). Behind Tailscale Serve each
is one bucket for all clients. A tailnet machine that floods these routes
blocks new pairings (or every offer, so **Add** in Found fails) while it runs. Paired phones are not affected, and guessing is not
helped (the broker has its own pairing limits). A per-client key needs the
address that Tailscale Serve forwards; that is a trust change and needs an ADR.

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

Web Push adds an outbound connection the broker makes on its own. See
`docs/notifications.md`. GitHub adds the second (see GitHub below): to
`github.com` and `api.github.com`, starting when a user taps **Connect**;
after that the broker also calls GitHub to refresh the token, for the
repository list, for a clone, and when git on that computer asks for the
login.

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
  output, prompts and previews are never included; the implementation plan's rule that
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

See `docs/preview.md` for the full design, and `docs/peer-preview.md` (ADR
0006) for previews of sessions on peers.

- **Origin isolation.** The dev site is arbitrary code from the owner's
  project and its dependencies. It runs on the preview origin, never on the
  app origin, so it cannot read the app's DOM, storage, API responses or
  terminal. The app's CSP `frame-src` names only preview origins: the
  computer's own, and `https://<host>:8443` of each confirmed computer in the
  paired phone's list (never a pending one).
- **Who may send a request to the preview listener.** All computers of one
  tailnet are same-site, so `SameSite=Strict` does not keep the cookie away
  from another machine's page. The selection needs no `Origin` header and
  exactly one `Referer` of this computer's own app origin or of a bound app
  origin; a bound one goes to the broker, which checks it against the
  request's own credential. Every other request must carry the preview origin
  as `Origin`, or no `Origin` and `Sec-Fetch-Site: same-origin`. Everything
  else is 403 before the cookie is read and before the dev server is reached.
  `Referer` is a second origin proof next to `Origin`; if a browser removes
  it, the preview fails closed. Markup injected into the app's page without
  script could start a selection (it needs an app bug and a real session ID).
- **A peer's frame (accepted risk, not removed).** When the user opens the
  preview of a session on a peer, that peer (or any script in its dev site)
  puts a page of its choice inside the app. It cannot read or change the app,
  navigate it, open popups or call any computer's API or other preview
  origin. It can show false content, for example a false "pair again" page;
  the only limit is the bar with the computer's label and **untrusted page**.
  It can sign the phone out of all computers by filling the cookie jar with
  `Domain=<tailnet>.ts.net` cookies (recovery: pair again with each
  computer), send such cookies to dev servers on other computers, and send
  blind `no-cors` requests from the phone into the tailnet. This withdraws
  the earlier guarantee that a peer cannot show phishing text in the home
  app. The owner accepted it on 2026-10-03.
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
  request; open preview WebSockets and streams close within 5 s. For a device
  bound to another computer's app, a changed node ID or tag set of that
  computer is seen with the next Tailscale snapshot (up to about 65 s); while
  the status cannot be read, the last good snapshot is used for up to 10
  minutes, then such devices are refused.
- **Proxy hygiene.** No Termivoa cookie, `X-Pterm-*`, `Tailscale-*` or
  forwarded header reaches the dev server; the dev server cannot set a
  Termivoa cookie; redirects are never followed by the gateway.
- **Known limits.** Previews of different sessions share one origin; a large
  cookie set by a dev site can break app requests until cleared; a same-user
  process binding a more specific address on the same port is out of scope.
  A dev-site service worker on a preview origin can answer navigations that
  another page started and can serve cached content after revocation
  (accepted by the owner). The app's probe of a preview origin shows that
  something answers there, not that Termivoa does. WebKit: Back inside the
  frame, past its first entry, moves the whole app back.
- **Network.** The owner runs `tailscale serve --bg --https=8443
  http://127.0.0.1:8789` only after a separate `y` in `pterm setup` (or by
  hand). Never Funnel.

## Multi-computer (next beta)

See ADR [0005](decisions/0005-multi-computer.md) and
[multi-computer.md](multi-computer.md), sections 3 and 7–9. One phone app,
served by a **home** computer, talks directly to **peer** computers in the
same tailnet, each with its own `__Host-` HttpOnly cookie. Not yet verified
on a real iPhone or Android (Gate 0).

New attackers and what limits them:

- **A compromised home computer (accepted risk, not removed).** Home serves
  the app and its service worker, so it can drive every peer bound to its
  origin, also with no app open (a push-woken worker, verified in Chrome),
  and it can show a fake "pair again" to capture a new pairing. It sees the
  grant and controls the code the phone shows, so it can redeem a grant
  from a non-browser client that fakes `Origin` and keep a raw peer cookie
  for up to 30 days. It can leave an extra service worker with its own push
  key that survives cleaning home. Limits: per-device binding (a phone that
  paired with a peer directly is never reachable from home's app),
  owner and node checks, the app's removal of every non-own service worker
  on each load, and the required recovery: `pterm revoke --via
  <home-origin>` (the full `https://` address) on every peer, then delete the Home Screen app (iPhone) or also clear the
  site's data in Chrome (Android), then pair again. Same-user malware on home
  therefore moves into scope for every peer. Home should be the least
  exposed always-on computer, and its HTTPS origin must serve only `pterm`
  (`pterm setup` refuses, and `pterm doctor` reports, other Serve handlers
  on that origin).
- **Another node in the same tailnet.** Same tailnet is same site, so
  `SameSite=Strict` gives no isolation between computers; only exact Origin
  binding, owner checks (other users in team tailnets, Funnel nodes) and the
  pinned node ID (reused MagicDNS names) do. A `no-cors` GET from any
  same-site page reaches a peer with its cookie; `/api` refuses it.
- **A compromised peer.** It controls only itself, but can break the app for
  all computers by setting large `Domain=<tailnet>.ts.net` cookies (recovery:
  delete the app). Its responses are untrusted input (see the table above).
  When the user opens its preview, it also controls the page in the frame
  (see "Dev-server preview", "A peer's frame").
- **A program printing a fake add-computer link.** See the table above; it
  can never add a computer in one tap or outside the user's tailnet and
  owner.
- **Offers to home (Found on your network).** See the two "offer" and "found
  list" rows above. Another node that reaches home can make **Add** fail for
  a while, never add a computer. A hostile computer of the same owner can
  offer itself and show in **Found**; the user must still tap **Add** and
  press `y` on it. If the user names a hostile computer as home, it could
  relay the code; the approval prompt names THIS computer and quotes the
  phone's sentence "type y in the terminal on vps", and the user must read
  it (`docs/found-on-network.md`, R1).
- **A lost phone.** It must be revoked on each computer; `pterm revoke` on
  home lists the phone's other computers. **Log out** in the app signs out
  of every reachable computer and names the others (`pterm revoke` there).

Not protected: computers in different tailnets (unsupported), WSL as a peer
(Tailscale usually runs on the Windows host), and any check that depends on
Tailscale's owner and tag data being correct. Known limit: the cached offline
shell keeps the CSP from when it was cached, so while home is offline it may
still allow `connect-src` and `frame-src` to a computer removed since; that
computer's own origin binding still applies.

Browser requirement: `/api` admits an Origin-less request only with
`Sec-Fetch-Site: same-origin`, for every user (one computer too). Safari
before iOS 16.4 sends neither header on GET, so it is refused; iOS 16.4 is the
minimum. The preview listener has the same rule for every request other
than the selection.

## herdr on your phone

See [herdr-provider.md](herdr-provider.md), section 11 (S1 to S16), and ADR
[0008](decisions/0008-herdr-provider.md). A paired phone can already run any
command in a Termivoa session, including `herdr agent send-keys`, so this
feature gives it no new authority on the computer. What is new:

- **Routes.** `GET` and `PATCH /api/v1/providers/{provider}`, `POST .../open`
  and `POST .../create` use the same `admitAPI` and `authorize` as the
  session routes. Mutations need `X-Pterm-Request: 1`, JSON and strict
  decoding. Errors use fixed Termivoa text, never herdr's or git's.
- **The herdr binary and socket (S3).** The binary comes from fixed install
  paths, then `PATH`, with the owner and mode checks of the Tailscale binary
  lookup (`tailnet.CheckBinary`). Before every dial the socket must be a
  socket owned by this user with no group or other access, in a folder owned
  by this user that group and others cannot write, and the folder's parent
  chain up to `/` passes `tailnet.CheckFolders`. After connect, the peer
  credential (`LOCAL_PEERCRED` on macOS, `SO_PEERCRED` on Linux) must be this
  user. A reply is at most 4 MiB and a call has a 1 s deadline. Same-user
  malware stays out of scope.
- **What leaves the host (S4, S5, S9).** Group and item names, the agent kind,
  the state word, tab labels and a duration. Never a terminal title (a
  program can set it, and it often holds the task text a person typed), screen
  text, a path or a prompt. Every herdr string has control and format
  characters removed and is cut. Push text has a fixed format
  (`docs/notifications.md`). A peer's herdr data is untrusted like any peer
  response.
- **Limits (S6, S7, S10).** The list is read from a cache, never from herdr on
  request. At most 4 provider sessions, ended 10 s after the last viewer. A
  limiter keyed by device ID (not by address, since all clients reach the
  gateway from loopback): `open` 20, `create` 3, `PATCH` 10 per minute, then
  `limit_exceeded`. One create at a time per computer.
- **Never `--takeover` (S8).** A terminal that another herdr client holds
  ends at once with herdr's reason; the owner's own attach is not kicked.
- **No prefix commands (S13).** The broker doubles `ctrl+b` in provider
  session input. **Accepted risk:** herdr's prefix key can be changed in its
  config. Termivoa does not read it and doubles only `ctrl+b`; with a custom
  prefix a phone can send herdr's prefix commands to the terminal it opened
  (at worst it detaches its own attach client). `pterm doctor` warns when
  `config.toml` sets `[keys] prefix` to anything but `ctrl+b`.
- **A link opens a terminal (S14).** Opening can resize the pane on the
  computer, so `/p/…` opens at once only after an in-app tap or a notification
  tap that the service worker confirmed (one-time record, 30 s). Any other
  arrival shows an **Open** button. Opening never takes control.
- **Tests never reach the owner's herdr (S16).** No provider exists unless
  `cmd/pterm` builds one; `herdr.New` has no default path or home. In a test
  binary `herdr.New` panics when any path in its config is outside a temp
  folder (`internal/herdr/guard.go`, proven by `TestGuardRefusesRealHerdr`).
  The e2e harness sandbox denies the real herdr binaries, the Homebrew Cellar
  they link to and `~/.config/herdr` (file access, exec and socket connect),
  and gives each stack its own `HOME`; `pterm doctor` in the harness reports
  the fake's version (the sandbox is macOS only). The opt-in lab test
  (`PTERM_HERDR_LAB=1`) runs a copy of the installed herdr binary with a temp
  `HOME` (never inside the real home) and its own named session, and stops
  only that session.

Not verified on a real herdr and a real iPhone: see
[owner-verification.md](owner-verification.md), "herdr on your phone".

### Manage (ADR 0009)

See [herdr-manage.md](herdr-manage.md), section 5 (M1 to M10). The manage
route (`closeItem`, `removeItem`, `newTab`, `newWorkspace`, `startServer`)
has the admission, authorization and mutation rules of create and a device
limit of 30 per minute. It adds no authority: a paired phone could do each
action by typing in a session. The risks it adds are accidents and wrong
targets, so: the app asks first; the request carries the confirmed name and
the host checks it on a fresh snapshot; a removal never forces (uncommitted
work is refused; files that git ignores are deleted, and the confirmation
says so); a close ends one workspace and deletes no file; a new workspace
takes a project ID, never a path, and the folder and its owner are checked
right before the call. `startServer` has no parameter from the phone and runs
only a herdr binary that passed the checks of S3. The broker downloads no
program (it clones only a GitHub repository that the user chose; see GitHub
below): **Install herdr** types herdr's install command into a normal
session, and the user presses Enter. herdr's and git's text is neither sent
nor logged (the log has the action and herdr's error code).

## GitHub

See [github-integration.md](github-integration.md), sections 6 and 10, and
ADR [0010](decisions/0010-github-integration.md). What is new:

- **A third-party secret in the broker.** One token pair for each computer
  (the GitHub App's user access token, 8 hours, and its refresh token, 6
  months, single-use) in `<data>/github.json`, mode 0600, outside SQLite.
  It never goes to the gateway, the phone or a log; the in-memory type
  prints `[REDACTED]`. Any process of the same user can read the file, as it
  can the VAPID key in the database or a `gh` login file. A backup of a
  computer holds a live refresh token for up to 6 months.
- **The admin channel returns a secret.** `admin.github.credential` answers
  the access token (never the refresh token) to a process of the same user
  (the admin channel's peer check). Such a process can already pair a
  device there. It exists for `pterm github-credential`, git's helper in a
  folder that Termivoa cloned.
- **Scope.** Contents read and write and Metadata read on the repositories
  the user chose on GitHub. No Workflows permission: GitHub refuses a push
  that changes `.github/workflows`.
- **Routes.** `GET`/`POST /api/v1/github` and `GET /api/v1/github/repos` use
  the same `admitAPI` and `authorize` as the session routes, with a limiter
  keyed by device ID (read 60, list 10, connect 3, set up 3,
  cancel/disconnect/stop 10 per minute). One Connect and one set-up at a time
  for each computer. Errors use fixed Termivoa text, never GitHub's or
  git's; the log has the action and a fixed problem word, no token, code,
  git text or repository name.
- **A paired or stolen unlocked phone.** It could already type any command.
  New: it sees the names of the chosen repositories (also private ones),
  and can clone one and fill the disk up to the bounds (free space 3 times
  GitHub's size plus 1 GiB, checked before and during; 30 minutes). It
  cannot name a folder, delete or overwrite one, or read the token.
- **A hostile repository.** The clone runs with a fixed environment (no
  token, a separate `HOME`, no `GIT_*` from the broker) and config: global
  and system config off, every credential helper reset, https only,
  `http.followRedirects=false`, no proxy, no hooks, no templates, no
  submodules, no LFS download, `transfer.fsckObjects`. It goes into a temp
  folder and an exclusive rename (`RENAME_EXCL`/`RENAME_NOREPLACE`) puts it
  at `~/<name>`; nothing ever deletes the final name. A name that starts
  with `.` or `-` is refused. Nothing from the clone runs; opening an agent
  in it is the user's act.
- **git's login in a cloned folder.** The clone's own config has a reset
  line and then `!<pterm> github-credential` for that one repository's
  address, so the user's other helpers do not store the token (Keychain,
  `~/.git-credentials`), and the helper prints `password_expiry_utc`. A
  folder that was already there keeps the user's login. A folder that says
  its `origin` is that repository becomes a project by a tap; it must be a
  real directory owned by the user in the home folder, so this gives a
  paired phone nothing it cannot do by typing.
- **Code phishing.** A user who enters a code that an attacker started gives
  the attacker lasting access to the chosen repositories, with no sign in
  Termivoa (only GitHub's list of authorized apps shows it). The sheet says
  "Enter a code only when you started Connect yourself." True of every
  device-flow login, also `gh`. The client ID is public and can be copied.
- **A stolen refresh token** works until the user revokes Termivoa on
  github.com. Reconnecting does not cut off a thief. When a refresh fails
  with `bad_refresh_token` before the token's expiry, the row says "Another
  program used this login. Revoke Termivoa on GitHub, then connect again."
  with the link.
- **Disconnect** sends both tokens to `POST /credentials/revoke` (best
  effort, 10 s), then zeroes and deletes the file. When the revoke failed,
  the row links to GitHub's authorized apps page. `pterm uninstall
  --delete-data` deletes the file without a revoke.
- **Tests never reach github.com.** The hosts are fixed in a release build;
  only an `e2e` build (`internal/github/github_e2e.go`) points them at
  `scripts/fakegithub`.

Not verified with the real GitHub App and a real iPhone: the device page and
the install page in iPhone Safari and the Home Screen app, a second try with
the same refresh token after a lost answer, and that nothing reaches the
Keychain or `~/.git-credentials` with the real app (design, section 12).

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
