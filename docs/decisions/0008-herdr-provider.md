# ADR 0008: herdr on your phone (terminal workspace providers)

- Status: **accepted** (2026-10-04). The owner approved the product in Paper (file `termivoa`, artboards H1–H6) and delegated the approval of this feature in writing to the coordinating agent ("approve if all looks good, then ship"). Under AGENTS.md rule 24 (new network routes) the coordinator approves after the independent security and feasibility reviews, which are recorded in the design, section 20.
- Date: 2026-10-04
- Design: [`docs/herdr-provider.md`](../herdr-provider.md) (version 2: the wire contract, the lab evidence, the work split)
- Builds on: ADR [0002](0002-server-authoritative-terminal.md), ADR [0005](0005-multi-computer.md), [`docs/notifications.md`](../notifications.md), [`docs/agent-state.md`](../agent-state.md)

## Context

The owner runs his coding agents in [herdr](https://herdr.dev), a terminal workspace manager: one workspace per repository, git worktrees as more workspaces, tabs, panes (real terminals), and agents with a state (`blocked`, `working`, `done`, `idle`, `unknown`). herdr has a local socket API (newline-delimited JSON) and a CLI over it. He wants the phone to show his herdr projects, worktrees and agent states, open one agent's real terminal full screen, and buzz when an agent is blocked or finished. Later (M2) he wants to make a worktree and start an agent from the phone. He wants a seam for other tools later (tmux, zellij, partner tools): the phone must not know herdr. He paired his phone and a VPS today, so nothing may force pairing again.

Lab facts that shape the decision (herdr 0.8.2, installed, and 0.9.3, the latest; isolated sessions with a throwaway `HOME`; design, section 1):

- `session.snapshot` over the socket takes about 2 ms for 12 workspaces (32 KB); the shape is the same in 0.8.2 (protocol 20) and 0.9.3 (protocol 22).
- Agent status events need one subscription per pane, status changes fire no workspace event, and 0.8.2 replays old events on subscribe while 0.9.x does not.
- `herdr agent attach <pane>` (0.8.2) and `herdr terminal attach <terminal>` (0.9.x, any pane) attach a terminal client to one pane. **The attach client gives the pane its own size** (also in the herdr UI on the laptop), keeps it while attached, and the herdr UI gets its size back only when the attach ends. herdr exposes no real PTY size: the `layouts` rect differs from it (borders; stale when no UI is attached), and a 0×0 size breaks the pane or ends the attach.
- The attach client's only prefix command is `ctrl+b q`; `ctrl+b ctrl+b` sends one literal `ctrl+b`. A second direct attach is refused unless it uses `--takeover`.
- `done` means "idle, not yet seen in the herdr UI"; a herdr UI that shows the tab turns it into `idle`. A phone attach marks nothing as seen.

## Decision

1. **A generic wire protocol is the seam.** The phone sees providers, groups, items, targets and state words: `GET /api/v1/providers/{provider}`, `POST …/open`, `PATCH /api/v1/providers/{provider}`, and in M2 `POST …/create`; `HTTPSettingsResponse.providers` lists what a computer has. Types in `protocol/v1` now; protocol revision 4 when the routes ship. On the host every herdr detail sits in a new package `internal/herdr`, used by the broker as one concrete type; a Go interface comes with the second provider.
2. **The broker polls the snapshot every 2 s over herdr's socket** and serves every read from its cache; it never waits on herdr in a read or in the Sessions list. Events are not used.
3. **Opening a terminal is option A: an in-memory broker session that runs herdr's attach client**, exec'd with argv, never a shell, at the phone's size. Live, keys, compose, answer cards and take-control work unchanged. Provider sessions are never stored (no schema change), never listed with normal sessions, one per pane and terminal, at most 4 and not counted in the 8; they end 10 s after the last viewer leaves, which gives the laptop its size back; the phone closes its stream when hidden; the broker doubles `ctrl+b` so a phone can never send a prefix command; never `--takeover`; opening never takes control.
4. **Push from herdr's state:** blocked (two polls in a row) and finished (`done` or `idle` after at least 20 s of observed work), once per change, in a fixed format with only names and the state ("herdr · shop · silver-meadow" / "claude needs you."), with their own rate budget. Never terminal text or titles.
5. **Settings → Integrations** per computer: on/off and which states buzz, in the existing key/value `settings` table. Nothing shows on a computer without herdr.
6. **M2** adds create: `worktree.create`, `agent.start` and an optional first prompt over the socket, in a broker-owned context, with tight name, kind and prompt rules.

## Rejected

- **B. Read and send through `pane read` and `send-keys`.** Not a terminal, and a second input path beside the broker's single writer (rule 10).
- **C. A full herdr UI client in a broker PTY.** The whole herdr UI in 45 columns, prefix keys and the mouse (ruled out); on 0.8.2 focus is shared, so the phone would move the laptop's view.
- **0.9's `terminal session control` / `observe` streams.** They would allow watching without resizing, but they do not exist in 0.8.2 and need a second terminal pipeline. Revisit when 0.8 support ends.
- **Restoring the laptop's size from the snapshot when control changes.** The rect is not the real size and herdr has no size API (lab); only ending the attach restores it.
- **Storing provider sessions in `session_metadata`.** It needs a schema change, and a schema change means a fresh database on every computer: pairing, devices and push subscriptions would be lost.
- **Events as the source of truth; a CLI process per poll; `agent.focus` to clear `done`** (it moves the laptop's focus).

## Consequences

- **While a herdr terminal is open on a phone, that pane on the laptop is phone-sized** (lab-proven). It gets its size back about 10 s after the phone leaves. Laptop typing still reaches the pane. Live says so in one line.
- Any paired phone can see herdr workspace names, tab labels and agent kinds on that computer, and type into any herdr terminal. This is no new authority: such a phone could already run `herdr` in a Termivoa session. Paths and terminal titles are never sent.
- An upgrade changes no database: pairing, devices, computers and push subscriptions stay.
- `done` stays on the phone until the owner looks at that tab in herdr on the laptop.
- The broker runs one more background loop (about 2 ms every 2 s) while herdr is installed and on.
- macOS and Linux only; the default herdr session only.

## Reviews

See [`docs/herdr-provider.md`](../herdr-provider.md), section 20.
