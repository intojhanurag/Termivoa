# How do I verify fresh agent choices?

This development-only fixture renders the actual Sessions sheets with React
StrictMode, synthetic providers and intercepted mutation requests. It never
contacts a broker, herdr, a real computer or an agent provider.

From the repository root, with Node 24 and an existing Chromium executable:

```sh
pnpm install --frozen-lockfile
CHROME='<installed Chromium executable>' node scripts/test-provider-kinds.mjs
```

The runner opens a loopback Vite server and temporary Chromium profile, runs
eight effect/UI scenarios, and closes both in `finally`. Missing Chromium is
an explicit prerequisite failure; the runner installs nothing. The fixture
is typechecked, but not imported by the production entry or included in the
production web bundle. This focused browser suite is separate from Vitest.

Checks cover home/peer settings origin routing, latest-provider changes,
pending agent button/form guards, removed selections, one successful fresh
request, chooser cancel/reopen, No agent/Shell while checking, rejected quiet
reads, shared-main-folder last-tab guidance, and safe stale close conflicts.
