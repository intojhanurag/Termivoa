# Naming

The product name is **Termivoa**. The command stays `pterm`.

"Phone Terminal" was the working name during development. Everything you see
(the web app, CLI messages, docs, the macOS app `Termivoa.app`, the DMG and the
Windows installer) now says Termivoa.

## Names kept on purpose

Some names still use the old working name. They are stored on your computer or
registered with the OS. If we renamed them, an existing install would lose its
data (paired devices, projects, settings, logs) or keep a stale startup entry.
A future release can move them with a real migration. Until then, do not change
them.

| What | Kept name |
| --- | --- |
| Command | `pterm` |
| Data and runtime directories (macOS) | `~/Library/Application Support/Phone Terminal`, `~/Library/Caches/Phone Terminal` |
| Data directory (Windows) | `%LOCALAPPDATA%\Phone Terminal` |
| Data, state and runtime directories (Linux, XDG) | `phone-terminal` (`~/.local/share`, `~/.local/state`, `/run/user/<uid>`) |
| Development profile directories | `Phone Terminal Development`, `phone-terminal-development` |
| Windows install directory | `%LOCALAPPDATA%\Programs\Phone Terminal` (installer `AppId` unchanged) |
| SQLite database file | `phone-terminal.db` |
| macOS LaunchAgent labels | `dev.phone-terminal.broker`, `dev.phone-terminal.gateway` |
| macOS bundle ID | `dev.phone-terminal.companion` |
| systemd user units | `phone-terminal-broker.service`, `phone-terminal-gateway.service` |
| Windows scheduled tasks | `Phone Terminal Broker (<user>)`, `Phone Terminal Gateway (<user>)` |
| Unit and task descriptions | `Phone Terminal broker ...` (`pterm doctor` compares the whole file; a new text would mark every existing registration as changed) |
| Cookies | `__Host-pterm_session`, `pterm_dev_session`, `__Host-pterm_preview` |
| Protocol and package identifiers | capability profile `phone-terminal-v1`, pnpm packages `@phone-terminal/*`, client build strings |
| Go module path | `github.com/intojhanurag/phone-terminal` |

The Go module path is only a build identifier. GitHub redirects the old
repository name to `intojhanurag/Termivoa`. Renaming the module touches every
Go import and is a later cleanup.

## Updating from Phone Terminal.app

On macOS the app is now `Termivoa.app`. Copy it to Applications, then run
`pterm setup --apply` from it (see [install.md](install.md#updates-manual-staged)).
Setup points the LaunchAgents at the new app. Then move `Phone Terminal.app` to
the Trash. Your data stays where it is.

On Windows the installer keeps the same install directory and replaces the old
Start menu shortcuts with Termivoa ones.
