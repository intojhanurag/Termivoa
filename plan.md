# Phone Terminal — authoritative Go implementation plan

Owner: Anurag Kumar Ojha. Revised: 20 September 2026. Intended implementer: Codex, using the owner's chosen model configuration. Status: researched implementation specification; the application has not been built or benchmarked as part of this document.

Private development is mandatory until Anurag manually makes the repository public. The implementation agent must never change repository visibility to public; Anurag owns that manual action. The first remote project action is to create and verify a private repository, before implementation code is pushed. Sections 17–20 define the required repository, commit, CI and open-source release workflow.

This revision supersedes all earlier Node-backend plans and their prompts. Implement this document as the source of truth. The language and architecture decisions are final for v0. Do not substitute a Node companion, a Rust backend, an SSH-only gateway, a cloud shell, or an agent-chat application. No rewrite is planned.

## 1. Mission and completion contract

Build an open-source, self-hosted phone website that controls real terminal sessions running on the owner's Mac or Windows computer. The same managed session can be viewed on the computer and phone. Shells and coding agents continue while the phone is disconnected. The first release supports one computer, one owner, and multiple sessions. Quality gates determine completion; there is no calendar shortcut.

The primary experience is: install the companion; connect privately; pair the phone; start a managed shell from the desktop or phone; run an installed coding CLI; use the phone's keyboard, terminal controls, and prompt composer; leave and return; resume the live terminal safely.

Existing projects overlap with this product. Do not claim that phone terminals, Windows support, or mobile coding agents are inventions of this project. Our objective is verified reliability and excellent phone interaction. Competitor comparisons are useful baseline evidence, not permission to redirect this assignment into a different product.

A session must start under this companion. Existing arbitrary Ghostty, Terminal, or PowerShell processes cannot simply be adopted. Users keep their preferred desktop terminal application by launching our attach CLI within it.

The host must remain awake, logged in, and running the session broker. A phone disconnect or gateway restart must preserve sessions. A broker crash, OS logout, or host reboot does not preserve a live process. Never silently restart an agent and describe it as the same session.

The agent's job is to implement, verify, and prepare reviewable release artifacts. Creating this plan does not itself authorize publishing software, purchasing services, changing account permissions, or using production credentials.

## 2. Mandatory opening preflight: ask early about access, never about settled design

At the beginning of implementation, read the repository's existing instructions and inspect its state. Produce a short preflight report showing what is available, what is missing, and which milestone depends on it. Use existing session authorization; do not repeatedly request the same permission. Anurag has already logged in to gh and commonly used tools in his terminal. Verify the existing authenticated session first; request login or credentials only if the required access is actually unavailable. This is an ongoing collaboration: share milestone progress and concrete blockers, accept Anurag's help as needed, and continue authorized work without treating the build as a one-shot task.

Ask the owner about missing prerequisites before beginning the dependent work. Group known questions into one clear request. Continue independent local implementation while waiting. Do not block unit tests on a signing certificate, and do not reach release day pretending that missing Windows access was a surprise.

| Need | What the agent must establish early | When to request owner action |
| --- | --- | --- |
| Implementation workspace | Existing target repository, branch, and its instructions | If no workspace is supplied, ask where to implement. A reversible local scaffold may be prepared in an isolated directory while waiting. Do not modify an unrelated repository. |
| GitHub destination | Default intended target intojhanurag/phone-terminal, verified owner identity, private visibility and authenticated access | The owner has directed private-first development. At implementation startup, verify the account and target; create the new repository explicitly private before implementation. If authentication is missing, ownership differs, or that target already contains unrelated work, ask before proceeding. Never substitute another account or create publicly. |
| Mac and Windows testing | Access to Apple Silicon macOS and Windows 11 x64; whether Windows testing includes a physical machine or VM | Ask at preflight if either is unavailable. Build and test the available platform, record the missing gate, and never substitute WSL for native Windows coverage. |
| Real phones | iPhone Safari/Home Screen app and Android Chrome testing availability | Ask at preflight for a test arrangement. An owner-run checklist is acceptable evidence if the agent cannot operate a device. Emulated viewports are not equivalent. |
| Tailscale | Owner's existing installation/account and ability to connect the host and phone | Before installation, interactive login, enabling Serve/HTTPS, or changing its configuration. The owner completes login; never request their password. |
| Host installation | Consent to register login startup, add the user CLI path, or install test builds outside the repo | Prepare scripts and describe the exact changes before asking. No elevation for the running companion. |
| Agent smoke tests | Which already-installed coding CLI and test repository can be used | Before a test that spends credits, accesses a real account, or edits the user's project. Standard shell/fixture tests need no provider credentials. |
| Apple release signing | Apple Developer membership, Developer ID identities, and approved notarization authentication location | Flag at preflight; request access before signing/notarization. Use the owner's Keychain or scoped CI secrets, not pasted private keys in chat. |
| Windows release signing | Owner-approved publicly trusted code-signing identity and its hardware/managed key access | Flag at preflight; request before SignTool signing. Never invent an identity or buy a certificate automatically. Do not assume a signing service supports the owner's country or account. |
| Public identity and release | Final product name, package identifiers, public repository and explicit publish authorization | Before applying public signing identities or publishing. Use the internal development name below until confirmed; produce complete unsigned/test artifacts first. |

No model API key, hosted database, domain purchase, cloud VM, payment processor, OAuth application, or App Store account is required to implement local v0. Do not create those dependencies. Existing agent subscriptions remain with their CLI on the host.

When a required credential is absent: state the exact purpose, minimum access, safe place to configure it, and blocked milestone. Never fabricate a token, weaken authentication, expose a public tunnel, switch providers, remove signing, or mark a test passed to work around the absence.

Required request format: “I need [access/action] for [specific milestone]. It permits [scope] and will change/use [resource]. Please configure it through [secure location/action]. Until then, I can complete [independent work]; [specific gate] remains blocked.” Do not ask the user to choose a programming language, database, network architecture, or terminal library; those decisions are below.

## 3. Locked technology choices

| Layer | Implement this |
| --- | --- |
| Backend language | Go 1.27.1 baseline, as listed by the official release feed on this plan's date. |
| Backend services | Go standard-library net/http, context, os/exec, crypto/rand, encoding/json and slog. No general web framework. |
| WebSocket transport | github.com/coder/websocket v1.8.15 baseline. |
| OS terminal integration | github.com/charmbracelet/x/xpty v0.1.4, behind internal/ptyhost. |
| Authoritative terminal emulator | github.com/charmbracelet/x/vt at v0.0.0-20260920004010-53e2afe73ae5, behind internal/terminal. |
| Desktop input decoding | github.com/charmbracelet/ultraviolet; use the version compatible with the pinned vt dependency, initially its required v0.0.0-20260303162955-0b88c25f3fff. |
| ANSI utility dependency | github.com/charmbracelet/x/ansi v0.11.7 baseline, matching the vt module. |
| Local metadata database | SQLite through modernc.org/sqlite; broker is the sole database owner. |
| Local IPC | Unix-domain socket on macOS; named pipe with a current-user ACL on Windows using github.com/Microsoft/go-winio. |
| Phone interface | TypeScript strict mode, React, Vite, CSS Modules. |
| Phone terminal display | @xterm/xterm, @xterm/addon-webgl, and @xterm/addon-unicode-graphemes 0.4.0. Fall back to the built-in renderer after WebGL loss. Use xterm as the display surface described below. |
| Browser history list | @tanstack/react-virtual for bounded, selectable scrollback rows. |
| Web build tooling | Node 24 LTS and pnpm, used at build/development time only. No Node runtime in the shipped companion. |
| Protocol types | JSON Schema Draft 2020-12 as canonical source; quicktype generates Go and TypeScript types, Ajv validates client-side shapes, explicit Go validation checks bounds and semantics. |
| Web assets | Embed the production Vite build in the Go executable with go:embed. |
| v0 network | Installed Tailscale client plus Tailscale Serve, private HTTPS only. |
| License | Apache-2.0 for original code, with preserved third-party licenses and notices. |
| CI | GitHub Actions when repository access is authorized; Linux generic tests, macOS and Windows integration/build jobs. |

