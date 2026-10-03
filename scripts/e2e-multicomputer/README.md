# Multi-computer end-to-end harness

Runs two isolated Termivoa dev stacks, **mac** and **vps**, that browsers reach as `https://mac.tailx.ts.net` and `https://vps.tailx.ts.net` on the default port, and checks the multi-computer design (`docs/multi-computer.md`, sections 3 and 6), the dev-server preview of home and of a peer (`docs/peer-preview.md`, section 11), and Found on your network (`docs/found-on-network.md`, section 10). Each computer's preview origin is `https://<name>.tailx.ts.net:8443`. It is a test tool only. It never touches the installed `pterm`, launchd, Tailscale or Tailscale Serve.

## Run

```sh
pnpm install --frozen-lockfile
node scripts/e2e-multicomputer/phase-a.mjs                 # builds web + pterm, runs the checks
node scripts/e2e-multicomputer/phase-a.mjs --no-web-build  # reuse internal/webassets/dist
node scripts/e2e-multicomputer/phase-b.mjs --no-web-build  # the phone flow through the screens (Chrome)
node scripts/e2e-multicomputer/phase-b.mjs --no-web-build --only PV  # pairing, the first add, then only the preview steps
node scripts/e2e-multicomputer/phase-b.mjs --no-web-build --only F   # pairing, the first add, then only Found on your network
go test ./scripts/e2e-multicomputer/                       # the front's rewrite rules and the dev site
PW=<playwright-core dir> WEBKIT=<webkit executable> node scripts/e2e-multicomputer/webkit.mjs --no-web-build
```

WebKit needs a Playwright whose WebKit build is installed, for example playwright-core 1.61.1 with `WEBKIT=~/Library/Caches/ms-playwright/webkit-2272/pw_run.sh`. The harness installs nothing.

Options: `--out <dir>` writes the results table, JSON and screenshots there (default `<root>/evidence`), and Phase B writes each failed step's whole error, with a CLI's output so far, as `fail-<ID>.txt` (links hidden); `--keep` leaves everything running until Ctrl+C. Exit status 0 means every check passed: Phase A has 32 checks, Phase B 78 (33 of them `PV-*`, the preview; 2 `SCAN-*`; 12 `F-*`, Found on your network), WebKit 8. Needs macOS, Go, Node 24, pnpm and Google Chrome.

`--no-web-build` reuses `internal/webassets/dist`; after a change in `web/`, run once without it (or run `pnpm build:web` and `sh scripts/embed-web.sh`: `pnpm build:web` alone only writes `web/dist`). Every run builds `pterm` again, with `-tags webdist,e2e`; Phase A also builds it without `e2e` (check A8).

## How it works

