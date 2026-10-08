# GitHub: set up a repository on a computer from the phone

- Status: **approved** by the owner on 2026-10-07, with the recommended choices (push allowed: Contents: Read and write). Critical under AGENTS.md rule 24. ADR [0010](decisions/0010-github-integration.md).
- Pictures (drawn in Paper, demo data): [`docs/evidence/github-integration/`](evidence/github-integration/).

## 1. What the owner asked for

> "in setting can we give integrate with github option … show any repo list … what he want to setup on which computer … if during setting up any folder found with similar name locally drop"
>
> "there is no sense of computer has installed or not gh … Be professional feature, not like bulky to user, every edge case should be cover"

So: one **Connect** in **Settings → Integrations**, no terminal, no `gh`, then a list of repositories, and one tap puts a repository on the chosen computer. "Drop" is read as **skip**: Termivoa never deletes or overwrites a folder (section 8).

## 2. What the user sees

| Step | Screen | Picture |
| --- | --- | --- |
| 1 | **Settings → Integrations** has one row for each computer, "GitHub on mac", like "herdr on mac": **Connect**, or the account name with **Set up a repository** and **Disconnect**. It shows also on a computer with no herdr | [g1](evidence/github-integration/g1-settings-card.png) |
| 2 | **Connect** shows a code and **Copy code and open GitHub**. GitHub opens; the user pastes the code and authorizes | [g2](evidence/github-integration/g2-connect-code.png) |
| 3 | One time for each GitHub account: **Choose on GitHub** (all repositories, or only some) | [g4](evidence/github-integration/g4-choose-repositories.png) |
| 4 | **Set up a repository**: a search box and the list. A repository that is already on that computer has the tag "on mac" | [g3](evidence/github-integration/g3-repository-list.png) |
| 5 | A tap on **Set up** clones on the computer. The phone can lock; the computer continues | [g5](evidence/github-integration/g5-cloning.png) |
| 6 | "Ready on mac" with **New session** and, when herdr runs there, **New workspace in herdr** | [g6](evidence/github-integration/g6-ready.png) |

Every problem is one line and one action: [g7](evidence/github-integration/g7-every-case.png) (section 9).

The second computer needs only step 2 (step 3 is done for the account). The same sheet opens from **From GitHub**, a link next to **Add a folder** in the **New workspace** sheet, so the list is also where a project is picked.

The pictures use the app's own parts (flat rows, the mono state word, the bone button, the sheet). Two small additions: `SheetHeader` gets one optional caps line ("GITHUB · ON MAC"), and the progress line is the browser's own `<progress>`.

## 3. Facts this design stands on

Checked against docs.github.com on 2026-10-06 (URLs in the ADR) and against the code.

| Fact | Source |
| --- | --- |
| A GitHub App can log a user in with the device flow. No client secret is needed to start it, to poll it, or to refresh a token that came from it | GitHub docs: "Generating a user access token", "Refreshing user access tokens" |
| The access token lasts 8 h; the refresh token lasts 6 months and is single-use (each refresh gives a new pair) | same |
| The token reaches only repositories where the app is **installed** and that the user can reach. Authorizing and installing are two steps | GitHub docs: "Installing a GitHub App from a third party" |
| There is no link with the code filled in (`verification_uri_complete`); the user types or pastes 8 characters | same |
| A client without a secret cannot use the app's revoke endpoints, but `POST https://api.github.com/credentials/revoke` takes no authentication and accepts `ghu_` and `ghr_` tokens | GitHub REST docs: OAuth applications; Credentials → Revoke |
| git asks system and global credential helpers **before** a repository's own, and after a success it sends `store` to **all** of them (so a token would land in the macOS Keychain or in `~/.git-credentials`). `GIT_ASKPASS` takes one program path, no arguments | Run on this Mac, git 2.53, by both reviewers |
| The database schema is one checksummed file; a new table makes every existing database refused | `internal/store/migrations.go:19-22,62` |
| The broker keeps secrets today only in its SQLite file (mode 0600): the VAPID private key is a plain BLOB there. No OS keychain is used anywhere | `internal/store/store.go:56,63`, `internal/store/push.go:24` |
| The broker has one outbound HTTPS client today (Web Push): no proxy, no redirects, public addresses only, host allowlist, timeouts, body cap | `internal/webpush/webpush.go:268-336` |
| No phone route adds a project today; the threat model forbids it | `docs/threat-model.md:91-92` |
| The broker has no background-job pattern; the phone waits on long requests or polls | `web/src/pairing/addComputer.ts:285` |
| The MacBook Pro has `gh` logged in; the VPS has `git` only | checked on both, 2026-10-06 |

Not proven yet (each is a first task of its pull request, section 12): the git user name that GitHub accepts with a user access token (documented only for installation tokens: `x-access-token`); what GitHub answers to a second try with the same refresh token (after a timeout); how the device page behaves in iPhone Safari and in the Home Screen app (is the GitHub login shared?); that `password_expiry_utc` keeps the user's other helpers from storing the token.

## 4. Decisions

