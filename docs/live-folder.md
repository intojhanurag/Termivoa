# Live folder

A session starts with the project name as its title, for example "Home folder".
After `cd`, the phone shows where the session is now: the tab and the
Sessions row show the folder name, and the Live status line shows the path
(`~/src/app`). A session that a user renamed keeps its name. The Sessions row
still shows the project name on its second line ("Home · zsh").

The wire format is in `protocol/v1`: `cwd` on the session record and on
`session.state`, and `titleIsCustom` on the session record.

## Where the folder comes from

| Host | Source |
| --- | --- |
| macOS | The current directory of the session's foreground process: the PTY's foreground process group (`tcgetpgrp` on the PTY master), else the newest process in the PTY session, else the shell. Read with `/usr/sbin/lsof -a -d cwd -p <pid> -Fn` (absolute path, no cgo). |
| Linux | The same process choice, read with `readlink /proc/<pid>/cwd`. |
| Windows, PowerShell (5.1 and 7) | A session-scoped prompt hook. The broker starts the profile with `-NoExit -Command <hook>`. The hook wraps the existing `prompt` (after the profile loaded it) so each prompt also writes `ESC ] 9 ; 9 ; "<path>" ESC \` for FileSystem locations. No profile file is edited and the screen does not change. |
| Windows, cmd.exe and WSL | No hook. These sessions keep their project name unless the shell itself writes OSC 7 or OSC 9;9. |
| Any host | An OSC 7 (`ESC ] 7 ; file://host/path`) or OSC 9;9 report in terminal output. A fresh report wins over the operating system. The OS value takes over again only when it changes, so a shell that reports `/tmp` does not flap against the OS path `/private/tmp`. |

The broker never injects hooks on macOS or Linux. The PowerShell hook is added
only when the profile's arguments are `-NoLogo`, `-NoProfile` or `-NoExit`; a
profile with its own `-Command` or `-File` runs exactly as configured.

The terminal core records OSC 7 and OSC 9;9 payloads (at most 4 KiB) without
consuming them (`internal/terminal`). The broker parses them
(`internal/broker/cwd.go`): OSC 7 must be a `file:` URL without user, query or
fragment; OSC 9;9 may be quoted.

## When the broker checks

- Once, 0.5 seconds after the session starts.
- 0.5 seconds after output or accepted input, at most once every 1.5 seconds.
- Never while the session is quiet. One OS query runs at a time, off the
  session actor, with a 3-second limit.

A change is sent on `session.state` at once and appears on the next session
list poll.

## Security

The folder is display text only. It comes from terminal output or from the
operating system, so the broker accepts only:

- an absolute POSIX path (`/…`), Windows drive path (`C:\…` or `C:/…`) or UNC
  path (`\\server\share`),
- without `.` or `..` segments,
- at most 512 bytes of valid UTF-8, without control characters (C0, C1, DEL)
  or leading/trailing white space.

Anything else is dropped (the previous folder stays). Other non-printable
characters, such as bidi overrides, are shown as U+FFFD. The phone renders the
value only as React text: never HTML, a link, or part of a URL.

## Titles

`session_metadata.title_custom` is set when a user renames
a session. The default title (the project name, or "Terminal") is not custom.
Sessions that existed before the migration count as renamed only when their
title differs from both default titles. The session record carries
`titleIsCustom: true` for renamed sessions.

## Known limits

- The phone does not know the host's home folder. It shortens `/Users/<name>`,
  `/home/<name>`, `/root` and `<drive>:\Users\<name>` to `~`, so another
  user's home also shows as `~`.
- Paths longer than 512 bytes are not shown; the title falls back to the
  project name.
- On Windows the PowerShell hook depends on ConPTY passing OSC 9;9 through.
  If a program replaces `prompt` later in the session (for example re-running
  a prompt theme's init), the hook is lost until the next session.