| Part | File | What it does |
| --- | --- | --- |
| Build | `lib.mjs` `Env.create` | `go build -tags webdist,e2e` of `pterm`. The `e2e` tag compiles two test-only files: `internal/broker/peerdial_e2e.go` (with `PTERM_E2E_FRONT` and `PTERM_E2E_SPKI` set, the broker's offer to home opens `CONNECT <home host>:443` through the front and accepts only the front's key for that name, in place of the pinned-IP dial) and `cmd/pterm/lifecycle_e2e.go` (with `PTERM_E2E_GATEWAY` set, `pterm setup` checks that gateway URL in place of `127.0.0.1:8787`). Production builds compile neither; A8 greps a production build for `PTERM_E2E`. |
| Stacks | `lib.mjs` `Stack` | `pterm up --dev` (inside the sandbox below) per computer with its own `HOME`, data dir, runtime dir and a free loopback port (never 8787, 8788, 8789 or 8443) and a preview listener on a second free port (`--preview-listen`), under `/tmp/mce2e.*`. `pterm config origin https://<name>.tailx.ts.net`. |
| HTTPS front | `main.go` | Started before the stacks, so each broker gets its address and SPKI hash (`PTERM_E2E_FRONT`, `PTERM_E2E_SPKI`). One loopback CONNECT proxy (Chrome `--proxy-server`, curl `--proxy`). Each `CONNECT <host>:443` gets an in-process TLS server with a throwaway self-signed certificate (Chrome trusts it with `--ignore-certificate-errors-spki-list`). Routed hosts go to their gateway; any other host gets a blank page, used as an attacker or near-miss origin (`evil.tailx.ts.net`, `xmac.tailx.ts.net`, …). `CONNECT <host>:8443` reaches that computer's preview listener, as `tailscale serve --https=8443` does; the log names such a host with its port. Logs every request as JSON lines with no secrets (`<root>/front.jsonl`): method, path, `Origin`, `Referer` without its query, `Sec-Fetch-Site`/`-Mode`/`-Dest`, whether a cookie came, status. With `-old-home <file>`, while the file names a host, that host answers `POST /api/v1/computers/offer` as Termivoa before Found does: `403 "origin not allowed"` (check F-old). |
| QR video | `main.go` `qr` | `<root>/bin/front qr -out <file>` writes the link on stdin as a QR code in a one-frame Y4M video. Phase B `SCAN-*` starts a second Chrome with it as the camera (`--use-fake-device-for-media-stream --use-file-for-fake-video-capture`), pairs it with mac, and drives **+ Add a computer → Scan QR** to the **Add this computer?** sheet: once with the browser's `BarcodeDetector` (when it reads QR codes here) and once with it removed, so the JavaScript decoder runs (the iPhone path). |
| Dev site | `main.go` `devserver` | `<root>/bin/front devserver -name <text> -listen 127.0.0.1:<port>`, typed into a session's terminal: a page with a marker text and a load counter, a script, a fetch endpoint, a WebSocket echo, one relative redirect. |
| Fake Tailscale | `tailscale` | `internal/tailnet.FindBinary` checks fixed system paths before `PATH`; every harness `pterm` runs in a `sandbox-exec` profile that hides the real Tailscale CLI there, so the fake, first on `PATH`, is used (no product hook). Answers `status --json` and `serve status --json` from `<root>/<name>/tailscale/*.json`; refuses everything else. The demo tailnet: mac (macOS) and vps (linux) owned by `ada@example.com`, evil by `mallory@example.com`; each node has one `TailscaleIPs` address in `100.64.0.0/10`. |
| Fake launchctl | `launchctl` | Only for check F-setup, first on that one `pterm setup`'s `PATH` (pterm runs `launchctl` by name; the sandbox denies `/bin/launchctl`): every service is registered, `bootout` and `bootstrap` do nothing, each call is logged to `<root>/launchctl.log`, anything else is refused. |
| Real terminal | `lib.mjs` `interactive(…, { pty: true })` | Runs a command under `script(1)`, so its stdin is a terminal: only then does `pterm pair --via` offer (piped stdin keeps the link wait). |
| Checks | `phase-a.mjs`, `phase-b.mjs`, `webkit.mjs` | Drive headless Chrome over CDP (390×844, touch), WebKit through Playwright, curl and the `pterm` CLI. |

**Own-origin rewrite.** A dev gateway accepts only loopback `Host` and `Origin` values. For a routed host H the front rewrites `Host` to the gateway address and an `Origin` of exactly `https://H` to the gateway's loopback origin. Every other `Origin`, including every near miss of H and the other computer's origin, reaches the gateway unchanged, so the cross-computer checks run as in production (`main_test.go`).

**Preview origin mapping.** A dev gateway names its preview origin `http://127.0.0.1:<port>` and its preview listener accepts only that Host (the `:8443` names are production only). The front maps this one name both ways and nothing else (`main_test.go`):