1. **Termivoa's own login, one path.** A GitHub App named Termivoa, device flow, the client ID compiled into `pterm`. No `gh` on any computer. A computer that has `gh` logged in also uses **Connect**: two code paths (gh and our own) would double the tests and the edge cases for the gain of one code entry.
2. **GitHub App, not OAuth App.** The user picks the repositories, the tokens are short-lived, and the permission is one line (section 5). An OAuth App would skip step 3 but needs the `repo` scope: read and write on every private repository, with a token that never expires.
3. **One token for each computer**, kept in the broker database, as the VAPID key is. It never goes to the gateway, the phone or a log. The access token (never the refresh token) goes to git on that computer through a helper that only the same user can call (6.6). A lost computer is cut off alone on github.com. (Keychain storage is a follow-up; nothing in the broker uses a keychain today.)
4. **The computer does the work.** The broker talks to GitHub and runs `git`. The phone sends only `owner/name`. No path and no URL come from the phone.
5. **Skip, never delete.** An existing folder is used when it is the same repository and refused when it is not.
6. **The clone lands in the home folder**: `~/<name>`. The owner's repositories are there today, and there is no setting to learn. Two repositories with the same name from two owners cannot both be set up; the second says "name taken".
7. **A clone is a job on the computer**, one at a time for each computer. The phone reads its state.
8. **macOS and Linux, github.com only** in version 1.

## 5. The GitHub App (the owner registers it; rule 12)

