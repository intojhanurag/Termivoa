# Multi-computer end-to-end harness

Runs two isolated Termivoa dev stacks, **mac** and **vps**, that browsers reach as `https://mac.tailx.ts.net` and `https://vps.tailx.ts.net` on the default port, and checks the multi-computer design (`docs/multi-computer.md`, sections 3 and 6). It is a test tool only. It never touches the installed `pterm`, launchd, Tailscale or Tailscale Serve.

## Run

```sh
pnpm install --frozen-lockfile
node scripts/e2e-multicomputer/phase-a.mjs                 # builds web + pterm, runs the checks
node scripts/e2e-multicomputer/phase-a.mjs --no-web-build  # reuse internal/webassets/dist
node scripts/e2e-multicomputer/phase-b.mjs --no-web-build  # the phone flow through the screens (Chrome)
PW=<playwright-core dir> WEBKIT=<webkit executable> node scripts/e2e-multicomputer/webkit.mjs --no-web-build
```

WebKit needs a Playwright whose WebKit build is installed, for example playwright-core 1.61.1 with `WEBKIT=~/Library/Caches/ms-playwright/webkit-2272/pw_run.sh`. The harness installs nothing.

Options: `--out <dir>` writes the results table, JSON and screenshots there (default `<root>/evidence`); `--keep` leaves everything running until Ctrl+C. Exit status 0 means every check passed. Needs macOS, Go, Node 24, pnpm and Google Chrome.

## How it works

| Part | File | What it does |
| --- | --- | --- |
| Stacks | `lib.mjs` `Stack` | `pterm up --dev` (inside the sandbox below) per computer with its own `HOME`, data dir, runtime dir and a free loopback port (never 8787, 8788, 8789 or 8443), under `/tmp/mce2e.*`. `pterm config origin https://<name>.tailx.ts.net`. |
| HTTPS front | `main.go` | One loopback CONNECT proxy (Chrome `--proxy-server`, curl `--proxy`). Each `CONNECT <host>:443` gets an in-process TLS server with a throwaway self-signed certificate (Chrome trusts it with `--ignore-certificate-errors-spki-list`). Routed hosts go to their gateway; any other host gets a blank page, used as an attacker or near-miss origin (`evil.tailx.ts.net`, `xmac.tailx.ts.net`, …). Logs every request as JSON lines with no secrets (`<root>/front.jsonl`). |
| Fake Tailscale | `tailscale` | `internal/tailnet.FindBinary` checks fixed system paths before `PATH`; every harness `pterm` runs in a `sandbox-exec` profile that hides the real Tailscale CLI there, so the fake, first on `PATH`, is used (no product hook). Answers `status --json` and `serve status --json` from `<root>/<name>/tailscale/*.json`; refuses everything else. The demo tailnet: mac and vps owned by `ada@example.com`, evil by `mallory@example.com`. |
| Checks | `phase-a.mjs`, `phase-b.mjs`, `webkit.mjs` | Drive headless Chrome over CDP (390×844, touch), WebKit through Playwright, curl and the `pterm` CLI. |

**Own-origin rewrite.** A dev gateway accepts only loopback `Host` and `Origin` values. For a routed host H the front rewrites `Host` to the gateway address and an `Origin` of exactly `https://H` to the gateway's loopback origin. Every other `Origin`, including every near miss of H and the other computer's origin, reaches the gateway unchanged, so the cross-computer checks run as in production (`main_test.go`).

**Sandbox.** Every `pterm` the harness starts (stacks, CLI, doctor) runs under `sandbox-exec` with a profile that denies reading or running the real Tailscale CLI, connecting to `127.0.0.1:8787` and running `/bin/launchctl`.

**`pterm doctor`.** Doctor reads only the installed paths, which follow `HOME`, and probes `127.0.0.1:8787`. Each stack therefore keeps its data and runtime dirs exactly where doctor looks under a second home (`<root>/<name>/dh`), and doctor runs with that `HOME`. The "gateway not healthy" (sandbox) and "startup not registered" lines are expected there.

**Phase B notes.** The `in-terminal` add path runs vps's `pterm pair --via mac` in a mac session with vps's environment (a stand-in for `ssh vps`). Screenshots hide add-computer links in the terminal. Shells use `PROMPT='%1~ %# '`, so no real user or host name shows. When a step fails, the harness may confirm vps on home through the API to keep going; the table says so.

## Limits

- Dev gateways use the `pterm_dev_session` cookie without `Secure`, not `__Host-pterm_session`. Host-only, HttpOnly and `SameSite=Strict` are the same.
- Phase A, and Phase B's own setup calls, send API calls from a mac page with CDP `Page.setBypassCSP`, so they test the computers' answers, not the app's CSP. Phase B's app page keeps the real CSP.
- The app polls only while its tab is visible, so Phase B keeps the app tab in front (`Page.bringToFront`).
- Headless Chrome reaches no push service here, so the app cannot subscribe. Step B3-push calls vps's push routes from the mac origin, makes the peer registration the app makes (`/sw.js` at scope `/push/vps.tailx.ts.net/`), and delivers a push with CDP `ServiceWorker.deliverPushMessage`. A real push service and a real tap are in `docs/owner-verification.md`.
- The front stands in for Tailscale Serve; Gate 0 (real phones, real tailnet) is separate (`scripts/gate0/`).
