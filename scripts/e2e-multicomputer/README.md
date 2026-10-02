# Multi-computer end-to-end harness

Runs two isolated Termivoa dev stacks, **mac** and **vps**, that browsers reach as `https://mac.tailx.ts.net` and `https://vps.tailx.ts.net` on the default port, and checks the multi-computer design (`docs/multi-computer.md`, sections 3 and 6). It is a test tool only. It never touches the installed `pterm`, launchd, Tailscale or Tailscale Serve.

## Run

```sh
pnpm install --frozen-lockfile
node scripts/e2e-multicomputer/phase-a.mjs                 # builds web + pterm, runs the checks
node scripts/e2e-multicomputer/phase-a.mjs --no-web-build  # reuse internal/webassets/dist
```

Options: `--out <dir>` writes the results table, JSON and screenshots there (default `<root>/evidence`); `--keep` leaves everything running until Ctrl+C. Exit status 0 means every check passed. Needs macOS, Go, Node 24, pnpm and Google Chrome.

## How it works

| Part | File | What it does |
| --- | --- | --- |
| Stacks | `lib.mjs` `Stack` | `pterm up --dev` per computer with its own `HOME`, data dir, runtime dir and a free loopback port (never 8787, 8788, 8789 or 8443), under `/tmp/mce2e.*`. `pterm config origin https://<name>.tailx.ts.net`. |
| HTTPS front | `main.go` | One loopback CONNECT proxy (Chrome `--proxy-server`, curl `--proxy`). Each `CONNECT <host>:443` gets an in-process TLS server with a throwaway self-signed certificate (Chrome trusts it with `--ignore-certificate-errors-spki-list`). Routed hosts go to their gateway; any other host gets a blank page, used as an attacker or near-miss origin (`evil.tailx.ts.net`, `xmac.tailx.ts.net`, …). Logs every request as JSON lines with no secrets (`<root>/front.jsonl`). |
| Fake Tailscale | `tailscale` | First on each stack's `PATH`, so `internal/tailnet.FindBinary` finds it (no product hook needed). Answers `status --json` and `serve status --json` from `<root>/<name>/tailscale/*.json`; refuses everything else. The demo tailnet: mac and vps owned by `ada@example.com`, evil by `mallory@example.com`. |
| Checks | `phase-a.mjs` | Drive headless Chrome over CDP (390×844, touch), curl and the `pterm` CLI. |

**Own-origin rewrite.** A dev gateway accepts only loopback `Host` and `Origin` values. For a routed host H the front rewrites `Host` to the gateway address and an `Origin` of exactly `https://H` to the gateway's loopback origin. Every other `Origin`, including every near miss of H and the other computer's origin, reaches the gateway unchanged, so the cross-computer checks run as in production (`main_test.go`).

**`pterm doctor`.** Doctor reads only the installed paths, which follow `HOME`, and probes `127.0.0.1:8787`. Each stack therefore keeps its data and runtime dirs exactly where doctor looks under a second home (`<root>/<name>/dh`), and doctor runs with that `HOME` inside `sandbox-exec`, which blocks `127.0.0.1:8787` and `/bin/launchctl`. The "gateway not healthy" and "startup not registered" lines are expected there.

## Limits

- Dev gateways use the `pterm_dev_session` cookie without `Secure`, not `__Host-pterm_session`. Host-only, HttpOnly and `SameSite=Strict` are the same.
- Phase A sends API calls from the mac page with CDP `Page.setBypassCSP`, so the checks test the computers' answers, not the app's CSP.
- The front stands in for Tailscale Serve; Gate 0 (real phones, real tailnet) is separate (`scripts/gate0/`).
