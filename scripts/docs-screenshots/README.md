# Docs screenshots

Takes the phone screenshots in `site/src/assets/docs/` against an isolated dev stack with demo data only (AGENTS.md rule 23).

macOS only: it uses Google Chrome and `/Users/Shared`. It writes outside the repository, to `/tmp/demo-home`, `/tmp/pterm-demo`, `/tmp/demo-chrome-*` and `/Users/Shared/code` (removed again by the clean step, and only when its own marker file is there).

1. Build a test binary with your normal `HOME`:

   ```sh
   pnpm install --frozen-lockfile && pnpm build:web && ./scripts/embed-web.sh
   go build -tags webdist -o /tmp/pterm-demo ./cmd/pterm
   ```

2. `node scripts/docs-screenshots/capture.mjs` builds the demo home (`/tmp/demo-home`) and the demo repositories (`/Users/Shared/code`, so the phone shows `~/code/...`), runs the dev stack on port 8788, and writes WebP files next to the script.
   It also shoots the pairing code (`pair-code`, for the README), Files (`files-list`, `files-viewer`) and a dev-server preview (`preview`: `python3 -m http.server` on port 4321 in docs-site, through the dev preview listener).
3. `node scripts/docs-screenshots/computers.mjs` takes the multi-computer screens (`phone-computers-found`, `-confirm`, `-code`, `-adding`, `-sessions`, `-offline`, 390×844). It runs two dev stacks, mac and vps, with the e2e harness (`scripts/e2e-multicomputer/lib.mjs`: fake Tailscale, a loopback HTTPS front, the demo tailnet `tailx.ts.net`), so it needs Go and the embedded web build (add `--web-build` to build it first). It cleans up after itself (`/tmp/mce2e.*` stays for a look).
   `node scripts/docs-screenshots/herdr.mjs` takes the herdr screens (`phone-herdr-tab`, `-live`, `-create`, `-integrations`, 390×844) the same way, with the fake herdr (`scripts/fakeherdr`) in each stack's own home: projects `shop` (worktrees `silver-meadow`, `lucky-valley`) on mac and `acme-web` on vps. The blocked agent's screen is fixed demo text from the fake's scenario (`screens`).
4. `node scripts/docs-screenshots/capture.mjs landing` updates the landing page pictures in `site/public/assets/`: `answer`, `sessions` and `preview` from step 2, `lock.webp` rendered from `site/public/_lock.html`, and `og.png` from `site/public/_og.html`.
5. Look at every image. Copy the ones you need to `site/src/assets/docs/`, naming phone screens `phone-<name>.webp` (`keyboard-row-crop.webp` is `key-row.webp`). The README pictures in `docs/images/` are the PNGs in `png/` scaled to 440 px wide: `sips -s format png --resampleWidth 440 png/sessions.png --out docs/images/sessions.png` (`live-terminal` → `live`, `project-buttons` → `snippets`, `pair-code` → `pair`).
6. Clean up: `node scripts/docs-screenshots/setup.mjs clean`. It stops the dev stack that `capture.mjs` started, then removes the demo home and repositories. `capture.mjs` saves that stack's PID in `/tmp/demo-home/.capture-pid`; the clean step sends SIGINT to that PID only, waits up to 15 seconds for `pterm up --dev` to stop its own broker and gateway, and never matches processes by name, so another demo stack is left alone.

`agent.js` is a neutral stand-in agent with no vendor name or logo. The script needs Google Chrome and never touches your real `pterm`.
