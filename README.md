<div align="center">

<img src="site/src/assets/mark.svg" width="72" height="72" alt="Termivoa logo: a terminal prompt in a rounded square">

# Termivoa

**Your computer's terminal and coding agents, on your phone. Private over Tailscale.**

[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go 1.27.1](https://img.shields.io/badge/go-1.27.1-00ADD8.svg?logo=go&logoColor=white)](go.mod)
[![Docs](https://img.shields.io/badge/docs-termivoa.com%2Fdocs-64D8E8.svg)](https://termivoa.com/docs/)
[![Website](https://img.shields.io/badge/website-termivoa.com-07090C.svg)](https://termivoa.com)

[Website](https://termivoa.com) · [Docs](https://termivoa.com/docs/) · [Quick start](https://termivoa.com/docs/quick-start/) · [Release notes](https://termivoa.com/docs/reference/release-notes/)

<img src="docs/images/sessions.png" width="300" alt="Termivoa Sessions list on a phone: acme-web waits with Run the test suite? and Answer, billing-api is working, each row with its project, shell and last output">

</div>

Termivoa lets you start, watch and answer shells and AI coding agents such as
Claude Code or Codex on your own computer from your phone. The phone talks
only to your computer, through your own Tailscale network. There is no
Termivoa server, no account and no telemetry. Termivoa is free and open
source.

> [!NOTE]
> Termivoa is a free, open-source beta (`v0.1.0-beta.4`, unsigned builds). The full flow is
> tested on an Apple Silicon Mac and on Linux (containers and one Ubuntu 24.04
> VPS). Windows support is written but not yet run on a real Windows machine.

## Why

Coding agents stop and ask: "Run the test suite?", "Allow this edit?". If you
are not at your desk, the work waits. Termivoa puts those sessions on your
phone, so you can answer, check what changed, and keep going, without
opening a port or trusting a cloud service.

## Features

- **Live sessions.** Start shells or coding agents and switch between them
  with tabs. Sessions keep running when the phone sleeps or loses signal.
- **Agent status and answer cards.** See **Working**, **Waiting** or **Your
  turn**, and answer Claude Code and Codex questions with one tap.
- **Needs you**. One list of every agent that waits for you,
  with **Next** to go from one to the next.
- **Notifications.** A buzz when an agent waits or finishes. Only the session
  name and status, never terminal text.
- **Wall.** All running sessions at once as small live screens.
- **Changes.** See the files the agent changed and each diff, then commit and
  push (never forced) from the phone.
- **Files.** Browse, search and read project files; tap `src/app.ts:42` in
  the terminal to open it.
- **Worktrees.** Start a task in its own folder and branch with Claude or
  Codex already running in it.
- **Several computers.** Use a Mac, a Linux server and a VPS from one phone
  app, with one Sessions list.
- **herdr**. Projects, worktrees and agents from
  [herdr](https://herdr.dev), on the phone.
- **GitHub**. Connect a computer to GitHub and clone a
  repository onto it with one tap. No `gh` needed.
- **Chat view (next beta, computer side only).** Turn it on with
  `pterm chat on`; the phone screen comes in a later beta.
- **Phone-first keys.** Agent keys (Esc, Ctrl+C, Shift+Tab, `y`, Enter), a
  composer for long text, snippets, project buttons and launch presets.
- **Touch gestures.** Hold and slide for arrow keys, tap to move the cursor,
  swipe to switch session, pinch for text size, two-finger tap for Esc.
- **Preview, photo and voice.** Open the session's dev server, send a photo
  without location data, or dictate a prompt.
- **Desktop attach.** `pterm attach` opens the same session in any desktop
  terminal.
- **Pairing you can check.** Scan a QR code, compare a short code, approve on
  the computer. Revoke a phone at any time.

Every feature has a guide in the [docs](https://termivoa.com/docs/).

## Quick start

1. Install [Tailscale](https://tailscale.com/download) on the computer and
   the phone, signed in to the same account.
2. Install `pterm`: [macOS](https://termivoa.com/docs/install/macos/),
   [Linux](https://termivoa.com/docs/install/linux/) or
   [Windows](https://termivoa.com/docs/install/windows/). You need no
   account. On an Apple Silicon Mac:

   ```sh
   mkdir -p ~/.local/bin
   curl -fsSL -o ~/.local/bin/pterm https://github.com/intojhanurag/Termivoa/releases/download/v0.1.0-beta.4/pterm-darwin-arm64
   chmod +x ~/.local/bin/pterm
   ```

3. Run setup. It checks the host, shows what it will do and asks once. It
   asks again before it runs `tailscale serve`, and it never uses Funnel.

   ```sh
   pterm setup
   ```

4. Scan the QR code with the phone. If the phone shows the same code as the
   computer, answer `y`.

Full steps: [Quick start](https://termivoa.com/docs/quick-start/) and
[Pair your phone](https://termivoa.com/docs/pair-your-phone/).

### Build from source

You need Go 1.27.1, Node.js 24.11.0 and pnpm 10.33.0. Node and pnpm are
build tools only; the computer that runs `pterm` does not need them.

```sh
git clone https://github.com/intojhanurag/Termivoa.git
cd Termivoa
pnpm install --frozen-lockfile
pnpm build:web && ./scripts/embed-web.sh
go build -tags webdist -o bin/pterm ./cmd/pterm
```

`pterm setup` registers the binary at its current path to start at login, so
keep it at a stable path. To try Termivoa without changing startup or
Tailscale settings, use the development profile: `pterm up --dev`
([details](docs/install.md#development-harness)).

## How it works

One Go program, `pterm`, runs as two background processes for your user. One
owns every terminal and its state; the other serves the phone app on
`127.0.0.1` only. Tailscale Serve gives the phone private HTTPS to it. The
phone gets screen updates, not raw terminal bytes, and sends structured key
presses and text. Only one device controls a session at a time. Read more in
[How does it work?](https://termivoa.com/docs/#how-does-it-work) and the
[security reference](https://termivoa.com/docs/reference/security/).

## Documentation

| Topic | Link |
| --- | --- |
| Quick start and install | [termivoa.com/docs/quick-start](https://termivoa.com/docs/quick-start/) |
| Feature guides | [termivoa.com/docs](https://termivoa.com/docs/) |
| CLI reference (`pterm help`) | [termivoa.com/docs/reference/cli](https://termivoa.com/docs/reference/cli/) |
| Troubleshooting | [termivoa.com/docs/reference/troubleshooting](https://termivoa.com/docs/reference/troubleshooting/) |
| Security model | [termivoa.com/docs/reference/security](https://termivoa.com/docs/reference/security/) |
| Architecture (for contributors) | [docs/architecture.md](docs/architecture.md) |
| Roadmap | [docs/pending-work.md](docs/pending-work.md) |

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) for the
dev setup, checks and pull request rules, and follow the
[Code of Conduct](CODE_OF_CONDUCT.md). For questions, see
[SUPPORT.md](SUPPORT.md).

## Security

Please do not report security problems in public issues. Follow
[SECURITY.md](SECURITY.md). A session runs with your user's permissions;
projects are not sandboxes. See the
[threat model](docs/threat-model.md).

## Licence

Termivoa is free and open source under the [Apache License 2.0](LICENSE).
Copyright 2026 Anurag Ojha and Termivoa contributors. Bundled third-party
code and fonts keep their own licences: see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Acknowledgements

Termivoa builds on [Tailscale](https://tailscale.com), the
[Charm](https://charm.sh) terminal libraries (`vt`, `ultraviolet`, `ansi`,
`xpty`), [xterm.js](https://xtermjs.org),
[modernc.org/sqlite](https://modernc.org/sqlite),
[React](https://react.dev), [Astro Starlight](https://starlight.astro.build)
and the [Geist](https://vercel.com/font) fonts.
