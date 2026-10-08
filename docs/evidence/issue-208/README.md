# Issue #208: first-agent availability

Date: 2026-10-08. Baseline: `08f0332`. Branch: `fix/issue-208`.
All agent commands and sockets used in tests are fake, inside guarded temp
folders. Nothing starts the owner's agents, logs in, consumes provider
credits, installs software, or changes production settings.

## Reproduction before the fix

`go test -run '^(TestInstalledKindsBeforeFirstAgentAndAfterLastStops|TestCreateStartsFirstInstalledAgent)$' ./internal/herdr`

```text
--- FAIL: TestInstalledKindsBeforeFirstAgentAndAfterLastStops (0.48s)
    installed kinds = [], want [claude codex] even with no running agents
--- FAIL: TestCreateStartsFirstInstalledAgent (0.16s)
    first agent create = {AgentStarted:false ItemID: Problem:<nil> TargetID:} herdr: agent kind is not offered
FAIL
```

The original filter at `internal/herdr/changes.go:101` accepted a help-derived
kind only if the current snapshot counted a running agent. This prevents
starting the first agent and drops a choice after its last agent stops.

## Fix and meaningful regression coverage

- A no-agent snapshot offers fake installed claude/codex before the first
  launch, while they run, and after the last stops. Discovery is checked
  against executable marker scripts: none was invoked.
- Fake first-agent worktree create and newTab both reach `agent.start` and
  report a started agent. No real notification, agent or network provider
  participates.
- Canonical Cursor (`cursor-agent`) and Kiro (`kiro-cli`) commands are
  recognized. Non-executable files, directories, broken symlinks, empty or
  relative PATH, unadvertised kinds and unknown command names do not add
  installed choices. Unknown supported running kinds retain existing behavior.
- Output is limited to 16 choices with stable running-count/name ordering.
  The first 64 PATH entries and 64 help-derived names bound discovery work.
  Installation is noticed at the 30 s timer; settings refresh removes a
  deleted command without waiting for that timer.

Primary source for the 24 canonical Unix launch commands:
[herdr v0.9.3 source](https://github.com/herdrdev/herdr/blob/v0.9.3/src/detect/mod.rs#L153-L185).
Detection manifests are not a startable/installed-kind API. The observed
CLI help still controls which of these known kinds this binary supports.

## Verification executed

Go 1.27.1; Node 24.11.0; pnpm 10.33.0. Commands ran with the existing Node
installation on PATH; repository dependencies used frozen lockfiles.

| Command | Result |
| --- | --- |
| `go test -race -count=5 -run '^(TestInstalledKinds|TestCreateStartsFirstInstalledAgent|TestManageStartsFirstInstalledAgent)' ./internal/herdr` | PASS, 23.631 s |
| `go test -race ./internal/herdr ./internal/protocol ./internal/broker` | PASS, 70.789 / 5.937 / 107.036 s |
| `go vet ./internal/herdr ./internal/protocol ./internal/broker` | PASS |
| `pnpm --filter @phone-terminal/web test src/screens/ProviderCreate.test.tsx src/screens/ProviderManage.test.tsx` | PASS, 68 tests |
| `pnpm typecheck` | PASS |
| `pnpm build:web` | PASS, production bundle built |
| `pnpm protocol:check` | PASS against reviewed staged generated files |
| `git diff --cached --check` and staged addition secret / personal-path / artifact scan | PASS |
| `cd site && pnpm install --frozen-lockfile --ignore-workspace && npx astro build` | PASS, 29 pages, all internal links valid |

The protocol payload shape and revision do not change. The existing
`agentKinds` field's schema description and generated Go/TypeScript comments
now state the availability rule.

## Limits and pending evidence

Presence on the broker's configured PATH is a hint: herdr's pane shell may
have different startup settings or require agent sign-in. Unknown future
canonical kinds and shell-only custom commands appear only once observed
running. At most 16 choices are sent, with running kinds first. This does
not guarantee a successful launch; existing partial-success replies apply.
The scan bounds filesystem operations, not wall-clock time on a hung
filesystem. No fresh database or pairing is needed.

Full `scripts/check`, real-agent launches, real-phone owner checks, CI, PR
review and deployment were not executed by this helper. Root owns those
gates. No performance percentile or real-phone success is claimed.
