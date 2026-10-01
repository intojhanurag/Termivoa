# Docs screenshots

Takes the phone screenshots in `site/src/assets/docs/` against an isolated dev stack with demo data only (AGENTS.md rule 23).

1. Build a test binary with your normal `HOME`:

   ```sh
   pnpm install --frozen-lockfile && pnpm build:web && ./scripts/embed-web.sh
   go build -tags webdist -o /tmp/pterm-demo ./cmd/pterm
   ```

2. `node scripts/docs-screenshots/capture.mjs` builds the demo home (`/tmp/demo-home`) and the demo repositories (`/Users/Shared/code`, so the phone shows `~/code/...`), runs the dev stack on port 8788, and writes WebP files next to the script.
3. Look at every image. Copy the ones you need to `site/src/assets/docs/`, naming phone screens `phone-<name>.webp`.
4. Clean up: `pkill -INT -f "/tmp/pterm-demo up --dev"; node scripts/docs-screenshots/setup.mjs clean`.

`agent.js` is a neutral stand-in agent with no vendor name or logo. The script needs Google Chrome and never touches your real `pterm`.