| Setting | Value | Why |
| --- | --- | --- |
| Name | Termivoa | Shown on GitHub's authorize page |
| Public | Yes | Other accounts can install it (this is not the repository's visibility) |
| Enable Device Flow | On | Required |
| Expire user authorization tokens | On | 8 h tokens, GitHub's recommendation |
| Webhook | Off | Termivoa has no server |
| Callback URL | The docs site | Required field; not used by the device flow |
| Repository permissions | **Contents: Read and write**, Metadata: Read-only | Clone, pull and push. See the decision below |
| Setup URL | A docs-site page: "Done. Go back to Termivoa." | Nothing else returns the user from GitHub |
| Client secret | Never generated into the product | A distributed binary cannot keep one |

The client ID is public: anyone can copy it and show a login page that says "Termivoa". GitHub documents this for every public client. It gives the copier a token for the victim only if the victim enters a code that the copier started, which is why the sheet says "Enter a code only when you started Connect yourself."

**Decision for the owner: push.** The design asks for **Contents: Read and write**. Reasons: the product is agents that commit and push from the phone; on a computer with no git login (the VPS today) a read-only login cannot push at all; in a folder that Termivoa cloned, git uses Termivoa's login for github.com (6.6), so read-only would also stop push on the Mac there; and a later change of permission makes every user approve again. The cost: each connected computer holds a token that can write to every chosen repository for 8 hours, and a refresh token. A push that changes `.github/workflows` is still refused by GitHub (that needs the Workflows permission, which is left out). The other choice is Read-only: smaller damage from a stolen token, and no push through Termivoa.

## 6. How it works

### 6.1 Connect

1. The phone calls `POST /api/v1/github/connect` on that computer.
2. The broker asks GitHub for a device code and answers the phone with the user code, GitHub's address and the expiry. Only the phone that started it gets the code; the others see "connecting".
3. The phone shows the code. **Copy code and open GitHub** is a link (`target="_blank"`, `rel="noopener"`) to `https://github.com/login/device` (a fixed address in the web app, never one from an answer) that copies the code in the same tap, with no wait between (the pattern of `Changes.tsx` and `AddComputer.tsx`).
4. The broker polls GitHub at the interval GitHub gave (plus 5 s after each `slow_down`), until a token, a refusal, the expiry (15 minutes) or a cancel.
5. On a token: the broker reads the login name, stores the pair, and reads the installations. None → state `needsInstall` (step 3 of section 2: the phone opens `https://github.com/apps/<slug>/installations/new`, with the slug compiled in; while that state is on screen each 2 s read makes the broker look again, at most once in 5 s, so no tap is needed).
6. The sheet is drawn from `GET /api/v1/github`, so it comes back in the same step after iOS reloads the app.

### 6.2 The token

- One row, one writer. Every use goes through one function that holds a lock, refreshes when less than 5 minutes are left, and **commits the new pair to the database before it returns it**. The old pair is dead from GitHub's answer on, so there is nothing to keep. The pair is lost when the broker stops between GitHub's answer and the commit, or when the answer does not arrive in the 10 s limit after GitHub already rotated; the broker then tries one time more with the same refresh token (a lab item: does GitHub accept it?), and after that the row says **Connect** again.
- An expired refresh token (6 months) → the pair is deleted; "GitHub is not connected on mac now."
- A `bad_refresh_token` that the broker does not expect (the token is not old) means that something else used it. It gets its own line: "Another program used this login. Revoke Termivoa on GitHub, then connect again." with the link. Connecting again does **not** cut off a thief; only the revoke on github.com does.
- **Disconnect** sends both tokens to `POST https://api.github.com/credentials/revoke` (best effort, 10 s), then deletes the stored pair (as built: the file `github.json`, overwritten with zeros, then removed; section 17). When the revoke call fails, the sheet says so and gives the link to **Settings → Applications → Authorized GitHub Apps**.
- The tokens are a secret type that prints as `[redacted]` (as `internal/auth/secret.go` does), not a formatter rule. `pterm doctor` prints only "GitHub: connected as <login>".

### 6.3 The list

`GET /api/v1/github/repos`: the broker reads `/user/installations` (at most 10) and each `/user/installations/{id}/repositories` (100 a page, at most 1,000 rows in total), and answers name, owner, private, fork, archived, size, last push, and `here` (below). Cached for 60 s for each computer; one shared run when two phones ask together; 15 s in total.

For each row the broker also answers `here` and `taken`: one `Lstat` of `~/<name>`, and, when a real directory owned by the user with a real `.git` directory is there, one read of its `origin` with `git config --file <dir>/.git/config` (never `git -C` in a folder that is not checked; `--file` follows no includes). The same repository → `here`. Anything else at that name → `taken`. A saved project elsewhere with that `origin` → `here`. These are cached with the list. (On the owner's Mac today 27 repositories are in `~` and only the home folder is a project; without the `Lstat` no row would say "on mac".)

### 6.4 Set up

`POST /api/v1/github/setup` with `{ "repo": "owner/name" }`.

1. Checks, in this order: the limiter; no other setup runs; the text matches `^[A-Za-z0-9_-]{1,39}/[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$` (so no owner with a dot, and no name that starts with `.` or `-`: a repository named `.ssh` or `.config` must never land in the home folder) and the name does not end in `.git`; GitHub answers `GET /repos/owner/name` and its `full_name` equals the request without regard to case (this stops a renamed or moved repository); the project count is under `MaxAdminProjects`; the free space is at least the repository's size times 3 plus 1 GiB.
2. The target is `~/<name>`, with the home folder resolved through its symlinks first. `Lstat` it:
   - **A saved project with the same `origin`** → answer `alreadyHere` with the project ID. Nothing runs.
   - **A real directory, owned by the user, with a real `.git` directory whose `origin` is the same repository** (https or ssh form, no regard to case) → add it as a project, answer `alreadyHere`.
   - **Anything else there** (also a name that differs only in case, on macOS) → answer `nameTaken`. Nothing is changed.
3. Nothing there → the broker makes `~/.termivoa-clone-<profile>-<random>` (mode 0700) and runs the clone into it (6.5). On success it sets the helper and the commit name (6.6), then renames the folder to `~/<name>` with a rename that **cannot replace**: `renamex_np(RENAME_EXCL)` on macOS, `renameat2(RENAME_NOREPLACE)` on Linux (Go's `os.Rename` replaces an empty folder, so it is not used). A name that appeared in the meantime → `nameTaken`, and the temp folder is removed. Then it adds the project. On failure, a stop, or the 30-minute limit, it removes **only the temp folder**, through an `os.Root` on the home folder. The final name is never deleted. At start (`NewManagementServer`) the broker removes temp folders of its own profile that a crash left.
4. The answer comes at once (`started`). The phone reads `GET /api/v1/github` every 2 s while the sheet is open: `cloning` with a percent, then `done` with the project ID (and `lfs: true` when the repository uses Git LFS: "Large files were not downloaded. Run `git lfs pull` in a session."), or `failed` with one of the fixed problem words. The last result stays until the next setup or 10 minutes.

### 6.5 The clone command

`git` is found with the same checked lookup as other binaries (`tailnet.CheckBinary`); the minimum version is the first that knows `GIT_CONFIG_COUNT` (2.31). No shell.

```
git clone --no-recurse-submodules --template= --progress https://github.com/<owner>/<name>.git <temp>
```

| Fixed | Value | Stops |
| --- | --- | --- |
| Environment | Only `HOME=<an empty temp folder>`, a fixed `PATH`, `LC_ALL=C`, the broker's socket address for the helper, and the lines below. Nothing from the broker's own environment, and **no token** | `GIT_*`, `GH_*`, `SSH_*`, proxy variables, `~/.netrc`, global attributes |
| `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1` | | `url.insteadOf`, global filters and helpers (the Keychain helper of Homebrew's system config) |
| `GIT_TERMINAL_PROMPT=0` | | A prompt |
| `GIT_LFS_SKIP_SMUDGE=1` | | LFS downloads during the clone |
| Config through `GIT_CONFIG_COUNT` | `credential.helper=` (reset), then `credential.https://github.com.helper=!<pterm> github-credential`; `protocol.allow=never`, `protocol.https.allow=always`, `http.followRedirects=false`, `http.proxy=`, `core.hooksPath=/dev/null`, `core.fsmonitor=false`, `submodule.recurse=false`, `transfer.fsckObjects=true` | Any other login source or store, other transports, redirects to another host, a proxy, hooks, fsmonitor, submodules, broken objects |
| Limits | 30 minutes; SIGTERM, then SIGKILL after 5 s; the temp folder's size is read every 5 s and the clone is stopped above the size from GitHub times 3 plus 1 GiB | A clone that never ends or fills the disk |

The first design passed the token in the child's environment through `GIT_ASKPASS`. Both reviews cut it: git runs `GIT_ASKPASS` as one program path, so it could not work, and an environment is readable by every process of that user and is passed on to git's children.

The percent comes from git's `Receiving objects: NN%` lines on stderr (separated by carriage returns; read with a 4 KiB line cap; other phases show no number). No git text goes to the phone or to the log.

### 6.6 The login for git in that folder

One helper serves the clone, and later `git pull` and `git push` in that folder: `<pterm> github-credential`. It asks the broker over the local admin channel for the access token and prints it in git's credential format with `password_expiry_utc`, so that git does not keep it. It answers only for `protocol=https`, `host=github.com`. The admin channel checks that the caller is the same user (`internal/ipc/peer_*.go`); such a process can already pair a device there, so this widens nothing. The refresh token never leaves the broker.

After the clone the broker writes into that repository's own config, in this order:

```
credential.https://github.com/<owner>/<name>.git.helper =
credential.https://github.com/<owner>/<name>.git.helper = !<pterm> github-credential
```

The first, empty line makes git forget the helpers from the user's system and global config **for this repository's address only**, so the 8-hour token is never sent to the macOS Keychain or to `~/.git-credentials`, and never goes stale there. The cost, stated in the docs: in a folder that Termivoa cloned, git uses Termivoa's login for that repository. That is why the app asks for read and write (section 5). A folder that was already there (`alreadyHere`) is **not** changed; it keeps the user's own login.

After **Disconnect** the line stays. The helper then prints one line on stderr: "Termivoa is not connected to GitHub on this computer. Connect in Settings → Integrations." and git fails with its own message.

When that repository has no `user.name`, and the user has none in the global config, the broker sets the repository's own `user.name` to the login and `user.email` to GitHub's no-reply address for that account, so the first commit does not fail on a new computer.

### 6.7 The outbound client

A second client, built like the Web Push one: hosts `github.com` and `api.github.com` only, TLS, no proxy, no redirects, public addresses only, 5 s dial, 10 s per request, 1 MiB per answer, a `User-Agent` with the Termivoa version. In tests the two base addresses are fields of the client struct. The e2e harness sets them through a `//go:build e2e` file and a `PTERM_E2E_*` variable, the pattern of `cmd/pterm/lifecycle_e2e.go` and `internal/broker/peerdial_e2e.go`; that file also relaxes "public addresses only" for the fake. A release build has no such file, so nothing can change the addresses.

## 7. Protocol (revision 7, feature `github`)

| Route | Body | Answer | Limit for each phone |
| --- | --- | --- | --- |
| `GET /api/v1/github` | | `state`, `login`, `connect` (code, expiry; only for the phone that started it), `installUrl`, `setup` (repo, phase, percent, problem, projectId) | 60 a minute |
| `POST /api/v1/github/connect` | | the same object | 3 a minute; one connect at a time |
| `DELETE /api/v1/github/connect` | `{}` | | 10 a minute. Cancels a connect that is not finished; it never touches a stored login |
| `DELETE /api/v1/github/account` | `{}` | | 10 a minute. **Disconnect** |
| `GET /api/v1/github/repos` | | at most 1,000 rows, each with `here` and `taken` | 10 a minute |
| `POST /api/v1/github/setup` | `{ repo }` | `started`, `alreadyHere` + projectId, or a problem | 3 a minute; one setup at a time |
| `DELETE /api/v1/github/setup` | `{}` | | 10 a minute. Stops the clone |

`state`: `unavailable` (Windows, or a build with no client ID), `noGit`, `notConnected`, `connecting`, `needsInstall`, `ready`.

Problems (fixed words; the phone has the text): `nameTaken`, `noSpace`, `tooManyProjects`, `notFound`, `cloneFailed`, `stopped`, `timeout`, `githubUnreachable`, `notConnected`.

`HTTPSettingsResponse` gets `github: { state, login }` so the row needs no second request (no repository count: it would cost GitHub calls on every settings read). Schemas in `protocol/v1`, generated Go and TypeScript, `FEATURE_REVISION.github = 7`. A computer below revision 7 shows "Update Termivoa on pi to use GitHub."

Stored data (as built: a 0600 file `github.json`, not a table, so no computer needs a fresh database; section 17): login, account ID, access token, its expiry, refresh token, its expiry, updated time. No database change.

## 8. Existing folders ("drop" = skip)

| On the computer | Result |
| --- | --- |
| No `~/<name>` | Clone |
| `~/<name>` is the same repository | No clone. It becomes a project (if it is not one) and the list says "on mac" |
| `~/<name>` is another repository, a plain folder, a file, or a symlink | "Another folder named notes is on mac." Nothing is changed |
| The same repository is a saved project at another path | "on mac". No clone |
| The same repository is at another path and is **not** a project | Not seen (the broker does not search the disk). The clone goes to `~/<name>` if that name is free |
| A repository whose name starts with `.` or `-` | Not offered: "Termivoa cannot set up a repository with this name." |
| Two owners, one repository name | The first gets `~/<name>`; the second says "name taken". Version 1 has no other target folder |

One hazard is stated in the docs: a folder that says its `origin` is that repository becomes a project by a tap, and **Changes → Push** then runs git with that folder's own config. The folder must be owned by the user and already be in their home folder, so this gives a paired phone nothing it cannot do by typing.

## 9. Every case

| Case | The user sees | Action |
| --- | --- | --- |
| Already connected | The account name and **Set up a repository** | |
| The code is not used in 15 minutes | "The code ran out." | **Get a new code** |
| Cancel on GitHub | "GitHub did not connect." | **Try again** |
| Signed in, no repository chosen | "Termivoa sees no repository yet." | **Choose on GitHub** |
| Org member who is not an owner | The same line; GitHub sends the owner a request | **Check again** |
| Access removed on github.com, or 6 months unused | "GitHub is not connected on mac now." | **Connect** |
| Something else used the login | "Another program used this login. Revoke Termivoa on GitHub, then connect again." | The link to GitHub |
| The repository uses Git LFS | "Large files were not downloaded. Run `git lfs pull` in a session." | |
| Push that changes a workflow file | git's own refusal from GitHub | Documented in troubleshooting |
| A second phone opens the row during a connect | "Connecting from another phone…" | |
| No internet, GitHub down, rate limit | "mac cannot reach GitHub." | **Retry** |
| git is missing | "git is not on pi. GitHub needs it." | **Install git** (a session with the command typed, not run), **Check again** |
| Repository already there | Tag "on mac" | **New session** |
| Another folder has the name | "Another folder named notes is on mac." | |
| Too little disk space | "mac has too little free space for this repository." | |
| Large repository | The size shows in amber in the row; the confirmation names it | |
| The clone fails or **Stop** | "The clone stopped. Nothing was left on mac." | **Try again** |
| The phone locks during a clone | The computer continues; the row and the sheet show the result | |
| The broker restarts during a clone | The temp folder is removed at start; the result is `cloneFailed` | **Try again** |
| The repository was renamed or deleted after the list | "That repository is not on GitHub under this name now." | The list reloads |
| 200 projects already | "mac has the most projects Termivoa keeps (200)." | |
| A computer with an old Termivoa | "Update Termivoa on pi to use GitHub." | |
| Windows computer | No GitHub row for it | |

## 10. Security

What changes in the threat model (the ADR lists the exact sentences):

| Today | After |
| --- | --- |
| "No browser administrative routes are allowed for … registering project roots" | One exception: the setup route may register `~/<name>` after the broker itself cloned it, or after the checks of 6.4 step 2. The phone never names a folder |
| "Web Push adds the only outbound connection the broker makes on its own" | A second one: GitHub (`github.com`, `api.github.com`). It starts when a user taps **Connect**; after that the broker also calls GitHub to refresh the token, for the list, and when git on that computer asks for the login |
| The admin channel returns no secret | It returns the GitHub access token (never the refresh token) to a process of the same user |
| "The broker downloads nothing" | The broker downloads a repository that the user chose, with git. It still downloads no program and runs nothing from the clone |
| The database holds the VAPID key | It also holds one GitHub token pair that can read and write the chosen repositories. Any process of that user can read it, as it can the VAPID key and a `gh` login file. A backup of a computer that is not used holds a live refresh token for up to 6 months |

| Threat | Answer |
| --- | --- |
| A paired or stolen unlocked phone | It can already type any command. New: it sees repository names (also private ones) and can fill the disk up to the bounds. It cannot name a folder, delete a folder, or read the token |
| A hostile repository | No hooks, no templates, no submodules, no LFS download, no filters from global config, objects checked, https to github.com only, and no name that starts with a dot. Nothing from the clone runs. Opening an agent in it is the user's act, as with any folder |
| Code phishing | The code is shown only in the sheet that started it; the sheet's warning; GitHub's own page names the app. A user who enters an attacker's code gives the attacker lasting access to all chosen repositories, and Termivoa shows no sign of it; only GitHub's list of authorized apps does. This is true of every device-flow login (also `gh`) |
| A copied client ID | Section 5 |
| Token theft from the computer | 8 h access; the refresh token is single-use, so a thief's refresh makes the owner's next refresh fail, and the row then says "Another program used this login…" (6.2). The thief keeps a working pair until the owner revokes Termivoa on github.com. The scope is the chosen repositories, read and write |
| The gateway | Never sees the token. It forwards the routes of section 7 like the others |
| Logs | The action and a fixed problem word. No token, no code, no git text, no repository name |

## 11. Bounds (rule 11)

| Thing | Bound |
| --- | --- |
| Connect | One at a time for each computer; 15 minutes; poll at GitHub's interval |
| Installations read | 10 |
| List rows | 1,000; 1 MiB a page; 15 s in total; cache 60 s; one shared run |
| Setup | One at a time for each computer; 30 minutes; disk checks before and during |
| Repository text | 39 + 1 + 100 characters |
| Request rates | Section 7 |
| Result kept | One, for 10 minutes |
| git's stderr | Read in lines of at most 4 KiB; nothing is kept but the last percent |
| Goroutines | The connect poll and the setup job end on cancel, on their time limit, and when the broker stops |

## 12. Coding plan (after approval)

Two pull requests. Each ships something a user can do, with its tests and all documents of rules 22 and 25.

| PR | The user can | Host | Web | Tests |
| --- | --- | --- | --- | --- |
| **1 Connect and list** | Connect, see the account, see the repository list with search, "on mac" and "name taken", disconnect | The GitHub client (6.7); the device flow; the token store, refresh and revoke; the `github_account` table; the connect, account and repos routes; the settings field; revision 7; `pterm doctor` line; `pterm github status` and `pterm github disconnect` for a user at the keyboard | The rows in **Integrations** (also with no herdr); the code sheet; the choose step; the list sheet with search (the pattern of `Files.tsx`); the optional caps line in `SheetHeader` | Unit, with an `httptest` GitHub: every poll error, `slow_down`, expiry, cancel against a finished connect, refresh rotation, a lost answer and the one retry, the unexpected `bad_refresh_token` line, revoke on disconnect, redaction; paging, caps, cache, one shared run; `here` and `taken` for https and ssh `origin`, a symlink, a file, a name that differs in case. e2e: a `fakegithub` in the harness (build tag `e2e`), two computers with two accounts |
| **2 Set up** | One-tap clone with progress and **Stop**, "Ready", pull and push in that folder, **From GitHub** next to **Add a folder** | The setup job and its routes; the clone (6.5); `pterm github-credential` (6.6); the exclusive rename; the start-up sweep; the project add; the commit name; the threat-model change | Confirm (with the size when it is large), progress, ready, every problem line, the link in **New workspace** | Unit: each row of section 8; the name rules (`.ssh`, `-x`, `a.git`); the rename under a race (a folder made in the meantime is not replaced); the child's environment and config are exactly the fixed lists and hold no token; a global `store` helper and a fake `osxkeychain` get nothing; a clone of a local fixture served over HTTP by the fake; stop and the time limit leave no folder. e2e: clone, lock the phone, result; `nameTaken`; stop; push from the clone |

Lab notes before the code that depends on them (in `docs/evidence/github-integration/`, with the real app installed on one empty test repository; no real code):

| Item | PR |
| --- | --- |
| The device page and the install page in iPhone Safari and in the Home Screen app; does the app come back in the same step | 1 |
| A second try with the same refresh token after a lost answer | 1 |
| The git user name for a user access token | 2 |
| `password_expiry_utc` and the reset line: nothing is stored in the Keychain or in `~/.git-credentials` | 2 |

Size (the first plan's estimate plus the login): about 2,700 lines for PR 1 and 1,700 for PR 2, tests and docs included.

Documents each PR updates: `CHANGELOG.md`, `README.md`, `docs/progress.md`, `docs/architecture.md`, `docs/threat-model.md`, `docs/pending-work.md`, `protocol/v1/http-api.md`; docs site: a new guide `guides/github.md` and its sidebar entry, `reference/security.md`, `reference/troubleshooting.md` (the code ran out, no repository in the list, "another program used this login", LFS, a workflow push, a pull after **Disconnect**), `reference/release-notes.md`, `reference/cli.md`; the landing page (`site/public/`, a GitHub line; it is not a provider, so the Providers section is not touched); a picture script `scripts/docs-screenshots/github.mjs` with demo data; the site deploy after each merge.

## 13. Not in version 1

A branch choice; another target folder (so: one repository for each name); create a repository; pull requests; reuse of a `gh` login; the Workflows permission; GitHub Enterprise; Windows; one login for all computers (it would move a secret between computers); keychain storage; a notification when a clone ends; a search of the disk for clones at other paths.

## 14. What the owner must do

1. Approve or change the decisions of section 4.
2. Decide push (section 5). The design asks for Contents: Read and write.
3. Register the GitHub App with the settings of section 5 (about 5 minutes, free), install it on one empty test repository, and give the client ID and the app's slug. Both are public values.
4. Accept a fresh database and a new pairing on each computer when PR 1 lands (section 7).

## 15. Review of plan 1 (each computer's `gh` login)

Two independent reviews (security; feasibility and alternatives), 2026-10-06. Both: build with changes. The owner then rejected plan 1 ("there is no sense of computer has installed or not gh"), so only the findings that still apply are listed.

| Finding | In this design |
| --- | --- |
| The route that adds a project breaks a threat-model sentence; the ADR must change it by name | ADR 0010; section 10 |
| The clone must not inherit the user's environment or global git config | 6.5 |
| "Remove what we created" can delete a folder the user made in the meantime | Temp folder and an exclusive rename, 6.4 |
| The disk bound from GitHub's size is weak | A check before, and the temp folder's size during, 6.5 |
| Check the one repository at GitHub, not a fresh full list | `GET /repos/owner/name` and `full_name`, 6.4 |
| Read `origin` with `git config --file`, never `git -C` | 6.3 |
| The owner's largest repository is about 5 GB; do not refuse by size | The size shows; the disk check decides |
| The clone root: the owner's repositories are in `~` | `~/<name>` |
| A provider session stops 10 s after the viewer leaves | A job in the broker |
| Each computer owns its action; no merged list | One row for each computer |

## 16. Review of this design

Two independent reviews of this document and ADR 0010, 2026-10-06. Both: **approve with changes**. All changes are made above.

### Security review

| # | Finding | Severity | Resolution |
| --- | --- | --- | --- |
| S1 | `GIT_ASKPASS=<pterm> github-askpass` cannot work (git runs it as one path), and a token in the environment is readable and inherited | Blocker | Removed. One helper through git config; no token in any environment (6.5, 6.6) |
| S2 | "Termivoa's helper is asked first" is false; global helpers are asked first and then **store** the token (Keychain, `~/.git-credentials`) | Blocker | The reset line scoped to the repository's address, and `password_expiry_utc`; a test with a global `store` helper (6.6, section 12) |
| S3 | "Cannot revoke" is wrong: `POST /credentials/revoke` needs no authentication | Should-fix | **Disconnect** revokes both tokens; `secure_delete` (6.2) |
| S4 | "A visible sign" overstates: after a thief's refresh the owner sees the plain "not connected" line, connects again, and the thief keeps a pair | Should-fix | Its own line and the revoke link; the ADR says that reconnecting does not cut off a thief (6.2, section 10) |
| S5 | A repository named `.ssh`, `.config` or `.aws` lands in the home folder; the owner pattern allowed `..` | Should-fix | No leading `.` or `-`; the owner pattern has no dot (6.4) |
| S6 | The lost-pair window is not milliseconds: a timeout after GitHub rotated loses it too | Should-fix | Said so; one retry; a lab item (6.2) |
| S7 | `os.Rename` replaces an empty folder in a race | Should-fix | `renamex_np(RENAME_EXCL)` / `renameat2(RENAME_NOREPLACE)` (6.4) |
| S8 | The config list lacked `transfer.fsckObjects`, `http.proxy=`, `--template=`, and a clean `HOME` (`~/.netrc`, global attributes) | Should-fix | Added; a minimum git version (6.5) |
| S9 | `DELETE /github/connect` meant cancel and disconnect; a cancel that races a finished connect deletes the login | Should-fix | Two routes (section 7) |
| S10 | ADR wording: "only after Connect" is not true for refreshes and git; the admin channel now returns a secret; a phished code gives lasting access with no sign; a backup holds a refresh token; the landing page is missing | Should-fix | Section 10, ADR 0010, section 12 |
| S11 | The start-up sweep could delete the other profile's clone; no cap on git's stderr; the poll must end with the broker; LFS leaves pointer files; redaction needs a type; resolve the home folder's symlinks | Nit | 6.4, section 11, section 9, 6.2 |

Held under attack: the SQLite storage (the same exposure as the VAPID key and a `gh` login file); the admin channel (it checks the peer user, and such a process can already pair a device); hostile repository content (it cannot send the token off github.com; a clone cannot deliver `.git/config`); one cache for all paired phones (all are the owner's); the GitHub rate budget (about 700 calls an hour at the worst); fixed hosts with no pinning; `http.followRedirects=false` (it breaks only renamed repositories, which the `full_name` check already refuses).

### Feasibility and experience review

| # | Finding | Resolution |
| --- | --- | --- |
| F1 | 6.6 is wrong on order (the same as S2); use one helper for clone and pull; say what happens after **Disconnect** | 6.5, 6.6 |
| F2 | Read-only will feel broken: the VPS cannot push, and the reset line stops push on the Mac in cloned folders. A later permission change makes every user approve again | The design asks for Read and write; the owner decides (section 5) |
| F3 | A new table needs a fresh database and a new pairing on every computer; the design did not say so | Sections 7 and 14 |
| F4 | On the owner's Mac no row would say "on mac" (27 repositories in `~`, one project); the picture drew "name taken" that nothing computed | `here` and `taken` for each row with one `Lstat` (6.3) |
| F5 | The pictures did not match the app: boxed cards, a cyan switch, a herdr card without its switches, one card for all computers, long footers; **Integrations** is hidden with no provider | Redrawn to the app's parts: one flat row for each computer, the mono state word, the bone button, one short line under each sheet; the section shows with no herdr (section 2) |
| F6 | Nothing returns the user from GitHub; the choose step needed a tap; the sheet must survive a reload on iOS | The Setup URL page; the broker looks again by itself; the sheet is drawn from the state (section 5, 6.1) |
| F7 | The entry is only in Settings | **From GitHub** next to **Add a folder** (section 2, PR 2) |
| F8 | PR 1 alone gave the user nothing to do; the plan lacked `pterm doctor`, a CLI, the landing page, a picture script, the e2e injection | Two PRs; the items are in section 12 |
| F9 | A new computer has no commit name, so the first commit fails | Set in the clone when none exists (6.6) |
| F10 | The two steps are needed (the one-step install flow needs the client secret); a `gh` computer entering one code is fine; `DELETE` routes work with a `{}` body; progress needs `--progress` and only the "Receiving objects" phase has a useful percent | Kept; section 7; 6.5 |

One incident in this review: the feasibility reviewer ran `git credential fill` on the owner's Mac to learn the helper order, and the Keychain helper printed the owner's real `gh` token into that reviewer's session log. It was not used and is not in this repository. The owner was told at once and advised to make a new token. Reviewer briefs now forbid every command that can read a credential; the same test belongs in a temp `HOME` with a fake helper (section 12).

## 17. Build notes (2026-10-07)

What the build does differently from sections 1–14, and why. Each line was checked against the code.

| Design | Build | Why |
| --- | --- | --- |
| One table `github_account` in the database (section 7) | One file `github.json` (mode 0600) in the broker's data folder, written with a 0600 temp file and a rename; deleted by writing zeros over it, then removing it. A damaged file reads as "not connected" | The schema file is checksummed: a new table would have forced every computer to delete its database and pair the phone again. The file has the same exposure as the database (same user, same folder) |
| `secure_delete` on the table (6.2) | Zeros over the file before the remove | No table. The ceiling is the same: a copy-on-write file system or a backup can keep old blocks |
| Six routes (section 7) | Three: `GET /api/v1/github`, `POST /api/v1/github` with `action` (connect, cancel, disconnect, setup, stop), `GET /api/v1/github/repos` | One action route is the pattern of herdr manage; fewer routes, the same limits for each action |
| State `unavailable` | No GitHub field at all in the settings when the build has no app or the computer is not macOS or Linux; the phone shows no row | The phone cannot tell "no app" from "old Termivoa" anyway |
| Problem word `githubUnreachable` | `gitHubDown`; connect problem `codeExpired`; set-up phase `finished` | The generated Go names of `expired`, `unreachable` and `done` clashed with existing protocol names |
| No field for a failed revoke | `revokeFailed` in the disconnect answer; the row then links to GitHub's settings page | Design 6.2 asks the sheet to say when the revoke failed |
| `GIT_ASKPASS` (already removed by the review) | The helper value is `!'<pterm>' github-credential`; a development broker writes `--dev --runtime-dir '<dir>'` after it. For the clone itself the value starts with the broker's own `HOME` and `XDG_*` folders (`!HOME='<home>' … '<pterm>' github-credential`), because git runs there with an empty temp HOME and a production pterm finds its runtime folder from HOME (macOS) or `XDG_RUNTIME_DIR` (Linux). The second review found that a production clone could not reach the broker without them; a test now checks the helper's HOME | A later `git pull` in a terminal must reach the broker that cloned the folder; a development broker has its own runtime folder. A path with a single quote turns the feature off |
| The helper prints a line when not connected | It also answers `quit=1` | Without it git asks "Username for…" in the terminal, because the reset line turned the user's other helpers off for that address |
| — | macOS uses `/usr/bin/git` only when the developer tools are installed | Otherwise running it opens Apple's install window on the computer |
| Install git (section 9) | **Install git** types `if [ "$(uname)" = Darwin ]; then xcode-select --install; else sudo apt-get install -y git; fi` (not run), and **Check again** | The settings answer does not say the computer's operating system, so the command picks it. It needs a POSIX shell (bash, zsh, sh); another Linux uses its own package manager (troubleshooting) |
| "Update Termivoa on pi to use GitHub." (section 9) | Not shown | A computer below revision 7 looks the same as one with no app |
| — | `pterm github status`, `pterm github disconnect`, `pterm doctor` line | Section 12 asked for them |

Known limits found while building (in `docs/pending-work.md`):

- A refusal from a computer other than home shows the app's general "That computer could not do this." (existing app behaviour for every peer error), so "GitHub is busy…" shows only for home.
- An organisation member who is not an owner sees an empty list until an owner approves the app; the sheet offers **Choose on GitHub**.

## How are old push timestamps formatted? (#215)

Repository metadata uses the same elapsed-age helper as session metadata
and computer last seen (`Sessions.age`). `pushedText` adds `ago`, except
for `now` (**just now**) and **yesterday**. Repository rows calculate their
metadata once per list read (`repos.at`), so typing in Search adds no time
formatting work. Host timestamps and the list's caching/authorization are
unchanged.

| Elapsed age | Label |
| --- | --- |
| Under one minute | `just now` |
| One minute to under one hour | Whole `min` |
| One hour to under 24 hours | Whole `h` |
| 24 hours to under 48 hours | `yesterday` |
| Two days to under seven days | Whole `d` |
| Seven days to under 30 days | Whole `wk` (seven-day units) |
| 30 days to under 365 days | Whole `mo` (30-day units) |
| 365 days or more | Whole `yr` (365-day units) |

This is approximate elapsed-time display, not calendar arithmetic.
Future times retain **just now**; an absent timestamp omits the date.
Tests cover each threshold and the preceding millisecond, old/leap-year
spans, future/unknown/invalid ISO inputs, timezone equivalence and
non-finite clocks. SSR tests prove the actual GitHub repository row and
SessionRow display the new units. See
[verification](evidence/issue-215/verification.md).
