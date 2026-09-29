# Project tasks

The phone can show buttons for the commands a session's project defines:
`package.json` scripts, Makefile targets, and the usual Cargo and Go commands.
The wire format is in `protocol/v1`: `tasks.list` → `tasks.listed` on the live
stream. The code is `internal/projecttasks` (reading) and
`internal/broker/tasks.go` (wiring).

## Which folder

The project folder the session started in, never the live working folder: the
phone may run a task in a new session, and a new session opens in the project
folder. The reply carries it as `folder`.

## What is read

Nothing runs on the host. The broker only reads files, through `os.Root`, so a
read never leaves the folder. A symlink or anything that is not a regular file
is ignored, as if it were absent.

| File | Tasks |
| --- | --- |
| `package.json` | Every `scripts` entry with a string value, in file order: `<pm> run <name>`. The package manager comes from the lockfile: `pnpm-lock.yaml` → `pnpm run`, `yarn.lock` → `yarn <name>`, `bun.lock` or `bun.lockb` → `bun run`, `package-lock.json` → `npm run`. With no lockfile, the `packageManager` field (`pnpm@9.0.0` → pnpm), else npm. yarn keeps `run` for names that are also yarn commands (`check`, `publish`, `install`, …). |
| `GNUmakefile`, `makefile` or `Makefile` (the first found, as make does) | Explicit targets, in file order, deduplicated: `make <name>`. Lines `name:` or `a b: deps` outside recipes and `define` blocks. Skipped: variables (`=`, `:=`, `::=`, `?=`, `+=`), pattern rules (`%`), special and hidden targets (`.PHONY`, `.x`), names with `$`, target-specific variables (`name: VAR = value`), and continuation lines. |
| `Cargo.toml` | `cargo test`, `cargo run`, `cargo build`, `cargo clippy`. |
| `go.mod` | `go test ./...`, `go build ./...`, `go vet ./...`. |

## Kinds and order

| Kind | Names |
| --- | --- |
| `test` | `test`, `test:*`, `test-*`, or a name that contains `spec` or `e2e` |
| `dev` | `dev`, `start`, `serve`, `watch`, `preview` (alone or before `:` or `-`), and `cargo run` |
| `build` | `build*`, `compile`, `bundle` |
| `lint` | `lint*`, `format*`, `fmt`, `typecheck`, `check`, `cargo clippy`, `go vet` |
| `other` | anything else |

Tasks are sorted by kind in that order, then by source (`package.json`,
Makefile, `Cargo.toml`, `go.mod`) and file order. At most 50 are sent, so
`other` tasks are dropped first.

## Safe names

The phone types the command into a terminal, so a script or target name must
be one plain shell word: 1..128 characters of `A-Z a-z 0-9 : . _ / @ + -`,
not starting with `-` (it would read as an option). Other names are skipped.
The broker validates every outgoing `tasks.listed` again: names follow this
rule, commands are one line of 1..512 characters without control characters,
and all text is valid UTF-8.

## States and bounds

| State | When |
| --- | --- |
| `ready` | At least one known file was read (the list may be empty, for example a `package.json` without scripts). |
| `none` | No known file in the folder. |
| `unavailable` | The folder cannot be opened, or known files exist but all failed (unreadable, or `package.json` is not valid JSON) and no task is left. A failing source is skipped when another one works. |

| Bound | Value |
| --- | --- |
| Bytes read per file | 256 KiB. A longer `package.json` fails; a longer Makefile is read up to its last whole line. |
| Tasks per reply | 50 |
| Time per request | 3 s |
| Requests in flight | One per viewer; another gets `limit_exceeded`. |

Any viewer of the session can list tasks; no controller lease is needed, as
for `changes.list`. The request runs off the session actor and is cancelled
when the viewer detaches. Running a task is ordinary typed input and needs the
lease like any other input.