| Direction | What the front changes | What it leaves alone |
| --- | --- | --- |
| Browser → preview listener of H | `Host`; an `Origin` of exactly `https://H:8443`; a `Referer` of exactly `https://H/` (H's own app) | Every other `Origin` and `Referer`. vps sees the real `Referer: https://mac.tailx.ts.net/` of the mac app's frame, and `evil`'s. |
| Browser → gateway of H | An `Origin` of exactly `https://H:8443` becomes the loopback preview origin, so the gateway's "own preview origin" refusal runs | Every other `Origin` |
| Gateway of H → browser | The loopback preview origin in `Content-Security-Policy` and in the body of `GET /api/v1/preview` reads `https://H:8443` | A peer's `https://<peer>:8443` in `frame-src`/`connect-src` is the gateway's own text |
| Preview listener of H → browser | `frame-ancestors http://<H's loopback gateway>` on the select page reads `frame-ancestors https://H` | `frame-ancestors https://mac.tailx.ts.net` from vps is the listener's own text |

So every check of a **peer** preview (the bound `Referer`, the forwarded origin, the broker's trust check, the peer's entry in home's CSP, `frame-ancestors`) runs on unchanged product output. For **home's** own preview the names are mapped, so the rules run but the production strings do not.

**Preview checks (Phase B `PV-*`, WebKit W5–W7).**

| Check | What it proves |
| --- | --- |
| `PV-pending` | While vps is pending, home's policy has its API origin in `connect-src` and no `:8443` anywhere; confirmed, `:8443` is in `frame-src` and `connect-src`, never as `wss:`. |
| `PV-chip`, `PV-added` | **Preview ready** shows for a vps session. vps was added in this visit: the loaded policy blocks its preview origin (`securitypolicyviolation`), the app reloads once and shows the preview. |
| `PV-vps-bar`, `-page`, `-probe`, `-select`, `-root`, `-reload`, `-api` | The bar `vps · untrusted page`; marker, script, fetch and WebSocket in the frame; the probe; the select request's headers as vps saw them; same-origin requests after it; ↻ makes exactly one new select; the dev site cannot call either API. |
| `PV-link-reload`, `-cold`, `-port` | A reload of a preview that a tap opened keeps it. The same address opened cold in another tab shows the session's terminal: no frame, no request to `vps:8443`. A port the session does not list does the same, also with a forged tap mark. |
| `PV-refuse-*` | With the phone's valid cookies (curl): a select with a `Referer` of evil, none, an `Origin` header, a dev site, a path, no slash, or two `Referer` headers is 403; `GET /` is 200 only with `Sec-Fetch-Site: same-origin`. |
| `PV-evil` | A top-level page on `evil.tailx.ts.net`: framing the select address or `/`, an image, a `no-cors` fetch and a WebSocket are all 403, though the browser sends the cookie. |
| `PV-home`, `-home-api`, `-home-refuse` | The same for a mac session, with no **untrusted page**; mac's own dev site cannot call the APIs; mac's listener refuses the vps app as `Referer`. |
| `PV-off` | vps runs with no preview listener: "Can't open the preview on vps…", no frame. |
| `PV-revoke-socket`, `-app`, `-after` | `pterm revoke --via` on vps: a preview socket held open with curl is closed by vps within the 5 s recheck; the app removes the frame and shows only `vps · pair again`; the old cookies get 401. |
| `PV-direct` (in `B7-direct`) | A device paired directly with vps is refused when the mac app is the `Referer` ("This app may not use this computer."). |
| `PV-node` (in `B7-node`) | A held preview socket closes after mac's node ID changes on vps. |
| W5–W7 | WebKit: the vps preview in the app with the same select headers; ↻; evil framing the select address is refused. |

**Found on your network checks (Phase B `F-*`, WebKit W8).** They use a second phone, "Found phone", paired with mac, because Found hides a computer the phone already uses and reads.

| Check | What it proves |
| --- | --- |
| `F-offer` | `pterm pair --via mac` in a terminal on vps prints "Sent to mac (https://mac.tailx.ts.net)."; mac's `/found` lists vps with a grant and the label `vps` from mac's status; vps's broker posted through the front with no `Origin`, no `Sec-Fetch-*` and no cookie, and got `204`. |
| `F-offer-owner` | An offer for `evil.tailx.ts.net` (another owner) gets `204` and is not listed. |
| `F-offer-origin`, `-preflight` | An offer with `Origin`, or with `Sec-Fetch-Site`, is `403` with no CORS header; a preflight gets no CORS header; a page on mac (same origin) and a page on evil cannot post an offer (Chrome). |
| `F-add`, W8 | **Settings → Computers → Found on your network: vps — Add** → **Add this computer?** (address, owner, "Add it only if you just ran pterm setup or pterm pair --via on vps.") → **Continue** → the code; vps's prompt says "THIS computer (vps)" and quotes "type y in the terminal on vps"; `y` → "vps is added". |
| `F-gone` | After the add, the found phone no longer shows vps under Found (home still lists the offer until it expires; the app hides a computer it uses and reads). |
| `F-dead` | vps is removed from the found phone's list and a junk grant is offered for vps: **Add** ends with "vps stopped waiting. On vps run pterm pair --via mac, then tap Add again." and no code. |
| `F-flood`, `-link` | 50 offers at once: about 10 get `204`, the rest `429`; right after, both phones' API calls answer `200`, and the pasted link of a new `pterm pair --via mac` adds vps. |
| `F-old` | With the front's `-old-home` for mac, vps prints "Termivoa on mac cannot receive this yet. Update it, or use the QR code or the link below." and the link; Ctrl+C ends it. |
| `F-setup-list`, `F-setup` | `pterm setup` on vps in a terminal (HOME at the stack's install paths, fake `launchctl`, `PTERM_E2E_GATEWAY`): answer 2, the list shows "1) mac — https://mac.tailx.ts.net (macOS)", the add screen says "Sent to mac (https://mac.tailx.ts.net).", **Add** on the found phone, `y` in setup, "Setup is done". The transcript (grant and QR hidden) is `f-setup.txt`. |

**Sandbox.** Every `pterm` the harness starts (stacks, CLI, doctor) runs under `sandbox-exec` with a profile that denies reading or running the real Tailscale CLI, connecting to `127.0.0.1:8787` and running `/bin/launchctl`.

**`pterm doctor`.** Doctor reads only the installed paths, which follow `HOME`, and probes `127.0.0.1:8787`. Each stack therefore keeps its data and runtime dirs exactly where doctor looks under a second home (`<root>/<name>/dh`), and doctor runs with that `HOME`. The "gateway not healthy" (sandbox) and "startup not registered" lines are expected there.

**Phase B notes.** The `in-terminal` add path runs vps's `pterm pair --via mac` in a mac session with vps's environment (a stand-in for `ssh vps`). Screenshots hide add-computer links in the terminal. Shells use `PROMPT='%1~ %# '`, so no real user or host name shows. When a step fails, the harness may confirm vps on home through the API to keep going; the table says so.

## Limits

- Dev gateways use the `pterm_dev_session` cookie without `Secure`, not `__Host-pterm_session`. Host-only, HttpOnly and `SameSite=Strict` are the same.
- Phase A, and Phase B's own setup calls, send API calls from a mac page with CDP `Page.setBypassCSP`, so they test the computers' answers, not the app's CSP. Phase B's app page keeps the real CSP.
- The app polls only while its tab is visible, so Phase B keeps the app tab in front (`Page.bringToFront`).
- Headless Chrome reaches no push service here, so the app cannot subscribe. Step B3-push calls vps's push routes from the mac origin, makes the peer registration the app makes (`/sw.js` at scope `/push/vps.tailx.ts.net/`), and delivers a push with CDP `ServiceWorker.deliverPushMessage`. CDP has no notification click, so the step dispatches `notificationclick` inside the peer worker; the browser refuses `focus()` for that event (it is not a user action), but the worker's message and the page's routing run as after a real tap. A real push service and a real tap are in `docs/owner-verification.md`.
- Preview, development profile: the cookies are `pterm_dev_session` and `pterm_dev_preview` (no `Secure`, no `__Host-`); the listener's Host rule for `:8443` names, the `https://<host>:8443` text a production gateway writes for its **own** preview origin (CSP, `GET /api/v1/preview`), and the own-app `Referer` match on a real https origin do not run here (see "Preview origin mapping"). These, a real `tailscale serve --https=8443`, a tailnet ACL on 8443, an installed iOS app, video, HMR of a real dev server, and WebKit's history restore of the app page are in `docs/owner-verification.md`, "Preview for sessions on peers".
- The dev site answers relative redirects only: the gateway rewrites an absolute redirect to its loopback preview origin, which a browser behind the front cannot reach.
- The front stands in for Tailscale Serve; Gate 0 (real phones, real tailnet) is separate (`scripts/gate0/`).
- Found on your network: the offer dial goes through the front (`peerdial_e2e.go`), so the pinned Tailscale IP, the dial guard and a real Let's Encrypt certificate are proven only by unit tests (`internal/broker/offer_test.go`); the URL, headers, node check, limits and answers are the production code's. The offer bucket is one bucket behind the front, as behind Serve. F-old stands in for an old home with the front, not a `v0.1.0-beta.2` build. F-setup runs the real setup code against a dev stack with a fake `launchctl`, so it registers nothing. The real two-computer and real-phone checks are in `docs/owner-verification.md`, "Found on your network".
