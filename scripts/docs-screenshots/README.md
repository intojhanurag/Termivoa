# Docs screenshots

Takes the phone screenshots in `site/src/assets/docs/` against an isolated dev stack with demo data only (AGENTS.md rule 23).

macOS only: it uses Google Chrome and `/Users/Shared`. It writes outside the repository, to `/tmp/demo-home`, `/tmp/pterm-demo`, `/tmp/demo-chrome-*` and `/Users/Shared/code` (removed again by the clean step, and only when its own marker file is there).

1. Build a test binary with your normal `HOME`:

   ```sh
   pnpm install --frozen-lockfile && pnpm build:web && ./scripts/embed-web.sh
   go build -tags webdist -o /tmp/pterm-demo ./cmd/pterm
   ```

2. `node scripts/docs-screenshots/capture.mjs` builds the demo home (`/tmp/demo-home`) and the demo repositories (`/Users/Shared/code`, so the phone shows `~/code/...`), runs the dev stack on port 8788, and writes WebP files next to the script.
3. Look at every image. Copy the ones you need to `site/src/assets/docs/`, naming phone screens `phone-<name>.webp`.
4. Clean up: `node scripts/docs-screenshots/setup.mjs clean`. It stops the dev stack that `capture.mjs` started, then removes the demo home and repositories. `capture.mjs` saves that stack's PID in `/tmp/demo-home/.capture-pid`; the clean step sends SIGINT to that PID only, waits up to 15 seconds for `pterm up --dev` to stop its own broker and gateway, and never matches processes by name, so another demo stack is left alone.

`agent.js` is a neutral stand-in agent with no vendor name or logo. The script needs Google Chrome and never touches your real `pterm`.
