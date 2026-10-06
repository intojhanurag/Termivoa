# ADR 0010: GitHub login in the broker, and a phone route that sets up a repository

- Status: **proposed** (2026-10-06). **DO NOT MERGE without owner review** (AGENTS.md rule 24: a stored secret, a new outbound connection, and a change to a threat-model rule).
- Date: 2026-10-06
- Design: [`docs/github-integration.md`](../github-integration.md)

## Context

The owner wants to connect GitHub in **Settings → Integrations**, see his repositories, and put one on a chosen computer with a tap, with no terminal step and no dependency on `gh`. Today a project is added only by `pterm project add` typed in a session; the broker stores no third-party secret, makes one kind of outbound connection (Web Push), and downloads nothing.

A first plan used each computer's own `gh` login and stored nothing. The owner rejected it: a computer without `gh` (his VPS) would need a terminal step, and the user would have to know what is installed.

## Decision

1. **The broker logs in to GitHub itself**, with the device flow of a GitHub App named Termivoa. The client ID is compiled in; there is no client secret.
2. **The broker stores one token pair for each computer** in its SQLite database (mode 0600), as it stores the VAPID private key. The pair never goes to the gateway, the phone or a log. The access token, never the refresh token, is given over the local admin channel to git on that computer (a process of the same user).
3. **The broker makes outbound HTTPS connections to `github.com` and `api.github.com`**, through a client with a host allowlist, no proxy and no redirects. They start when a user taps **Connect**; after that the broker also calls GitHub to refresh the token, for the list, and when git on that computer asks for the login.
4. **A phone route may register one project root**: `~/<name>`, after the broker cloned that repository there, or found that the folder already is that repository. The phone sends `owner/name` and nothing else.
5. **The broker runs `git clone`** with a fixed environment and config (no hooks, no templates, no submodules, no global config, no token in the environment, https to github.com only), into a temp folder that it renames with a rename that cannot replace. It never deletes or overwrites the final name, and it refuses a name that starts with `.` or `-`.
6. **In a folder that Termivoa cloned, git uses Termivoa's login for that repository** (a helper in the repository's own config, after a line that turns off the user's other helpers for that address). The token is not written to the Keychain or to any file by git.
7. **Disconnect revokes** both tokens at GitHub (`POST /credentials/revoke`, which needs no secret) and deletes the row.
8. **Contents: Read and write** is what the design asks for, so that push works in a cloned folder and on a computer with no git login. **This is the owner's decision**; Read-only is the other choice (design, section 5).

## Threat-model sentences this changes

| File | Sentence today | Change |
| --- | --- | --- |
| `docs/threat-model.md:91-92` | "No browser administrative routes are allowed for approving devices, creating pairing grants, registering project roots, or changing startup/network settings." | Add the one exception of decision 4 |
| `docs/threat-model.md:158` | "Web Push adds the only outbound connection the broker makes on its own." | Add GitHub, with its conditions |
| `docs/threat-model.md:432-434` | "The broker downloads nothing" | The broker downloads no program; it clones a repository the user chose |
| `site/…/reference/security.md:112` | "**Termivoa downloads nothing.**" | The same change, in public words |
| (new sentence) | The admin channel returns no secret today | It returns the GitHub access token to a process of the same user |
| `site/…/reference/security.md:7` | "There is no telemetry and no Termivoa cloud service; only notifications pass through your phone's push service" | Add: a computer you connected talks to GitHub |

## Rejected

- **Each computer's `gh` login** (plan 1). No stored secret, but a terminal step on every computer without `gh`, and the user must know what is installed.
- **Both paths** (`gh` when it is there, our login when not). Two sets of code, tests and edge cases to save one code entry.
- **An OAuth App.** One step less, but the `repo` scope (read and write on every private repository) and a token that never expires.
- **Login on the phone**, with the phone listing repositories. The computer still needs a credential to clone, and a token would live in the browser.
- **One login for all computers.** A secret would travel between computers, and one compromised computer would hold the key of all.
- **A path or a URL from the phone**; **delete or overwrite of an existing folder**; **recursive submodules**.
- **The clone in a provider session.** Provider sessions stop 10 s after the last viewer leaves.

## Consequences

- "The broker stores no third-party secret" and "Web Push is the only outbound connection" stop being true. The security page must say so in plain words.
- A process of the same user on a connected computer can get a token that reads and writes the chosen repositories for 8 hours, and can read a refresh token from the database. The same is true today of a `gh` login file.
- A stolen refresh token works until the owner revokes Termivoa on github.com. Connecting again does not cut off a thief; the app says "Another program used this login" when it sees the sign.
- A user who enters a code that an attacker started gives the attacker lasting access to the chosen repositories, and Termivoa cannot see it. This is true of every device-flow login.
- A backup of a connected computer holds a live refresh token for up to 6 months.
- Every computer needs a fresh database and a new pairing when the first pull request lands (the schema file is checksummed; rule 26: no migration code).
- The owner must register and keep a GitHub App. Its client ID is public and can be copied; the design's answer is in its section 5.
- A new GitHub outage mode exists; nothing else in Termivoa depends on it.

## Sources (GitHub docs, read 2026-10-06)

- <https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app>
- <https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens>
- <https://docs.github.com/en/apps/using-github-apps/installing-a-github-app-from-a-third-party>
- <https://docs.github.com/en/rest/apps/installations>
- <https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app>
- <https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/differences-between-github-apps-and-oauth-apps>
- <https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/best-practices-for-creating-a-github-app>
- <https://docs.github.com/en/rest/credentials/revoke>
- <https://git-scm.com/docs/gitcredentials>

## Reviews

Two independent reviews of plan 1 and two of this design (security; feasibility and experience), all on 2026-10-06. Findings and resolutions: [`docs/github-integration.md`](../github-integration.md), sections 15 and 16.
