# Installing Termivoa

> Status: **private beta** (`v0.1.0-beta.3`). The binaries are **unsigned**.
> Signed and notarized releases need the owner's Apple Developer ID and Windows
> code-signing identity. Nothing below has passed the release gate yet. You can
> also build from source ([README: build from source](../README.md#build-from-source)) or
> build the artifacts with [`packaging/macos`](../packaging/macos/README.md),
> [`packaging/windows`](../packaging/windows/README.md) and
> [`packaging/linux/build.sh`](../packaging/linux/build.sh). Linux support is
> written and tested in containers, but not yet on a real Linux computer. The
> short version of this guide is the
> [quick start](https://termivoa.com/docs/quick-start/).

## Private beta

The beta is a prerelease in the private repository, so only invited
collaborators can download it. Use the [GitHub CLI](https://cli.github.com/)
(`gh auth login` first). Files that `gh` downloads get no "downloaded from the
internet" mark, so macOS and Windows do not block the unsigned binary. A
browser download does get the mark: macOS then refuses to open it and Windows
shows SmartScreen.

You also need [Tailscale](https://tailscale.com/download) on the computer and
the phone, signed in to the same account.

macOS (Apple Silicon):

```sh
mkdir -p ~/.local/bin
gh release download v0.1.0-beta.3 -R intojhanurag/Termivoa -p pterm-darwin-arm64 -O ~/.local/bin/pterm
chmod +x ~/.local/bin/pterm
~/.local/bin/pterm setup
```

Linux (x86_64; use `pterm-linux-arm64` on arm64):

```sh
gh release download v0.1.0-beta.3 -R intojhanurag/Termivoa -p pterm-linux-amd64 -p install-linux.sh
sh install-linux.sh ./pterm-linux-amd64
```

Windows 11 (x64, PowerShell):

```powershell
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\Programs\Termivoa" | Out-Null
cd "$env:LOCALAPPDATA\Programs\Termivoa"
gh release download v0.1.0-beta.3 -R intojhanurag/Termivoa -p pterm-windows-amd64.exe -O pterm.exe
.\pterm.exe setup
```

Keep the executable where it is after setup: the startup entries point to its
path. `SHA256SUMS` in the release lists the checksum of every file
(`shasum -a 256 -c SHA256SUMS --ignore-missing`). Then continue with
[Pair your phone](https://termivoa.com/docs/pair-your-phone/).

Termivoa runs two per-user processes from one `pterm` executable:

- the **broker** owns every terminal session, the terminal state and the database;
- the **gateway** serves the web page on `127.0.0.1:8787` and talks to the broker.

They start separately at login (see the startup table below for restarts
and logs). Neither runs as root,
SYSTEM or an administrator. No Node.js, Go or compiler is needed on the host.

## Install

### macOS (Apple Silicon)

1. Open `Termivoa-<version>-macos-arm64.dmg`.
2. Drag **Termivoa.app** to **Applications**. Do not run it from the
   disk image: `pterm setup` refuses a pterm on a mounted image or in
   App Translocation, because that path disappears later.
3. Optional, for the CLI on your PATH (you do this; the app never changes PATH):

   ```sh
   mkdir -p ~/.local/bin
   ln -s "/Applications/Termivoa.app/Contents/MacOS/pterm" ~/.local/bin/pterm
   ```

   The rest of this guide writes `pterm` for that path.

Unsigned development builds are blocked by Gatekeeper when downloaded. Signed
and notarized builds are pending owner signing.

### Windows 11 (x64)

1. Run `Termivoa-<version>-windows-x64-setup.exe`. It needs no
   administrator rights and installs to
   `%LOCALAPPDATA%\Programs\Phone Terminal\versions\<version>\`.
2. Choose tasks:
   - **Start Termivoa when I sign in** (checked): runs
     `pterm setup --apply --yes`, which registers two scheduled tasks for your
     account only.
   - **Add pterm to my PATH** (unchecked): edits only your user PATH.

Without the PATH option, `pterm` is not a command. In PowerShell, go to the
folder that has `pterm.exe` and write `.\pterm.exe` in its place, for example
`.\pterm.exe setup`.

#### Test on Windows with a single pterm.exe

To try a bare `pterm.exe` without the installer:

1. Put it in `%LOCALAPPDATA%\Programs\Termivoa\`. Do not use Desktop or
   Documents (OneDrive may move or sync them) or a Temp folder (`pterm setup`
   refuses Temp): the startup tasks run this exact path at every logon.
2. In PowerShell:

   ```powershell
   cd "$env:LOCALAPPDATA\Programs\Termivoa"
   .\pterm.exe setup
   .\pterm.exe doctor
   ```

3. Logs are in `%LOCALAPPDATA%\Phone Terminal\logs\` (`broker.log`,
   `gateway.log`).

Microsoft Defender or SmartScreen may warn about an unsigned `pterm.exe`.
Signed builds are pending owner signing.

### Linux (Ubuntu 22.04 and 24.04, x86_64 or arm64)

Termivoa is one static `pterm` file. It needs no root, no packages and
no Node.js on the host. Put it in `~/.local/bin` and run `pterm setup`:

```sh
# On a build computer with Go, Node 24 and pnpm (any OS), from a source checkout:
pnpm install --frozen-lockfile
./packaging/linux/build.sh            # dist/linux/pterm-linux-amd64, -arm64, SHA256SUMS

# On the Linux host (copy the file first, for example with scp):
./scripts/install-linux.sh ./pterm-linux-amd64    # or pterm-linux-arm64
```

`scripts/install-linux.sh` copies the file to `~/.local/bin/pterm` and runs
`pterm setup`. Without an argument, in a source checkout on the host, it builds
the binary first. It refuses to run as root: Termivoa runs as your user.
As root it prints the commands that make a normal user and copy the files
to it; under `sudo` it says to run it without `sudo`. If the shell does not
find `pterm` yet, setup's last lines give the `export PATH=…` command for
this terminal, and say whether new logins have it (bash with Ubuntu's
`~/.profile` does; zsh does not). Invited beta testers can download the
binary instead of building it; see [Private beta](#private-beta).

Linux uses the XDG directories:

| | Path |
| --- | --- |
| Data (database) | `$XDG_DATA_HOME/phone-terminal` (default `~/.local/share/phone-terminal`) |
| Logs | `$XDG_STATE_HOME/phone-terminal/logs` (default `~/.local/state/phone-terminal/logs`) |
| Sockets | `/run/user/<uid>/phone-terminal`; without it `$XDG_RUNTIME_DIR/phone-terminal`, else `<data>/runtime` |
| Startup units | `$XDG_CONFIG_HOME/systemd/user` (default `~/.config/systemd/user`) |

All of them are private to your user (directories `0700`, files `0600`).
`pterm` also refuses to register a copy in `/tmp`, `/var/tmp` or `/dev/shm`.

## First run: three steps

1. Install (above).
2. Run `pterm setup` in a terminal.
3. Scan the QR code with your phone, compare the code, and answer `y`.

`pterm setup` first runs only read-only checks: the pterm location, data and
runtime directories, whether the broker and gateway respond, startup
registration, available shells, and `tailscale status --json` /
`tailscale serve status --json` if Tailscale is installed. Then it lists what
it will do and asks **once**:

```text
pterm setup will:
  - Start Termivoa when you log in (your user only, no administrator rights).
  - Keep its data in /Users/you/Library/Application Support/Phone Terminal and its logs in /Users/you/Library/Application Support/Phone Terminal/logs.
  - Save the phone address https://<mac>.<tailnet>.ts.net (unless you set another one).
  - Share Termivoa with your tailnet devices at https://<mac>.<tailnet>.ts.net (port 443). It asks first.
  - Share dev-server previews at https://<mac>.<tailnet>.ts.net:8443 (port 8443). It asks first.
  - Add your home folder /Users/you as the project "Home folder" if you have no project.
  - Then connect your phone (pair it, or add this computer to your app).
For the checks, the startup files and every command, run `pterm setup --verbose`.

Continue? [y/N]
```

After `y` it:

- registers startup (see the table below) and waits for the broker and gateway;
- saves `https://<Self.DNSName>` from `tailscale status --json` as the phone
  address. The running gateway accepts that Host and Origin within about a
  second, without a restart. It does not replace an address you set yourself
  with `pterm config origin`;
- asks a **separate** question before it runs
  `tailscale serve --bg --https=443 http://127.0.0.1:8787`. Your `y` there is
  the only authorization. It asks only when HTTPS 443 serves nothing else and
  Funnel is off; otherwise it lists the conflict and changes nothing. It never
  enables Funnel;
- after that, only when HTTPS 443 serves Termivoa, asks a **second,
  separate** question before it runs
  `tailscale serve --bg --https=8443 http://127.0.0.1:8789` for dev-server
  previews (see below). It changes nothing if HTTPS 8443 already serves
  something else;
- adds your home folder as the project "Home folder" when no project exists;
- connects your phone. When Tailscale shows other computers of yours, it
  first asks "Is Termivoa already on your phone?": `1` pairs this computer
  (below); `2` asks which computer you first paired the phone with and adds
  this computer to that app, the same as `pterm pair --via <name>`
  (`docs/multi-computer.md`). A tagged Tailscale computer needs a separate
  `y` (the same as `--allow-tagged`). A wrong number or an unknown name asks
  again; Enter skips. Otherwise it goes straight to pairing (below).

Without `--verbose`, the checks are not printed, but every `!` warning they
find that the plan does not state itself (for example other ports that get
the Termivoa cookie, Funnel, or a multi-computer finding) is printed under
`Warnings:` before the plan. The Funnel reminder prints only with
`--verbose` or when Funnel is on for another port (Funnel on 443 or 8443
stops setup, see below).

When the phone cannot reach this computer over port 443 (Tailscale missing
or signed out, the Tailscale operator missing on Linux, a Serve conflict, or
you said no to sharing), the plan says "Your phone cannot reach this
computer yet: <fix>", setup still registers startup, and it ends without
pairing: "Setup is done, but your phone cannot reach this computer yet:
<fix>". Each fix ends with "then run `pterm setup` again" (or "run `pterm
setup` again and say yes to sharing on port 443").

On Linux, setup refuses to run as root and prints the commands that make a
normal user (the `sudo` or `wheel` group, your `authorized_keys`, a copy of
`pterm`); under `sudo` it says to run it without `sudo`. When the shell does
not find this `pterm` on `PATH` yet, its last lines give the `export PATH=…`
command for this terminal.

`pterm setup --apply --yes` is the installer path: no questions. It registers
startup and saves the Tailscale phone address if none is set. It never changes
Tailscale Serve and does not pair. `--apply` refuses while a broker or gateway
already runs without startup registration (for example after `pterm launch`):
the registered copy could not start and would restart forever. Stop the
process it names, then rerun.

What startup registration adds:

| | macOS | Windows | Linux |
| --- | --- | --- | --- |
| Broker | `~/Library/LaunchAgents/dev.phone-terminal.broker.plist` | task `Phone Terminal Broker (<user>)` | `~/.config/systemd/user/phone-terminal-broker.service` |
| Gateway | `~/Library/LaunchAgents/dev.phone-terminal.gateway.plist` | task `Phone Terminal Gateway (<user>)` | `~/.config/systemd/user/phone-terminal-gateway.service` (`Wants=` and `After=` the broker) |
| Start | at login (`RunAtLoad`) | at your logon (gateway 10 s later), normal priority | when your systemd user manager starts: at login, or at boot with linger (below) |
| Restart | on failure (`KeepAlive` / `SuccessfulExit=false`) | Task Scheduler "restart on failure" (every minute, up to 999 times); not yet verified through `conhost.exe --headless` | on failure, after 5 s (`Restart=on-failure`) |
| Command | `launchctl bootstrap gui/<uid> <plist>` | `schtasks /Create /TN <name> /XML <file> /F`, then `/Run` | `systemctl --user daemon-reload`, then `enable` and `start` (or `restart` for a changed unit) |
| Logs | `~/Library/Application Support/Phone Terminal/logs/` | `%LOCALAPPDATA%\Phone Terminal\logs\` (pterm writes them; the headless console shows nothing) | `~/.local/state/phone-terminal/logs/`; `journalctl --user -u phone-terminal-broker` shows starts and stops |

On all systems the gateway waits up to two minutes for the broker when it
starts, so a slow broker start at login does not stop it.

On Linux, setup also asks a **separate** question about linger, and on a
server you need it; see [Linux: desktop, server and WSL](#linux-desktop-server-and-wsl).

### Pairing a phone

`pterm pair` (and the end of `pterm setup`) prints the link and, on a
terminal, a QR code of the same link in black on white. Then it waits until
the link expires (two minutes). When the phone opens the link it shows a
code, and pterm asks:

```text
Approve only if the phone shows exactly this code.
Phone "iPhone" shows code 123-456. Approve? [y/N]
```

Answer `y` only if the codes are the same. Anything else leaves the phone
unpaired. pterm never approves without your answer.

- `--no-wait` prints the link and returns. Then approve with
  `pterm pair pending` and `pterm pair approve <pending-id>`.
- `--no-qr` prints only the link.
- `--origin <https-origin>` uses another address for this one link.

The link and the QR code are a single-use secret: do not share them in chats,
issues, screenshots or logs.

Other commands: `pterm project add <path>` adds more projects,
`pterm devices` lists phones, `pterm devices rename <device-id> <label>`
renames one, `pterm revoke <device-id>` removes one. Flags such as `--dev`
may come before or after IDs and paths.

`pterm doctor` repeats the read-only checks and exits non-zero if it finds a
problem. It never prints secrets.

`pterm launch` (the app and the Start-menu shortcut use it) starts the broker
and gateway if they are not running and opens `http://127.0.0.1:8787/`.

## Phone access with Tailscale Serve

`pterm setup` offers to run this for you, and runs it only after you answer
`y`. You can also run it yourself on the host:

```sh
tailscale serve --bg --https=443 http://127.0.0.1:8787
```

Then open `https://<host>.<tailnet>.ts.net/` on your phone (on the same
tailnet). If HTTPS 443 already serves something else, setup reports it and
does not change it: resolve that yourself; do not overwrite another service.
`pterm setup` (also with `--apply`) refuses to run while
other Serve handlers share this computer's HTTPS origin, because they would be
same-origin with the app. It names each handler and the command that removes
it.
If you set up Serve by hand under another name, tell pterm with
`pterm config origin https://<that-name>`; the gateway accepts it at once.

### Dev-server previews (optional, HTTPS 8443)

The phone can show a web page that a program in a session serves (for example
`npm run dev`). The page is served on a separate origin, so it needs a second
Serve port. `pterm setup` asks separately, or run it yourself:

```sh
tailscale serve --bg --https=8443 http://127.0.0.1:8789
```

Previews then open inside the app at `https://<host>.<tailnet>.ts.net:8443/`.
Only ports that the session's own processes listen on are shown. `pterm
doctor` says whether this is set up; it is optional and never a doctor
failure. The gateway serves only dev-server previews on 127.0.0.1:8789, never
the app. Design and security: [preview.md](preview.md).

**Never use Tailscale Funnel.** Funnel makes the page public on the internet.
`pterm doctor` reports Funnel as a problem. `pterm setup` refuses, before any
change, while Funnel is on for port 443 or 8443 (also with `--apply --yes`),
and prints the command that turns it off, for example
`tailscale funnel --https=443 off`; it never runs it.

On Linux, only root or the Tailscale **operator** may change Serve. If you are
not the operator, `pterm setup` and `pterm doctor` say so and do not ask the
Serve questions. Run this once yourself, then run `pterm setup` again:

```sh
sudo tailscale set --operator=$USER
```

pterm never runs `sudo`. It finds `tailscale` on your PATH, in `/usr/bin` or in
`/usr/sbin`.

## Linux: desktop, server and WSL

The broker and the gateway are systemd **user** services: they run as you,
never as root. `pterm setup` shows the two unit files and every `systemctl
--user` command before it runs them, as on macOS.

### Ubuntu desktop

Run `pterm setup` in a terminal. Termivoa starts when you log in and
stops when you log out. Keep the computer awake while you use it from the
phone (Settings > Power > Automatic Suspend: off).

### Ubuntu server or VPS (SSH only)

A server has no desktop login. Without **linger**, systemd stops your user
services, and every session, when your last SSH login closes. `pterm setup`
asks a separate question:

```text
Keep Termivoa running when you log out, and start it at boot before anyone logs in?
On a server (no desktop login) you need this, or your sessions end when your last SSH login closes.
This runs: loginctl enable-linger <user> [y/N]
```

Answer `y` on a server. pterm runs `loginctl enable-linger` only after that
`y`, never with `sudo`. If your system refuses it (no polkit rule for you),
setup prints `sudo loginctl enable-linger <user>` for you or an administrator
to run. `pterm doctor` shows `linger: on` or `off`. With linger on, Termivoa
starts at boot, and sessions survive logout. A reboot or a broker
crash still ends every session.

Then install Tailscale on the server (`tailscale up`), make yourself the
Tailscale operator (above), and run `pterm setup` again for the Serve
questions and pairing.

### Without systemd (WSL without systemd, containers)

If `systemctl --user` does not answer, nothing can start Termivoa at
login. `pterm setup` and `pterm doctor` say so. Setup then starts the broker
and gateway in the background, as `pterm launch` does; they keep running
after you close the terminal. Run `pterm launch` again after each restart.
pterm never falls back to this silently.

### WSL (Windows Subsystem for Linux)

pterm detects WSL. Windows stops the WSL virtual machine when no Windows
program uses it (about a minute after the last WSL window closes), and every
session ends then. Choose one:

- **Option A (recommended):** install the **Windows** app. With a WSL distro
  installed, it offers a **WSL bash** shell profile. The broker runs on
  Windows, so sessions do not depend on a WSL window.
- **Option B:** install the Linux `pterm` inside WSL. Turn on systemd in WSL
  so it can start at login: add these lines to `/etc/wsl.conf`, run
  `wsl --shutdown` in Windows, open WSL again, and run `pterm setup`:

  ```ini
  [boot]
  systemd=true
  ```

  Sessions still end when Windows stops the WSL VM.

## Keep the host awake; broker lifetime

Sessions live inside the broker on your computer. The phone only views and
controls them.

- If the computer sleeps, the phone cannot reach it. Keep it awake and on
  power while you work from the phone (macOS: System Settings > Battery /
  Energy options; Windows: Settings > System > Power; Ubuntu desktop:
  Settings > Power).
- Logging out, rebooting, or a broker crash **ends every live session**. The
  broker restarts, marks those sessions lost, and never re-runs old commands.
- Restarting only the gateway keeps sessions alive.

## Stop and shut down

```sh
pterm stop                       # stop the gateway only; sessions keep running
pterm shutdown                   # confirm, end every session, stop broker and gateway
```

`pterm stop` asks the running gateway to exit through its private control
endpoint in the runtime directory (a current-user-only socket or named pipe).
It never looks up or signals processes by name. The broker and every session
keep running, and `pterm list` still shows them; phones cannot connect until
the gateway starts again (`pterm launch`, or your next login).

`pterm shutdown` shows how many sessions are active and asks for confirmation,
like `pterm terminate`. Then the broker ends every session and its process tree
through the normal termination path, stops, and the gateway stops after it.
Phones that are still connected see the host as unavailable.

Both commands exit the processes cleanly, so the supervisor does not restart
them on failure. If they are registered for startup, the supervisor starts them
again at your next login (or `pterm launch` starts them). The command then
prints the exact command that unloads the registration, but never runs it:

| | macOS | Windows | Linux |
| --- | --- | --- | --- |
| Gateway | `launchctl bootout gui/<uid>/dev.phone-terminal.gateway` | `schtasks /Change /TN "Phone Terminal Gateway (<user>)" /Disable` | `systemctl --user disable --now phone-terminal-gateway.service` |
| Broker | `launchctl bootout gui/<uid>/dev.phone-terminal.broker` | `schtasks /Change /TN "Phone Terminal Broker (<user>)" /Disable` | `systemctl --user disable --now phone-terminal-broker.service` |

`pterm setup --apply` registers them again. On Windows, `pterm doctor` reports
a disabled task, and setup creates it again, enabled.

## Updates (manual, staged)

v0 has no automatic updater. Nothing downloads code at startup.

1. Install the new version beside the old one:
   - Windows: run the new installer. It adds
     `...\Phone Terminal\versions\<new-version>\` and keeps the old directory for
     rollback. It never closes a running broker.
   - macOS: keep the old app (for example rename it
     `Termivoa <old-version>.app`), then copy the new app to Applications.
     Before the rename the app was `Phone Terminal.app`; step 2 re-points
     startup to `Termivoa.app`, then you can move the old app to the Trash.
   - Linux: keep the old file (for example `cp ~/.local/bin/pterm
     ~/.local/bin/pterm-<old-version>`), then run `scripts/install-linux.sh`
     with the new file. The unit files do not change, so setup restarts
     nothing: the running broker keeps the old version until it restarts.
     Run `pterm shutdown` and `pterm launch` (after your sessions finish) to
     use the new one now.
2. The new pterm re-points startup with `pterm setup --apply` (the Windows
   installer does this). If the registered broker changes and the broker has
   **active sessions, setup refuses** and tells you to finish them or end them
   with `pterm terminate <session-id>`. It does not restart a broker with live
   sessions.
3. On Windows, re-registering the task does not stop the running broker; the
   new version starts at your next logon. On macOS, `--apply` reloads the
   LaunchAgents, which restarts the broker (only when no session is active).
4. Rollback: run `pterm setup --apply` from the previous version.

To update now instead of waiting for sessions to finish, run `pterm shutdown`
first: it ends every session only after you confirm. Pending: a broker-side
refusal for replacement (an update RPC) needs broker RPC support. Today the
check is done by `pterm setup` / `pterm uninstall` through the session list
before they change the broker registration. Database backup before migration is
not yet implemented.

## Uninstall

```sh
pterm uninstall --dry-run        # show what would be removed; changes nothing
pterm uninstall                  # remove startup registration after confirmation
pterm uninstall --delete-data    # also delete paired devices, projects, settings and logs
```

Uninstall stops and removes the two startup entries. It refuses while the
broker has active sessions or does not answer. On Windows, ending a task may
end only its console host, so uninstall then also stops the gateway and shuts
down the broker, and only after it checks again that no session is active.
Your data stays unless you pass
`--delete-data`, which also refuses while any broker still runs (for example
one started by `pterm launch` without startup registration); stop the broker
it names first.

Then remove the application. `pterm uninstall` prints the exact file to
delete:

- macOS: move Termivoa.app (or an older `Phone Terminal.app`) to the Trash and delete any `~/.local/bin/pterm` link.
  With the beta download, delete `~/.local/bin/pterm`.
- Linux: `rm ~/.local/bin/pterm`. Uninstall already ran
  `systemctl --user disable --now` for both units, removed them and ran
  `daemon-reload`. It does not turn linger off; to do that, run
  `loginctl disable-linger`.
- Windows: Settings > Apps > Termivoa > Uninstall. The uninstaller runs
  `pterm uninstall`, asks whether to delete data (default: keep), and removes
  its PATH entry. With the beta download (a single `pterm.exe`), delete that
  file.

Data locations (they keep the old working name so existing installs keep
their data; see [naming.md](naming.md)):

- macOS: `~/Library/Application Support/Phone Terminal` and `~/Library/Caches/Phone Terminal`
- Windows: `%LOCALAPPDATA%\Phone Terminal`
- Linux: `~/.local/share/phone-terminal`, `~/.local/state/phone-terminal` and
  `/run/user/<uid>/phone-terminal` (`--delete-data` removes these three,
  never their parents)

If you enabled Tailscale Serve, remove it yourself (`tailscale serve reset`
removes **all** Serve configuration; use `tailscale serve status` first).

## Development harness

```sh
pterm up --dev                   # broker + gateway, isolated development profile
```

It starts the broker and gateway as two child processes in separate process
groups, prints the loopback URL (default `http://127.0.0.1:8788/`) and the
matching `pterm pair --dev ...` command, and stops both on Ctrl+C. Killing the
gateway does not stop the broker. `--data-dir`, `--runtime-dir` and `--listen`
select other development locations. `--preview-listen` sets the dev-server
preview listener (default `127.0.0.1:8790`; `off` disables it). It changes no startup, Tailscale or
production state.

A full local run from a source checkout, with isolated temporary directories:

```sh
pnpm install --frozen-lockfile
pnpm build:web && ./scripts/embed-web.sh
go build -tags webdist -o bin/pterm ./cmd/pterm
mkdir -p /tmp/pt/data /tmp/pt/run && chmod 700 /tmp/pt/data /tmp/pt/run
./bin/pterm up --dev --data-dir /tmp/pt/data --runtime-dir /tmp/pt/run
```

The `/tmp` and `chmod` lines are for macOS and Linux only. On Windows, build
`bin\pterm.exe` and leave out `--data-dir` and `--runtime-dir`: the development
profile then uses `%LOCALAPPDATA%\Phone Terminal Development`. In PowerShell:
`.\bin\pterm.exe up --dev`, and in the second terminal the same commands with
`.\bin\pterm.exe` and without `--runtime-dir /tmp/pt/run`.

In a second terminal:

```sh
./bin/pterm project add --dev --runtime-dir /tmp/pt/run --name demo <project-dir>
./bin/pterm config origin --dev --runtime-dir /tmp/pt/run http://127.0.0.1:8788
./bin/pterm pair --dev --runtime-dir /tmp/pt/run
./bin/pterm list --dev --runtime-dir /tmp/pt/run
./bin/pterm attach --dev --runtime-dir /tmp/pt/run <session-id>
```

Open the pairing link in a browser, compare the code and answer `y` (or use
`pterm pair pending` and `pterm pair approve <pending-id>` with `--no-wait`).
Start a session and tap **Take control**. `pterm attach` starts read-only;
`--control` takes control. Ctrl+] detaches without ending the session.


### Next beta: how is the GitHub login removed during uninstall?

On macOS/Linux, confirmed `pterm uninstall --delete-data` tries to revoke the
saved pair before removing local data. A request-only outage prints the
manual GitHub applications hint; canceling stops local deletion. The private
empty data directory and ownership marker remain after removal, so another
broker cannot replace its lock during cleanup. No database, paired device,
project, setting or login remains there. See [uninstall-github.md](uninstall-github.md)
for the private design and pending owner-review gate.