Go runtime release source: [official Go downloads feed](https://go.dev/dl/?mode=json). The selected xpty package documents Unix PTYs, Windows ConPTY, and a special process-wait helper: [xpty v0.1.4 documentation](https://pkg.go.dev/github.com/charmbracelet/x/xpty@v0.1.4). The selected Go emulator exposes screen cells, cursor callbacks, scrollback, and input encoding: [vt documentation](https://pkg.go.dev/github.com/charmbracelet/x/vt). These APIs establish an implementation route; they do not prove our complete product works.

The vt module is pre-stable and uses a commit-based version. Pin it, retain its source and license in the dependency record, and treat its compatibility suite as mandatory. Do not call it a mature drop-in equivalent to xterm. The Go module proxy identified the pinned commit during research; the source was also inspected. No executable Go prototype was run in this planning environment. The Unicode addon version was verified against the npm registry and its published README, which marks it experimental. The implementation must earn support claims through P1/P2 evidence, including this dependency.

For dependencies without an exact version above, P0 resolves the newest non-prerelease release compatible with these pinned foundations once, then records exact versions in go.mod/go.sum and pnpm-lock.yaml. Commit tools and CI action pins. No floating latest references in release builds. Security updates require an explicit dependency change and regression run. Resolving exact package patches is implementation work, not a new architecture choice.

Read the primary references for [coder/websocket](https://github.com/coder/websocket), [SQLite driver](https://pkg.go.dev/modernc.org/sqlite), [Windows IPC](https://pkg.go.dev/github.com/Microsoft/go-winio), and [Go asset embedding](https://pkg.go.dev/embed). Do not add Redis, Postgres, Docker as a user requirement, Kubernetes, Electron, a hosted auth service, or another backend language.

## 4. Repository and agent operating contract

Use internal name Phone Terminal and executable pterm. This is a development identifier, not a trademark or package-name availability claim. Once the private repository intojhanurag/phone-terminal is created and verified, use module github.com/intojhanurag/phone-terminal from the first code commit. If the owner explicitly changes the destination, use that confirmed path. Do not use a fictitious public module path or move the repository solely to remove private development history.

| Path | Responsibility |
| --- | --- |
| cmd/pterm | CLI entry point and subcommands, including internal broker/gateway modes. |
| internal/broker | Sessions, command ordering, controller ownership, device authorization. |
| internal/ptyhost | All xpty and platform lifecycle details. |
| internal/terminal | The vt adapter, normalized frame production, terminal capability policy. |
| internal/protocol | Generated Go types plus hand-written validation and compatibility checks. |
| internal/ipc | Local framing, socket/pipe ownership, peer checks. |
| internal/gateway | HTTP, WebSocket, static assets and request limits; never owns PTYs. |
| internal/auth | Pairing, bearer-token hashes, cookie validation and revocation. |
| internal/store | SQLite migrations and broker-owned persistence. |
| internal/platform | Startup registration, user paths, browser opening and process isolation. |
| web/src/terminal | Frame renderer, input controller, history view, compatibility fixtures. |
| web/src/screens | Setup, sessions, terminal, connection state and settings. |
| web/src/generated | Generated TypeScript protocol types. |
| protocol/v1 | Canonical JSON schemas, capability profile and message examples. |
| tests/fixtures | Terminal probes, synthetic output programs and deterministic expectations. |
| tests/integration | PTY, process lifecycle, connection loss and revocation tests. |
| packaging/macos; packaging/windows | Packaging/signing scripts and startup manifests. |
| docs | Setup, architecture, dependency record, threat model, test matrix and milestone evidence. |

Create AGENTS.md with these rules: follow this plan; use Go for all shipped host runtime code; never change public protocols silently; complete gates in order; keep terminal bytes out of React state; never send client-generated terminal-query responses to the child; never queue offline user input; ask before using missing credentials or taking unauthorized external actions; report evidence honestly; do not alter existing user files outside the approved workspace.

Implement repository-owned tasks `./scripts/check` on Unix and `./scripts/check.ps1` on Windows. Both run protocol generation drift checks, Go formatting/vet/tests, TypeScript checking, frontend unit tests and the production build. Platform integration suites are separate explicit commands. A Linux pass never implies Mac or Windows validation.

At every milestone, update docs/progress.md with completed deliverables, commands run, evidence paths, known defects, and owner-dependent blockers. Keep pending items pending. Do not spawn parallel workers unless the owner or repository instructions authorize delegation; otherwise implement sequentially. The agent configuration is chosen by the owner, not hard-coded into the application.

## 5. v0 scope and user interface

Support Apple Silicon macOS, Windows 11 x64, iPhone Safari/Home Screen installation, and Android Chrome. P0 records exact installed OS/browser versions used for evidence. Include zsh, native Windows PowerShell and available PowerShell 7; WSL bash is a separate explicit profile when WSL is installed. Never auto-install WSL or replace native Windows tests with it.

The Windows companion runs as the logged-in user. The Mac companion runs as the logged-in user. Linux may be used for CI and development, but is not a claimed public v0 host platform until its own release validation is done.

Use six screens: Setup, Sessions, Live Terminal, History/Selection, Composer, Settings. Session creation asks for a saved project and shell profile. Project roots are added locally by the owner; select them by IDs over the network rather than interpolating paths into commands. This is convenience, not a shell sandbox: the shell still has the user's filesystem permissions.

Sessions show title, project, shell, running/exited state, controller and connection state. Do not label an agent “thinking,” “awaiting approval,” or “done” based on arbitrary text. A shell process being alive is different from a child command completing.

Design: background #0B0E14, main text #D5DCE5, accent #64D8E8, restrained borders, system UI font, local/system monospace. Initial terminal font size 14 px, user-adjustable. Controls have at least 44 CSS px hit targets; visible keyboard focus and accessible names are required. No downloaded fonts, third-party scripts, gradients or animated dashboard decoration.

The live screen contains a small connection/session bar, terminal display, reachable Ctrl/Alt/Esc/Tab/arrows controls, and buttons for keyboard, composer, history and session switching. Use visualViewport and safe-area insets to keep the cursor and action bar above the mobile keyboard. Only the controller changes PTY dimensions; observers can zoom or pan without changing the application.

Use a dedicated textarea-backed input controller for mobile text/IME and hardware keyboard events. xterm is display-only with disableStdin; do not simultaneously enable xterm's input path. The input controller converts edits to structured text/key/paste events described later. Ensure each IME commit is delivered once across composition and beforeinput/input events. Disable autocorrect, smart punctuation and capitalization for live terminal input; composer behavior is explicit and independently tested.

Composer drafts live only in memory for v0 and are tied to a session generation. “Paste into terminal” is an explicit action. A separate Enter key sends submission. Multiline paste can execute commands in shells that do not support safe bracketed paste; show a clear confirmation before sending multiline content. Never promise that paste is inherently non-executing. Disconnect disables all sending and preserves the current in-memory draft; reconnect never sends it automatically.

Touch scrolling enters History/Selection mode using a consistent snapshot of normal-screen history plus the current screen. Freeze the displayed rows while the user selects text, show that new output is available, and provide Return to live. Use selectable DOM text and a virtualized list; xterm's local scrollback is disabled to avoid a second competing history. Copy uses the user's clipboard action. Alternate-screen applications keep their own live view; a dedicated application scroll action sends structured wheel events when the program supports them. Ordinary phone scrolling must not accidentally traverse shell command history.

Public v0 contains no file editor, agent-specific approvals, generated summaries, relay, SFTP, multi-user sharing, cloud execution, payment feature or native phone client. These exclusions are scope boundaries for this agent, not permanent product limitations.

## 6. Host process architecture and ownership

Build one Go executable with separate process modes: broker, gateway, and CLI/desktop attach. The persistent broker owns every PTY, every authoritative terminal emulator, authentication state, and SQLite. The gateway serves the website and translates authenticated HTTP/WebSocket requests into IPC messages. The CLI communicates over IPC. The gateway never spawns shells and never becomes the lifetime parent whose exit ends a session.

The broker is started independently at user login, with the gateway as another supervised user process. On macOS use separate LaunchAgents. On Windows use separate per-user Scheduled Tasks at logon with restart-on-failure. No SYSTEM account and no root daemon. The development harness also starts them independently so killing the gateway cannot accidentally kill its process group including the broker.

A broker failure can end all its sessions in v0. Supervision starts a new broker generation; mark previously live sessions lost. Do not auto-run previous commands. This deliberate failure boundary is documented. Per-session process isolation is not required for v0.

Use a private runtime directory owned by the current user. Unix directories are mode 0700 and sockets/database files are restricted to the user. Windows data directories and named pipes have explicit current-user ACLs. Use go-winio to implement the pipe. Reject pipe/socket access outside the intended user boundary. A process already running as that user can use the terminal; same-user malware is outside this authentication boundary.

Store persistent state under the user's application-data directory. Store runtime endpoint descriptors and PID/generation information separately. Verify a live broker by handshake, not merely by trusting a stale PID file. Enforce a single broker through an OS lock/mutex. Do not scan and kill arbitrary processes with a matching name.

Each session receives a random UUID, generation UUID, shell profile, project ID, canonical dimensions, timestamps and explicit lifecycle state: starting, running, exiting, exited or lost. Initial dimensions are 80 columns by 24 rows; allowed bounds are 20–400 columns and 5–200 rows. Default limit is eight live sessions for resource control, with validation under three simultaneously busy sessions. The limit is a documented local configuration value, not a paid tier.

Spawn executables with argument arrays. No command concatenation from titles, paths or request bodies. macOS uses the user's selected zsh login/interactive profile. Windows uses a resolved absolute shell executable. Retain necessary OS environment, including SystemRoot on Windows, and add the terminal capability environment only inside the child. Do not run agent installers automatically.

Use xpty.NewPty, Start, Resize and the package's WaitProcess lifecycle helper. Its documentation specifically distinguishes Windows ConPTY waiting from ordinary exec.Cmd.Wait. The session context survives an HTTP request, a WebSocket close and gateway cancellation. Only an explicit session termination or broker shutdown cancels it. Implement Windows termination with a broker-owned per-session Job Object and macOS process-group termination; verify descendant cleanup and avoid killing unrelated processes. ConPTY handle/pipe shutdown order must follow Microsoft's guidance and have an explicit deadlock test. [Microsoft pseudoconsole lifecycle documentation](https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session).

Ctrl+C is a terminal input event. Terminate Session is a separate explicit destructive operation that closes the managed process tree. Detach only closes the viewer. These actions must never share the same handler accidentally.

## 7. Final terminal-state approach: server-authoritative screen replication

Use a Go virtual terminal on the host as the sole interpreter of the child's terminal output. Phone and desktop clients receive normalized screen frames and send structured input. They do not receive the child's original ANSI byte stream. This removes the need to reconstruct hidden terminal parser state inside a newly connected browser.

The data flow is PTY output into the Go emulator; immutable normalized frames out to clients. Structured input enters the emulator's key/text/paste encoder; emulator-generated bytes and terminal-query responses go back to the PTY through one writer. The emulator remains running with no clients attached.

The broker is the only terminal-query responder. A child cursor-position/device/status query is interpreted there once, whether zero, one or several clients are connected. Browser xterm and the desktop terminal only see our constrained drawing instructions, never the child's query sequences. Do not use the xterm attach addon or bridge xterm.onData directly to the shell.

For each session, serialize emulator mutation and frame capture through one actor/event loop. It owns raw-output processing, resize, accepted user actions, mode callbacks and frame revisions. Concurrent goroutines read the PTY and continuously drain emulator-generated input; they communicate through bounded queues. The selected emulator uses an input pipe, so failing to drain it concurrently can block output parsing. Do not hold a lock needed by the response drain while calling emulator Write/Paste/SendKey. Verify shutdown and closed-state access with the race detector and fix any dependency issue in a documented local patch.

All PTY writes pass through one writer. Do not interleave bytes from paste, key input and automatic replies through several concurrent writes. Limit a paste to 64 KiB; reject larger pastes with an explicit message. The terminal state may generate replies during normal output handling; the reply pump must operate without any viewer and without relying on UI frame rendering.

The wire screen model contains: session/generation, revision, canonical rows/columns, active normal/alternate buffer flag, cursor position/style/visibility, resolved default colors, complete styled rows, and normal-history epoch/revision. A cell describes its display text, column width, continuation marker, resolved foreground/background and supported attributes. Include the emulator width method in screen metadata so clients do not assume a fixed Unicode mode. Coalesce equivalent adjacent styles in transport; retain exact column positions. Do not flatten the grid into an unstyled string.

The backend draws from public emulator cell/cursor/scrollback APIs and records mode/cursor callbacks. The dependency's Render method returns styled screen output; it is not a complete emulator checkpoint and must not be treated as one. Our frame is an application protocol representation generated from canonical state.

Transmit a full screen on attach or resize. During live operation, transmit changed whole rows with a baseRevision and revision. A client only applies a patch when its base matches. Missing/mismatched state requests a new full screen, never raw-output replay. Snapshot capture and its revision occur in the actor so they describe one consistent state. All frames use the current generation; obsolete frames are discarded.

Build the browser drawing adapter that turns validated cell rows into a small allowlist of drawing operations: cursor positioning, erase, SGR color/attributes, safe printable cell text, and cursor visibility/style. Disable display autowrap while painting positioned rows. Erase rows before repainting so wide-character tails cannot remain. Restore the server cursor after drawing. Never pass untrusted escape characters from cell content or labels into drawing commands. Never enable query, clipboard, title, image or device-control escapes from child output on a client.

Resolve links as sanitized metadata and allow only explicit user-opened http/https links with noopener; do not auto-open links. Unsupported visual attributes degrade in a documented way, but cursor position, columns, supported Unicode and terminal input cannot silently degrade. Fix a failing required compatibility case before progressing.

Use xterm for drawing, accessibility support and font rendering. Disable its independent scrollback and input handling. Physical-device checks must verify that xterm's character width agrees with the server for the advertised Unicode profile. Load the pinned UnicodeGraphemesAddon and include the server width method in each snapshot. Implement the adapter against its pinned public API and document the resulting width profile in protocol/v1/terminal-capabilities.md. The addon is experimental; its installation is not proof of agreement with the Go emulator. Required width fixtures must pass; if the selected renderer contract cannot represent them correctly, report the demonstrated blocker to the owner rather than concealing misalignment.

For normal-screen history, retain at most 5,000 rows and 32 MiB of normalized history payload per session, evicting oldest complete rows. Apply retention to the emulator's own scrollback as well as transport copies; an independently unbounded emulator buffer is not acceptable. Bound dimensions and per-cell content as well. Track history epoch and revision independently of live screen revision. Read history under the actor into immutable pages of at most 200 rows. If history changes between page requests, return history_changed and begin a new consistent snapshot; never splice unrelated positions. A history view is a frozen snapshot, not a log subscription. The frame protocol includes a history-reset signal when terminal output clears history.

Do not keep an unbounded raw-output log. Do not persist terminal contents to disk in v0. Reconnection restores the current screen and retained history from the live broker; it does not restore dead processes. Normal and alternate buffers stay inside the live emulator, so buffer switching after reconnect remains an ordinary terminal operation.

## 8. Input encoding, desktop attachment and controller ownership

The shared input messages are key, text, paste and mouse. Key includes a documented key enum and modifier flags; text includes committed UTF-8; paste is a distinct action; mouse includes cell coordinates, button, modifiers and event kind. No unrestricted raw terminal bytes from remote clients. Browser textarea/hardware events and desktop decoded input both map to these messages. The broker invokes the emulator's corresponding encoders so application cursor/keypad/paste/mouse modes have one authority.

Reject disallowed control characters in text; represent Enter, Backspace, Escape and other control actions as keys. Preserve Ctrl+C, Ctrl+D, Tab, Shift+Tab, navigation and tested F-keys. Enter never comes from a browser form submit side effect. The key enum is part of the versioned protocol, not platform keyCode numbers.

The desktop CLI enters an alternate screen/raw input mode, decodes input through Ultraviolet, consumes the same normalized screen frames, and restores the user's TTY on exit and signals. It does not relay outer-terminal replies to the child. Reserve Ctrl+] as the explicit detach chord, display it on attach, and provide a documented way to send a literal Ctrl+] through a CLI action. Pass OS resize events only while the CLI holds control.

Each session has one controller connection, not merely one controlling device. Multiple tabs belonging to the same device are separate connections. Other viewers are read-only. Taking control is an explicit action; the broker serially revokes the old lease, increments its epoch, grants a new unpredictable lease token and broadcasts the change. No simultaneous writers and no automatic control stealing on reconnect.

Heartbeat every 5 seconds while active; controller expires after 15 seconds without a heartbeat or immediately on explicit connection closure/revocation. Every mutating session message includes generation and controller lease. Recheck authorization and lease at the broker immediately before accepting an action. Release control on background/pagehide when observable, and also rely on expiry because mobile background events are not guaranteed.

Input uses a monotonically increasing sequence scoped to the lease. Record the accepted high-water mark before enqueueing. Reject gaps; duplicate already-accepted sequences return the same accepted acknowledgement without re-enqueueing. Accepted means received by the broker, not executed successfully by the shell. Do not claim exactly-once execution across crashes.

Never automatically retransmit user input after a connection break. If an acknowledgement is lost, show “Delivery uncertain—check the terminal before sending again.” A newly connected client starts read-only, restores state, and obtains a new lease only after an explicit Take control action. This intentionally prevents stale buffered keystrokes from entering an unrelated prompt.

## 9. Protocol and reconnect contract

Use JSON text messages over WebSocket and length-prefixed JSON over local IPC. Protocol major version is 1. Define every message in protocol/v1 before implementing a second client. JSON is the fixed v0 encoding; do not add Protobuf, a binary terminal stream or a second undocumented socket protocol.

Envelope fields: version, type, requestId, optional sessionId and generation, and payload. Screen and input sequence counters are decimal strings on the wire to avoid JavaScript integer precision loss. IDs are opaque UUIDs; credentials and leases are cryptographically random secrets, never derived from IDs.

| Client-to-host message | Required meaning |
| --- | --- |
| hello | Client build, protocol version and supported capability profile. |
| session.attach | Subscribe to one session generation as an observer. |
| screen.resync | Request a fresh normalized snapshot after a gap or invalid base revision. |
| snapshot.applied | Confirm a complete validated snapshot has reached the display. |
| control.acquire / control.release | Explicit request to take or release the single controller lease. |
| heartbeat | Renew an active connection/controller according to broker policy. |
| input.key / input.text / input.paste / input.mouse | Typed, bounded user actions with generation, lease and input sequence. |
| terminal.resize | Controller's measured grid dimensions with generation and lease. |
| history.read | Read an immutable history page for an epoch/revision. |
| session.detach | Stop viewing without ending the process. |

| Host-to-client message | Required meaning |
| --- | --- |
| welcome | Host/broker generation, negotiated protocol and limits. |
| snapshot.begin / snapshot.rows / snapshot.end | One complete screen at a specified revision; chunks do not become visible as a partial new state. |
| screen.patch | Changed rows and metadata, with exact baseRevision. |
| history.page / history.changed | Bounded history data or invalidated pagination. |
| control.changed | Current controller and lease epoch; secret lease only goes to its holder. |
| input.accepted / input.rejected | Acceptance state, never an execution claim. |
| session.state | Starting/running/exiting/exited/lost state and exit code when available. |
| access.revoked / error | Stop access or report a typed, user-understandable error. |

Initial HTTP surface: GET /healthz with minimal non-sensitive status; POST /api/v1/pair/redeem; POST /api/v1/pair/poll; GET /api/v1/sessions; POST /api/v1/sessions; PATCH /api/v1/sessions/{id}; POST /api/v1/sessions/{id}/terminate; POST /api/v1/logout; GET /api/v1/settings; and authenticated /api/v1/ws. Session creation, renaming and termination require unique operation IDs deduplicated by the broker. Pairing grants, device approval/revocation, project registration and startup configuration are local CLI actions in v0; do not accidentally expose administrative routes to unpaired browsers.

Define stable error codes: unauthorized, pairing_expired, pairing_pending, revoked, protocol_mismatch, session_missing, generation_changed, session_lost, not_controller, stale_lease, input_gap, limit_exceeded, history_changed, resync_required, host_unavailable and internal_error. Do not send stack traces to the phone.

Production HTTP gateway binds only 127.0.0.1:8787. Tailscale Serve proxies the approved private HTTPS origin to it. Do not listen on 0.0.0.0 as a convenience. Browser and API are same-origin. Validate Host against the configured private hostname and the deliberately configured local development hostname; ignore externally supplied forwarded-user identity headers. Tailscale connectivity is not a replacement for application authentication.

Reconnect steps: stop outgoing input; establish an authenticated WebSocket; negotiate version; attach as observer; receive a snapshot transaction at revision N; validate and paint it; acknowledge applied; process subsequent patches; then offer Take control. A gateway disconnect never deletes the session. A changed broker/session generation gives a clear lost-session screen and a separate Start new session action.

Capture snapshots without stopping a running child for the duration of network transfer. Copy immutable state under the actor, then transmit outside it. Publish patches after that snapshot boundary. If later changes exceed the bounded window before the snapshot completes, abandon that transmission and send a newer snapshot. Never apply a patch to a partially painted screen.

Limits: input text/paste payload at most 64 KiB UTF-8; inbound WebSocket messages at most 256 KiB after JSON encoding; outbound messages at most 1 MiB, splitting snapshots into row chunks. IPC has the same chunking constraints. Per-client outgoing queue budget is 2 MiB. Rate-limit abusive connection, pairing and input traffic; report rejected input rather than silently dropping it. Keep a bounded PTY-output parsing queue independent of viewers.

Prefer at most one current pending screen update per lagging viewer; coalesce by generating a new full screen when necessary. Never discard raw bytes before the canonical emulator has interpreted them. If a viewer cannot consume updates, disconnect that viewer and let it reconnect. A slow phone must not indefinitely backpressure the child process. Do not impose an unbounded disk spool to conceal the problem.

Publish frames at up to 60 Hz when output changes, with at most one render queued per browser animation frame. React owns navigation and settings; a terminal controller owns frame application and xterm writes. Parsing the full history on each update is prohibited.

## 10. Pairing, authentication, storage and network setup

The initial local command is pterm setup. It checks broker/gateway health, Tailscale status, the configured private hostname, available shell profiles and permissions. It describes startup changes before performing them. All production phone access uses the private HTTPS hostname. Tailscale login and Serve/HTTPS changes require the owner's earlier authorization; preserve existing Serve configuration and report a conflict instead of overwriting another service. Never enable Tailscale Funnel. [Tailscale Serve reference](https://tailscale.com/docs/reference/tailscale-cli/serve).

Local pterm pair creates a 256-bit random grant expiring after two minutes and displays a QR/link with the secret in the URL fragment. The page removes the fragment from history immediately and sends the secret in a POST body over HTTPS. Store only the grant hash. Redemption is atomic and single-use. It creates a pending device with a separate random poll secret and a short verification code shown on phone and local CLI.

The owner compares the code and executes pterm pair approve <pending-id> locally. A verification code alone does not grant access. The pending browser polls with its secret; on approval the server sets the authenticated cookie. No reusable access credential appears in URLs, terminal logs or browser localStorage. Reject expired/redeemed grants, rate-limit failed attempts and clear secrets from in-memory UI state when done.

Production cookie: __Host-pterm_session, Secure, HttpOnly, SameSite=Strict, Path=/ and no Domain. Browser tokens are 256-bit random values; SQLite stores their SHA-256 hashes, creation, last use, absolute expiry and revoked state. Use a 30-day absolute lifetime and seven-day inactivity expiry. Expired clients pair again. A device label is user-controlled display text, not identity.

Validate exact Origin on state-changing HTTP requests and WebSocket upgrades. Reject missing/mismatched browser origins on these endpoints. Require JSON content type and a fixed application request header on HTTP mutations. Do not enable wildcard CORS or disable coder/websocket origin checks. Local CLI requests use restricted IPC and do not reuse browser-origin rules.

Revocation is authoritative at the broker. It invalidates the credential and device leases, drops that device's user-action messages still waiting in the actor queue, and instructs all connected gateways to close its sockets. Bytes already encoded into the PTY writer or written to the child are in flight and cannot be reliably retracted. Input authorization remains checked at the broker so a delayed gateway notification cannot permit further accepted commands. Already-written shell input cannot be undone; do not imply otherwise.

SQLite tables: schema_migrations; devices; browser_sessions; pairing_grants; pending_pairings; projects; shell_profiles; session_metadata; settings. Store credential hashes, never provider credentials or raw terminal data. Process handles and active controllers are in-memory broker state. Use foreign keys, transactions and prepared statements. Configure WAL and a bounded busy timeout; only the broker writes. Migration failure leaves the previous database intact and prevents startup with an actionable error.

Terminal output is untrusted. Do not use innerHTML for titles, history, clipboard previews, errors or links. Disable automatic OSC clipboard writes, remote clipboard reads, terminal-image protocols, automatic notifications and URL launching. No analytics, advertisements, remote scripts or third-party fonts on authenticated terminal pages. Ship a restrictive CSP and a no-referrer policy. The terminal page's script supply chain has access to terminal control and must stay small. [xterm.js security guidance](https://xtermjs.org/docs/guides/security/).

Operational logs include bounded event names, timestamps, non-secret IDs and error codes. Exclude input, output, environment variables, tokens, query strings and pairing secrets. Logging terminal transcripts is not a v0 feature. Diagnostics export requires a user action and shows a preview before writing a local file.

Use an isolated development profile for loopback HTTP tests: different data directory, cookie name and credentials, enabled only by an explicit --dev flag. Do not auto-enable it when TLS or Tailscale fails. It binds loopback and does not accept external forwarded origins. Real phone tests use production HTTPS.

## 11. CLI contract and installation

| Command | Behavior |
| --- | --- |
| pterm setup | Guided checks and explicitly authorized per-user startup/network configuration. |
| pterm doctor | Read-only diagnostics; never prints secrets. |
| pterm project add <path> | Register a local project root after validating it exists. |
| pterm new --project <id> --shell <profile> | Create a managed session and attach locally. |
| pterm attach <session-id> | Observe an existing session; explicit control takeover if another client controls it. |
| pterm list | Show sessions and lifecycle status. |
| pterm pair | Produce the expiring phone pairing link/QR. |
| pterm pair approve <pending-id> | Approve the matching verification code from trusted local IPC. |
| pterm devices | List paired device names, status and last use. |
| pterm revoke <device-id> | Revoke browser access and close connections. |
| pterm terminate <session-id> | Explicitly end a selected session after confirmation. |
| pterm stop | Stop the gateway only; leave broker sessions alive and say so. |
| pterm shutdown | Confirm, end all sessions and stop broker/gateway. |
| pterm uninstall | Explain and remove registered startup/install files; offer explicit data deletion separately. |

Internal broker/gateway modes are not user-facing setup choices. The installed binary embeds the website and requires no Node installation on the user's computer. Target CGO_ENABLED=0 for release runtime builds; OS-specific startup/signing tools may still be required on build machines. Go race tests use the appropriate supported toolchain even when release binaries are built without cgo.

macOS release: a signed/notarized .app in a DMG. Its Go launcher starts or checks the user processes and opens the setup/status webpage; bundle the CLI executable and an explicit user-level CLI installation action. No system-wide daemon. Startup registration is per-user and requested by the setup flow. Prepare unsigned local artifacts first, then sign/notarize only with the configured identities and permission. [Apple notarization documentation](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution).

Windows release: a per-user Inno Setup installer containing pterm.exe, shortcuts, uninstaller and per-user startup registration. Install under LocalAppData; use the user's PATH only with explicit installer consent. Sign the executable and installer with SignTool using the owner's trusted code-signing identity. Do not disable Defender, SmartScreen or certificate checks to make a release look ready. [Inno Setup documentation](https://jrsoftware.org/isinfo.php).

The GUI launcher opens an already paired browser or displays setup instructions. Local administration remains in the CLI for v0; do not add an unprotected localhost admin endpoint. A user can perform first pairing from the computer browser through the same grant/approval flow. Do not treat a request as authenticated solely because it came from loopback.

Manual signed updates are the fixed v0 approach. Stage the new executable in a versioned directory. If the broker has active sessions, refuse to replace/restart it and explain that those sessions must first finish or be explicitly terminated. Do not promise zero-downtime broker upgrades. Keep the previous signed version available for rollback; migration backup/rollback compatibility must be tested. No updater downloads executable code at startup in v0.

The PWA manifest and icons are bundled. The service worker caches immutable versioned application assets only, never API responses, terminal frames, secrets or history. Offline opens show connection unavailable. Do not claim a disconnected phone can execute commands. A waiting web update requires explicit user reload; do not force service-worker activation during a live session.

## 12. Compatibility profile and required test fixtures

Publish protocol/v1/terminal-capabilities.md. Use TERM=xterm-256color and COLORTERM=truecolor on supported Unix shells, and verify the advertised baseline. Support ordinary ANSI/VT cursor movement, erase, scrolling regions, SGR colors/styles, normal/alternate screen, cursor visibility, device/status responses needed by the tested programs, bracketed paste, application cursor keys, and the selected mouse modes.

The Go emulator determines capability replies. Do not advertise extensions because the browser's xterm happens to support them. Unsupported image/clipboard extensions remain disabled. Run deterministic fixtures that compare canonical state to expected results; do not rely solely on screenshots or one real agent session.

Required fixture families:

1. Split UTF-8 bytes and split escape sequences at every relevant read boundary; combine chunks differently and assert equivalent state.
2. Cursor save/restore, scrolling margins, erase, insert/delete cells, colors, reverse/bold/underline, full right/bottom boundary behavior.
3. Normal-to-alternate-to-normal transitions during detach, reconnect and resize; no history contamination or lost saved screen.
4. CJK, combining accents, surrogate-requiring characters, emoji variation selectors and ZWJ sequences; server columns and rendered browser cells must agree for the supported profile.
5. Device/cursor queries with zero, one and two viewers; capture child input and assert one correct response per query.
6. Bracketed paste on/off, literal control characters, multiline composer warnings, Ctrl+C and Ctrl+D, application cursor/keypad behavior.
7. Output flood with a stalled viewer and with no viewers; child progresses, memory is bounded, canonical state remains valid.
8. Snapshot chunk interruption, wrong base revision, changed generation, stale controller lease, duplicate and missing input sequences.
9. Revocation, expired/redeemed grant, CSRF/Origin/Host mismatch, token leakage checks, malformed JSON, excessive dimensions and oversized payloads.
10. Gateway process kill/restart, broker restart, explicit detach, forced termination and orphan descendant cleanup on both platforms.
11. Shell argument/path quoting with spaces, Unicode and shell metacharacters; input must never be reinterpreted as installer or launcher arguments.
12. WebGL context loss, font loading/layout resize, phone rotation and keyboard show/hide without losing terminal alignment.

Run real applications after fixtures: zsh interactive editing, a pager, a full-screen editor, Windows PowerShell editing, and an owner-authorized coding CLI. Use disposable repositories for commands that modify files. Record exact CLI versions and test account consent. No automatic model-provider signup or API-key collection.

Physical iPhone tests: keyboard composition, dictation when available, paste, selection handles, a long draft, background/foreground, screen lock, Wi-Fi-to-cellular transition, orientation and Home Screen mode. Repeat essential flows on Android. The owner can perform these with a written checklist and screen recording; mark evidence as owner-run.

A browser reconnection cannot be promised while the OS suspends the browser. Measure recovery after the app is foregrounded and network reachability returns. The host continues independently.

## 13. Performance, soak tests and evidence

These are proposed acceptance targets, not measured achievements. Record hardware, OS, browser, shell, dimensions, output rate, network latency and exact build before reporting a result.

| Measurement | v0 acceptance target |
| --- | --- |
| App-added key-to-frame overhead | p95 under 30 ms in the controlled local benchmark, reported separately from network round-trip time. |
| Foreground reconnect | p95 under two seconds from reachable authenticated connection to usable current screen at 120x40; history loads separately. |
| Normal phone interaction | No persistent visible lag during typing and no lost/double input in the required device matrix. |
| Stress output | One session emits a sustained 1 MiB/s synthetic stream for ten minutes while two other sessions remain interactive; then screen resynchronization succeeds. |
| Soak | 24-hour three-session run with repeated phone/gateway reconnects; no orphan growth or unbounded queue/history growth. |
| Resource budget | Report broker and gateway RSS/CPU at idle, ordinary use and stress. For the default three-session test, target combined RSS under 300 MiB excluding child programs; investigate and fix sustained growth rather than hiding it by restarting. |

For latency measurement use a deterministic child echo probe with request IDs. Measure host receive-to-frame time using one host clock and browser send-to-paint using one browser clock; estimate/report transport separately rather than subtracting unsynchronized timestamps. Use at least 1,000 events for the local p95 claim. Mobile performance measurements require a real phone, not only Playwright.

Store evidence under docs/evidence/<milestone>/ with commands, timestamps, environment, expected/actual outcomes and defect links. Never attach raw production terminal history or credentials. A screenshot alone does not establish process persistence; verify child identity/generation and continuing output. A test that could not run is blocked, not passed.

## 14. Ordered implementation milestones

Complete the deliverables and exit criteria of each milestone before dependent work. Do not spend time polishing the dashboard while terminal correctness remains unproven. Independent documentation, fixtures and packaging preparation may proceed while owner-dependent access is pending.

| Milestone | Exact work | Exit criterion |
| --- | --- | --- |
| P0 — Private repository, preflight and scaffold | Verify GitHub account/target; create and verify the private repository; record access blockers; initialize main and its bootstrap commit; add the OSS preparation files; configure the PR/CI workflow; pin dependencies and dev commands. | Repository is verified private; the bootstrap commit is pushed privately; repository permissions and CI are recorded; credential/access blockers are identified. P1 starts only after this gate. |
| P1 — Go terminal core | Wrap xpty and vt; build a Go fixture child; implement canonical state and one response writer; native Mac and Windows process lifecycle. | Required parser/query/resize/Unicode fixtures pass on both hosts, including no-viewer operation. No browser UI is required yet. |
| P2 — Frame and input protocol | Write schemas, generate types, implement normalized snapshots/patches, revision rules and structured input. Build a minimal xterm display adapter and desktop attach client. | Full-screen fixture matches canonical state after reconnect/resize; one response per query; desktop and browser input never compete. |
| P3 — Persistent broker | Separate broker/gateway processes, user IPC, session registry, metadata, controller lease, sequence handling and history limits. | Gateway restart preserves child identity; broker restart reports lost; stale input is rejected; bounded memory under a stalled viewer. |
| P4 — Authentication and private access | Pairing/revocation, cookie/Origin rules, local project registration, Tailscale setup/status integration. | Owner-authorized real phone connection over cellular; revoked phone cannot accept new input; pairing cannot be replayed. |
| P5 — Phone experience | Implement the six screens, mobile input controller, shortcut row, composer, history selection, connection UX and accessibility. | Physical iPhone and Android input/selection/background tests pass; no desktop-layout squeeze or hidden controls. |
| P6 — Install and update | Implement macOS/Windows packaging, user startup, doctor, uninstall, staged manual update and signing scripts. | Fresh machines run without Node/Go/compiler installation; updates respect active sessions; unsigned/test and signed-release states are distinguishable. |
| P7 — Reliability and review | Full test matrix, rate limits, security cases, stress/soak, source review and release documentation. | Required tests have evidence; known defects are fixed or explicitly block release. No silent scope removal. |
| P8 — Release preparation | Assemble signed artifacts when credentials are authorized, checksums, notices, README, support matrix, limitations and demo. | Complete reviewable release package; ask for publication permission if not already granted. |

P1/P2 can reveal a bug or missing capability in the pinned Go terminal library. The fixed response is to reproduce it, add a failing fixture, inspect upstream behavior and implement a minimal documented adapter/dependency patch when feasible. If a required capability would demand a material architecture change, stop that dependent milestone and present the evidence plus a concrete proposed amendment to the owner. Do not silently switch backend languages, add a Node sidecar, remove Windows, remove desktop attachment, or fake a reconnect snapshot. “Final approach” is an implementation direction, not permission to claim an unproven dependency works.

## 15. Agent task prompts and review instructions

Opening prompt to paste with this file:

> Read phone-terminal-final-plan.md completely and treat it as the authoritative implementation specification. Follow repository instructions and preserve unrelated work. First verify the GitHub identity and create the intended repository explicitly private, following sections 17–20. Confirm private visibility before pushing. Commit coherent changes as you work, push to private feature branches, use PRs and CI, and never change repository visibility to public; Anurag will do that manually. Produce the preflight access report and ask Anurag about missing credentials, platform access or permissions before the dependent action. Continue independent local work. Implement P0, then the ordered milestones, using the fixed Go backend, server-authoritative terminal frames and TypeScript interface. Do not reopen stack selection, add a cloud dependency or silently weaken a requirement. Record verifiable evidence at each gate. Never claim a platform, security or real-phone test passed unless it was actually performed. Prepare reviewable artifacts before asking for any external publication or privileged installation action.

P1 implementation prompt:

> Implement internal/ptyhost and internal/terminal using the pinned xpty and vt dependencies. Build a deterministic Go child fixture that emits terminal sequences and captures responses. Verify Windows process waiting, pipe shutdown, query-response uniqueness without clients, split sequence parsing, Unicode boundaries, alternate buffers and resize. Keep the emulator owned by one actor and continuously drain its response pipe. Add bounded queues and cancellation tests. Produce a source-level explanation of process lifetime and actual host test results. No polished UI, authentication bypass or Node runtime.

P2 implementation prompt:

> Implement protocol/v1 and both frame clients. Transmit normalized rows and metadata, never child ANSI output. Implement a display-only xterm adapter, structured browser input and the Go desktop attach client. Prove that full snapshot plus patches equals canonical state after interrupted attach, resize and buffer switching. Ensure server terminal queries have one responder. Verify the width profile with CJK, combining marks and emoji. Do not treat vt.Render as a serialized emulator checkpoint.

P4 implementation prompt:

> Implement two-step local-approved phone pairing, hashed random credentials, strict cookie/Origin/Host checks, broker-authoritative revocation and private Tailscale Serve access. Ask before configuring the owner's Tailscale account or host startup. Test replayed grants, expired grants, forged origins, revoked active sockets and delayed gateway revocation. Never introduce a public tunnel or global auth-disable flag.

P5 implementation prompt:

> Build the six specified phone screens and dedicated textarea input controller. Keep xterm input disabled. Validate IME deduplication, multiline paste, Ctrl/Esc/arrows, selection freeze, return-to-live, safe-area layout and visible disconnection state. Keep drafts local in memory and never replay input on reconnect. Supply the real-phone checklist and record who executed each test. Browser emulation is supplemental evidence.

Independent review pass, performed sequentially if delegation is not authorized:

> Review the completed milestone against this plan. Attempt to find a second terminal-query responder, a gateway-owned shell lifetime, stale controller input, duplicated Enter/paste, an unbounded buffer, a credential in a URL/log, a trust boundary based only on localhost, a false Windows pass from WSL, or a fake restore that loses alternate-screen state. Give reproduction steps for demonstrated defects. List untested claims separately. Do not expand product scope during review.

## 16. Definition of done and delivery report

A public v0 also requires Anurag's manual private-to-public transition in section 20. Passing technical checks does not authorize that transition. A public v0 is complete only when it has a functioning Go runtime on both target host platforms, real phone access over private HTTPS, multiple sessions, desktop handoff, correct normalized terminal display, structured input, bounded history, pairing/revocation, reconnect behavior, signed installers, clean uninstall, meaningful test evidence and clear documentation of host-awake/broker-lifetime requirements.

The delivery report must list: implemented behavior; exact artifacts and build identifiers; supported/tested OS/browser/shell versions; tests actually run and their results; unresolved defects or owner-dependent gates; credential locations by name only; installation/uninstall steps; and any external action still awaiting explicit authorization.

If signing credentials are missing, deliver unsigned development artifacts and completed signing scripts labeled accurately. Do not call the public release complete. If a target device is unavailable, provide its checklist and leave that gate blocked. Do not hide either gap behind a general “all tests passed.”

Future work is reserved, not authorized by this v0 assignment: Go relay with reviewed end-to-end encryption; structured agent integration; diffs and project previews; multiple computers; public SDK packaging; native iOS/Android clients. Preserve the versioned session protocol so those additions do not require replacing the Go foundation. Finish v0 before starting those features.

## 17. Private repository first: mandatory startup sequence

This is a plan for a fresh private build that will later be open-sourced. It is not authorization to change any repository's visibility now. At implementation time, the owner's private-first instruction authorizes creating the intended new private repository after verifying the account and target. Do not repeatedly ask for permission already granted; ask for missing access, a conflicting existing repository, or a different owner.

1. Inspect the supplied workspace and Git configuration. Preserve unrelated changes. Verify authenticated GitHub identity and authorization without printing tokens. Default intended repository: intojhanurag/phone-terminal. Do not create it under a company organization or a different personal account.
2. Check whether the repository already exists. Reuse it only if it is the intended private project. If it is public, unrelated, or ownership/access is uncertain, stop repository mutations and ask the owner. Do not delete, overwrite, rename or change an existing repository's visibility as a shortcut.
3. Create the new repository with an explicit private-visibility argument. Do not rely on an account default. Verify private status from the GitHub API before the first push and record the owner/name/visibility in docs/progress.md.
4. Clone that private repository into the approved workspace, set main as the default branch, and use the confirmed GitHub module path. Create the baseline documentation/configuration commit before implementation commits.
5. Add the files in section 19, the authoritative plan at docs/implementation-plan.md, and AGENTS.md. Include .gitignore before staging. Exclude secrets, local runtime state, terminal transcripts, generated installers and private test data.
6. Run a staged-file and secret review, commit the bootstrap, and push to the verified private origin. Record the commit SHA. Configure branch/CI settings as described below, checking which controls the owner's GitHub plan supports.
7. Start P1 on a feature branch. Repository visibility must remain private throughout implementation, testing, draft releases and preparation.

If repository authentication is missing, request an interactive GitHub login or an approved scoped integration. Never request a pasted password or broad personal token. Until the private repository is available, prepare the access report and bootstrap material locally, but do not begin untracked application implementation or push anywhere else.

Keep all code, issue discussions, PRs, release drafts, screenshots and CI artifacts inside the approved private project. Do not upload the source to public package registries, public container registries, public gists, public preview sites or third-party analysis services. Private GitHub visibility does not automatically make every separately hosted artifact private. Do not add collaborators, GitHub Apps, external bots or webhooks without existing authorization.

## 18. Commit, branch, PR and CI rules

Commit continuously at coherent, verified checkpoints. Do not wait until the end to create one giant commit. Examples of separate commits: protocol schema and generation tests; PTY lifecycle implementation and its tests; pairing flow and its tests; a documented bug fix. A commit should explain one change and why it exists. Finish a milestone with its evidence and documentation committed.

Use Conventional Commit prefixes with meaningful scopes: feat(terminal), fix(reconnect), test(pty), docs(setup), build(packaging), ci and chore(deps). Do not use messages such as “updates,” “done,” or an uninformative file list. Do not split a single inseparable change into broken intermediate commits just to increase commit count.

Before every commit, inspect git status and the staged diff, run the relevant checks, and stage explicit paths belonging to the task. Review staged additions for tokens, .env files, signing material, personal paths, model logs, terminal output and binaries. Never stage all unrelated workspace changes. Use the configured author identity; if it is absent, ask for the intended identity. Do not invent identities, approvals or co-author/sign-off attestations. Preserve configured commit signing; ask if its key is unavailable instead of silently disabling signing.

Use one branch per reviewable milestone or subtask, named feat/p1-terminal-core, fix/p2-resize-state, or an equivalent descriptive name. Keep main buildable. After the initial bootstrap, all application changes reach main through PRs. Push verified checkpoints to the private remote and open a PR with problem, implementation, test evidence, known limitations and the associated milestone. Verify the push destination and private visibility before the first push to a newly configured remote.

Routine commits, private branch pushes and private PR integration are part of the authorized implementation workflow once the target repository is established. The agent may squash-merge a private PR after its required checks pass, a documented review pass is complete, and actual repository rules permit it. Do not request fresh permission for each routine commit or merge. Honor any user-imposed review requirement and never bypass a required human approval. Do not enable unattended repository-wide auto-merge or invent a second review identity. Same-account authoring/review must be transparent; report a self-review as such.

Configure main to require PRs, the actual named CI checks, resolved review conversations and protection against force pushes/deletion. Use squash merge for a clear main history, retaining detailed working commits on the private feature branch/PR. Do not require an impossible second-person approval when the only account available is the owner authoring the PR through an agent; record the review evidence in the PR and honor any additional owner review requirement. Add CODEOWNERS for routing, not to manufacture an approval.

GitHub branch-protection availability in private repositories depends on the account plan. Verify support during P0. If an enforcement setting is unavailable, report it and ask the owner for the required account access/upgrade before calling repository hardening complete. Continue branch-based implementation and manual CI review privately while that setting is blocked; do not make the repository public to unlock the feature. [GitHub protected-branch documentation](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches).

Required CI checks: protocol generation drift; Go format/vet/unit tests; Go race-enabled tests on supported runners; TypeScript checking and web unit tests; production web build; macOS PTY/lifecycle integration; Windows-native PTY/lifecycle integration; release-build smoke checks; secret scanning; dependency vulnerability scanning. Use stable unique check names so branch rules match them. A physical-phone gate remains recorded manual evidence and cannot be replaced with a green CI badge.

Use GitHub Actions with read-only contents permission by default. Pin external actions to immutable commit SHAs, use ordinary pull_request workflows for untrusted code, and never run an untrusted checkout under pull_request_target with secrets. Signing runs only in a manually approved release environment with minimal permissions; normal PR jobs receive no signing/provider credentials. Never execute an untrusted PR on the owner's personal workstation runner. [GitHub Actions secure-use reference](https://docs.github.com/en/actions/reference/security/secure-use).

Use Gitleaks for local/staged and repository-history secret scans, govulncheck for Go, and pnpm audit for JavaScript dependency reporting. These tools reduce risk but do not prove the absence of secrets or vulnerabilities. Pin the scanner versions and document any reviewed finding with rationale and owner-visible expiry; do not blanket-disable checks. Add a Dependabot configuration for Go modules, npm/pnpm and GitHub Actions; upgrades arrive as reviewed PRs, never silent automatic merges.

Do not force-push shared branches, reset another person's work, rewrite published history or disable checks to obtain a green result. Resolve conflicts deliberately and rerun affected checks. If a necessary repair changes shared history, describe the exact change and obtain the owner's authorization first.

At the end of every agent work session, report the current branch, latest commit SHA, whether it is pushed, PR status, checks run, uncommitted task work and remaining blockers. Preserve unrelated user changes; a “clean tree” requirement is not permission to discard them. Do not tag or publish a release merely because a milestone finished.

## 19. Open-source preparation from the first private commit

Build as an open-source project while keeping distribution private. The repository must contain the following files, with truthful content updated as implementation progresses:

| File | Required content |
| --- | --- |
| README.md | Product purpose, private-development status, architecture overview, actual support matrix, setup/build/test instructions, honest limitations and screenshots from synthetic data. Clearly mark features that are not implemented yet. |
| LICENSE | Apache-2.0 text for original code. A license file does not change GitHub visibility. |
| THIRD_PARTY_NOTICES.md | Included dependencies, applicable licenses, required notices and attribution. Preserve upstream notices in distributions; include NOTICE where an included license/notice requires it. |
| CONTRIBUTING.md | Local setup, code layout, dev commands, tests, branching/PR rules, issue reporting, dependency changes and contribution licensing. Contributions are under the project license; do not introduce a CLA service or fabricated sign-offs. |
| CODE_OF_CONDUCT.md | Contributor Covenant with a real owner-approved private contact before public release. No invented email address. |
| SECURITY.md | Supported release status and private vulnerability reporting instructions. No public issue reports containing exploit details or secrets. Confirm the real reporting route before public release. |
| CHANGELOG.md | Unreleased changes grouped by user-visible impact; later released versions with accurate dates and links. |
| .github/CODEOWNERS | Verified maintainer identity for important paths, including protocol, authentication and release workflows. |
| .github/ISSUE_TEMPLATE | Bug report and feature request forms asking for sanitized reproduction steps and versions, never credentials or raw personal terminal history. |
| .github/pull_request_template.md | Problem, behavior change, validation, compatibility impact and documentation requirements. |
| .github/dependabot.yml | Reviewed dependency-update schedule for the actual ecosystem manifests and Actions. |
| .editorconfig; .gitignore; .gitattributes | Consistent formatting/line endings, Go and web build exclusions, runtime/database/log exclusions and secret/signing exclusions. Use .env.example only with harmless dummy values if configuration needs it. |
| AGENTS.md | The implementation, credential, commit, visibility and evidence rules from this plan. |
| docs/architecture.md | Runtime/process ownership, trust boundaries and versioned protocol design. |
| docs/decisions | Short decision records for the fixed Go/TypeScript stack, server-authoritative terminal model, networking and persistence boundaries. |
| docs/implementation-plan.md; docs/progress.md | This authoritative plan plus milestone status and actual evidence. |
| docs/release-checklist.md | Section 20, signing, install/uninstall validation, known limitations and explicit owner approval. |

Track milestones and concrete defects in the private repository. Each implementation task has a clear outcome and acceptance criteria; each PR links to the corresponding task. Avoid manufacturing large numbers of empty issues. Do not automatically assign or notify outside collaborators.

Prepare a dependency inventory/SBOM for release artifacts and include checksums and exact source commit IDs. Version initial public release as v0.1.0 after it passes the v0 acceptance contract; use documented prerelease identifiers for private candidates. Do not imply version 1 stability. Signing keys and certificates remain outside Git, including when the repository is private.

If a code-of-conduct contact, vulnerability reporting address, publisher identity or other owner-controlled detail is missing, request that exact detail before public release. Keep an explicit private-development placeholder rather than inventing a contact. This does not block terminal implementation.

## 20. Private-to-public transition: separate owner decision

Completing development must never automatically make the repository public. Anurag will manually change visibility himself when ready. The agent prepares a complete release review package and a concise manual handoff; it must not run a visibility-changing command or offer to perform that step on his behalf. Keep packages unpublished and release material in private drafts until separately authorized publication actions are requested. Anurag making the repository public does not automatically authorize package or installer publication.

Before handing the project to Anurag for his manual public transition:

1. Confirm all technical gates, signed installer availability, platform evidence and unresolved limitations. Supply the private release candidate, source commit and reproducible build instructions for owner review.
2. Review all tracked files and full reachable Git history, including branches and tags, for credentials, private identifiers, employer/customer code, terminal transcripts and confidential material. Review PR/issue discussions, release drafts, screenshots, Actions logs and artifacts too. GitHub warns that Actions history and logs become visible when the repository becomes public. [GitHub visibility documentation](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/managing-repository-settings/setting-repository-visibility).
3. If a real credential was committed, revoke/rotate it first. Deleting its current file is insufficient. Prepare any required history/log/artifact cleanup and obtain permission before destructive shared-history changes. Do not publish until the incident is resolved; do not create a fresh public repository merely to hide unresolved exposure.
4. Verify license/attribution obligations, genuine maintainer contacts, contribution/security documents, install steps, support matrix, changelog, SBOM and checksums. Confirm public branding/package identifiers with the owner.
5. Review workflow permissions and behavior for public fork PRs. Confirm no private source or credentials will be exposed by inherited CI, deployment hooks, external bots or test artifacts.
6. Present a concrete handoff identifying the private repository, release candidate commit, proposed release tag, exact artifacts and remaining checklist items. Tell Anurag the project is ready for his manual visibility change only when the evidence supports it. Do not change visibility. Ask separately before any package, installer or release publication that is not already authorized.

After Anurag reports that he has manually made the repository public, verify visibility with a read-only check. Inspect effective branch protections/rulesets and workflow permissions after the transition. Perform separately authorized release publication actions only, and verify their artifacts. Report exactly what is public and what remains unpublished. Repository visibility changes can affect GitHub controls; pre-transition settings are not evidence that post-transition protections still apply.

Keep the private-development history unless the owner approves a specific cleanup. Do not promise that making a public repository private later can retract copies or forks. Subsequent releases continue through the same review, test, signing and approved publication workflow.
