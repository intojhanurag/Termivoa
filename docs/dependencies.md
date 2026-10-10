# Dependency record

This started as the P0 resolution record and lists the direct dependencies in
`go.mod` and `web/package.json` as of 2026-10-02. Exact transitive versions are
recorded in `go.sum` and `pnpm-lock.yaml`. Terminal behavior is established by
the fixtures under `docs/evidence/`, not by inclusion here. Local patches are
described in [dependency-patches.md](dependency-patches.md); release notices are
tracked in [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md).

## Runtime Go dependencies

| Module | Version | Purpose | License check |
| --- | --- | --- | --- |
| `github.com/coder/websocket` | `v1.8.15` | WebSocket transport | pending notice audit |
| `github.com/charmbracelet/x/xpty` | `v0.1.4`, local patch | PTY/ConPTY integration | pending notice audit |
| `github.com/charmbracelet/x/vt` | `v0.0.0-20260920004010-53e2afe73ae5`, local patch | Authoritative emulator | pending notice audit |
| `github.com/charmbracelet/ultraviolet` | `v0.0.0-20260303162955-0b88c25f3fff`, local bounded-input patch | Desktop input decoding; see `dependency-patches.md` | upstream MIT text retained; distribution notice audit pending |
| `github.com/charmbracelet/x/ansi` | `v0.11.7`, local patch | ANSI utilities | pending notice audit |
| `github.com/charmbracelet/x/term` | `v0.2.2` | Local terminal state and size for CLI commands | MIT per locked module `LICENSE`; distribution notice audit pending |
| `github.com/charmbracelet/x/windows` | `v0.2.2` | Windows console input records for the `pterm attach` detach handoff | pending notice audit |
| `github.com/creack/pty` | `v1.1.24` | Unix PTYs (through `xpty`; tests open PTYs directly) | pending notice audit |
| `golang.org/x/sys` | `v0.48.0` | Platform system calls (Unix and Windows) | BSD-3-Clause per locked module `LICENSE`; distribution notice audit pending |
| `modernc.org/sqlite` | `v1.59.0` | Local metadata database | pending notice audit |
| `github.com/Microsoft/go-winio` | `v0.6.2` | Windows named-pipe IPC | pending notice audit |
| `rsc.io/qr` | `v0.2.0` | Terminal QR code for `pterm pair` (packages `qr`, `qr/coding`, `qr/gf256` only; never the cgo `libqrencode` subpackage) | BSD-3-Clause, checked against the locked module `LICENSE`; distribution notice audit pending |

## Build-time web dependencies

The exact direct and transitive dependency graph is locked in
`pnpm-lock.yaml`. The P0 baseline uses Node 24.11.0 and pnpm 10.33.0. The
runtime companion does not require Node.

The root and website lockfiles resolve `source-map-js` 1.2.2, the patched
release for GHSA-68fv-2mgg-jv7q. A root workspace override restricts versions
below 1.2.2 to that compatible patch; later versions are not overridden.
This package is used by build tools, not the shipped Go host. Verification
and remaining separate advisories are recorded in
[issue-226 evidence](evidence/issue-226/README.md).

| Package | Version | Purpose | License check |
| --- | --- | --- | --- |
| `qr` | `0.7.0` (exact pin) | QR decoder for **Scan QR** in + Add a computer, on phones whose browser has no `BarcodeDetector` (iPhone). Only `decodeQR` from `qr/decode.js` and the QR tables that `decode.js` imports from `qr/index.js` (no encoder); no dependencies. Its own lazy chunk (34.8 kB, 14.3 kB gzip), fetched when the user taps **Scan QR**, never at start. Chosen over `jsqr` 1.4.0 (Apache-2.0, 130.0 kB, 47.5 kB gzip, last release 2020), measured in this build. | MIT OR Apache-2.0, checked against the locked package's `LICENSE` and `LICENSE-MIT`; contains code inspired by ZXing (Apache-2.0); distribution notice audit pending |

## Website build dependencies

The website in `site/` (landing page and docs) is built with Astro 7.3.5 and
Starlight 0.42.4, locked in `site/pnpm-lock.yaml`. It is a separate static
site: nothing from `site/` ships in `pterm` or the phone app.

## Security tooling

CI pins Gitleaks 8.30.1 and govulncheck 1.8.0. Scanner results require human
review and are not proof that the repository is free of secrets or vulnerable
code.

## How is the separate docs lockfile audited?

The root workspace audit cannot see `site/pnpm-lock.yaml`. The
`vulnerability-scan` job also installs that lockfile with `--ignore-workspace`,
builds the static site with its link validator, and runs `scripts/audit-site.mjs`.
The original root `pnpm audit --audit-level high` gate remains unchanged.
The site gate fails on any new moderate/high/critical finding, audit errors,
report drift, changed dependency paths/versions or availability of a patch for
one of the two reviewed findings below. It prints their advisory IDs and actual
counts on every run; it does not claim zero advisories.

### Which findings remain on 2026-10-08?

| Finding | Actual use and assessment | Upstream status and action |
| --- | --- | --- |
| [GHSA-ch52-4w7c-c8xp](https://github.com/advisories/GHSA-ch52-4w7c-c8xp), `http-cache-semantics` 4.2.0, exact path `astro > http-cache-semantics` | Astro uses it in remote-image build helpers to calculate TTLs from responses to its own requests. That module never calls `satisfiesWithoutRevalidation`, the vulnerable incoming `max-stale` path. This project uses the passthrough image service and local WebP assets. The deployed output is static files, with no Node server, shared authenticated response cache or incoming cookie/header processing. No affected request path is demonstrated. | No patched version is listed in the primary advisory or current registry. Retain this exact unpatched finding visibly; review again if the version/path, advisory patch status, image service or static deployment changes. |
| [GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm), `braces` 3.0.3, exact path `starlight-llms-txt > micromatch > braces` | The plugin matches local document IDs against the `include`, `exclude`, `promote`, `demote` and custom-set patterns configured by this repository. Those patterns are short literal globs; there is no visitor input or deeply nested brace pattern. CI executes repository code already; this is not a boundary allowing an unauthenticated caller to supply a pattern. No affected external input path is demonstrated. | No patched version is listed. Retain only this advisory at this exact version/path; no package-wide or root-workspace exclusion exists. Reassess if patterns can come from external data or the dependency changes. |
| [GHSA-rj75-hqrm-r3gf](https://github.com/advisories/GHSA-rj75-hqrm-r3gf), `postcss-selector-parser` 6.1.4 | Expressive Code's `postcss-nested` parent pins the older major. The site-only edge is overridden to patched 7.1.6; the full docs build passes and all eight emitted CSS files are byte-identical to the prior parser's output. | Patched 7.1.6 is installed and its registry integrity matches the lockfile. The moderate finding disappears. The primary advisory also excludes ordinary trusted-source build use. |

The separate source-map-js update belongs to #226 / PR #234; this work is
stacked on that branch. After both patched updates, the raw site audit still
exits 1 and reports **two high, zero moderate** findings. These two retained
entries are a reachability assessment, not a vulnerability fix or proof of
absence. They are limited to the static documentation build; this policy does
not permit either package in a shipped host runtime or an untrusted request
path. Any change to the deployment, patterns, image service or relevant
configuration requires a fresh review of this table before merge.
