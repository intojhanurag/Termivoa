---
title: Security
description: How Termivoa keeps your computer's terminal private over Tailscale, what it stores and does not store, and how to report a vulnerability.
lastUpdated: 2026-10-11
---

Termivoa keeps your terminal reachable only from your own Tailscale devices, lets only phones you approve connect, and never saves terminal content to disk. There is no telemetry and no Termivoa cloud service; only notifications pass through your phone's push service (Apple or Google), encrypted, and a computer that you connect to GitHub talks to GitHub.

## Who can reach the page?

- The page listens only on loopback (`127.0.0.1`), so other machines cannot reach it directly.
- Tailscale Serve offers it as private HTTPS in your tailnet (your private Tailscale network).
- `pterm setup` asks before it changes Tailscale Serve settings, and never overwrites another service.
- `pterm setup` refuses to run while other Tailscale Serve handlers share the Termivoa address (port 443), and `pterm doctor` reports them. Such a handler is the same origin as the app, so it could act as the app. Handlers on other ports of the same computer are other origins, but browsers send them the Termivoa cookie, so `pterm doctor` warns about them. Termivoa's own preview port 8443 removes the cookie.
- `pterm` runs the Tailscale CLI only when no other user can replace it (the file and every folder up to `/` belong to root or to you and are not writable by others), and checks this again before each run. `pterm doctor` says why it refused one.
- An `/api` request must say where it comes from (`Origin` or `Sec-Fetch-Site: same-origin`), so a page on another computer in your tailnet cannot read your API with your cookie. This needs iOS 16.4 or later on iPhone, also with one computer.
- Termivoa **never** uses Tailscale Funnel or any public tunnel, which would put your terminal on the public internet. `pterm doctor` and `pterm launch` warn about Funnel. `pterm setup` and `pterm pair` refuse to run while Funnel is on for port 443 or 8443, also a Funnel that runs in the foreground (`tailscale funnel` without `--bg`), and print how to turn it off.

## How does pairing work?

- A pairing link works once and expires after two minutes.
- Only the computer can approve a phone, after you compare the phone's code. A phone cannot approve itself.
- A paired phone gets a secure cookie that scripts on other sites cannot read or use.
- Access ends after 30 days, or after 7 days without use.
- `pterm revoke <device-id>` removes a phone at once, also on open connections.

## How do several computers trust each other?

:::note
Not tested on a real iPhone or Android phone yet. See [Use several computers](/docs/guides/computers/).
:::

With [several computers](/docs/guides/computers/), one computer (home) serves the app, and the app talks directly to the others (peers).

- **Each peer decides for itself.** You approve the phone on the peer with `pterm pair --via <home>` after you compare codes. Each peer gives the phone its own cookie, which scripts cannot read and no other computer can use.
- **A peer accepts the phone only from that home.** The approval binds the phone's access to home's exact address. The same phone cannot use the peer from another app address.
- **Same tailnet is not enough.** At pairing, the peer looks up home in its own Tailscale status: home must have the same Tailscale owner (compared by Tailscale user ID, not by login name), or, for a tagged node, you must allow it with `--allow-tagged`. The peer saves home's node ID and its tags. If that ID changes (for example a reused name, or Tailscale reinstalled on home), the peer revokes the phones that used it. It still lets the app at that address read its answers, so the app shows "pair again" instead of "can't reach"; every answer is a refusal. If home's tags change (added, removed or replaced), the peer refuses those phones until they pair again.
- **Trust loss closes connections at once.** When the peer reads a new Tailscale status that no longer trusts home, it closes the live connections from home's app at once. If Tailscale restarts or logs out, the peer keeps its last good status; after 10 minutes without one, it refuses requests from home's app until it can read it again.
- **Home pins each peer too.** Home saves each peer's Tailscale node ID. If another machine takes the peer's name, home drops the peer from the list and from the app's allowed addresses, and you must add it again.
- **Nothing goes through home.** Home stores only each phone's list of computer addresses and names. It holds no peer secret.
- **The app checks what a peer sends.** The app checks every answer from a peer and never shows a peer's error text as its own. The one exception is a dev-server preview: there the peer chooses the page, so the app marks it **untrusted page**. See [Can a preview from another computer harm the app?](#can-a-preview-from-another-computer-harm-the-app)
- **You can cut a home off.** `pterm revoke --via <home address>` on a peer ends the access of every phone that uses it from that home, and deletes that home's pairing links and pending pairings.
- **Log out reaches every computer.** **Settings → Log out** signs the phone out of every computer it can reach, and names the ones it could not (run `pterm revoke` there).

:::caution
**If home is hacked, every computer you added through it is hacked too.** Home serves the app's code, so it can use every peer, even with the app closed, and it can trick you into pairing again. Choose as home an always-on computer where you do not run untrusted code. If it happens, follow [What do I do if home was hacked?](/docs/guides/computers/#what-do-i-do-if-home-was-hacked)
:::

## What does Found on your network share?

:::note
Not tested on real computers or a real phone yet. See [How do I add a computer?](/docs/guides/computers/#how-do-i-add-a-computer)
:::

When you add a computer with **Add** in **Found on your network**, the new computer sends its one-time invite to your home computer, and you still approve on the new computer.

| What | Who gets it |
| --- | --- |
| The new computer's one-time invite (a secret of the same kind as its link, but a different one) | Your home computer, the one you named in `pterm pair --via` or `pterm setup`. It goes over Tailscale's encrypted connection, only while the command waits in a terminal (up to 10 minutes). Home keeps it in memory for at most 60 seconds per send, never on disk. |
| The list of waiting computers, with their invites | Every phone paired with home. Termivoa treats each of them as yours. |

- **You still approve on the new computer.** The invite only lets a phone ask. Press `y` only when the prompt names this computer ("wants to add this computer (vps)"), and the phone shows the same code and says "type y in the terminal on" the same computer name. If it names another computer, shows another code, or shows nothing, answer `N`.
- **Home shows only your own computers.** Home lists a computer only when its own Tailscale status shows it in your tailnet, with your Tailscale user, and neither computer is tagged. The name in the list comes from home's Tailscale status, not from the sender.
- **Anyone who can reach home in your tailnet can make Add fail for a while** (for example with fake invites, or very many of them). They cannot add a computer or learn anything. The QR code and the link still work.
- **Another of your own computers can show itself in Found.** That is why you check the name and the code before you press `y`.
- **This adds no trust in home.** Home, and every phone paired with it, could already use every link you paste or scan into its app.

## Can a preview from another computer harm the app?

:::note
Not tested on a real iPhone or Android phone yet. See [Dev-server preview](/docs/guides/preview/#can-i-preview-a-session-on-another-computer).
:::

It cannot read the app, but it can lie to you. A [preview](/docs/guides/preview/) of a session on another computer is a page that the other computer chooses.

| The page from another computer | |
| --- | --- |
| Cannot | Read the app, the terminal, your sessions or the app's sign-in. It is kept apart from the app. |
| Can | Show false content, for example a false "pair again" screen. |
| Can, in the worst case | Sign the phone out of your computers. You then pair again. |

- **The app always marks it.** Over every preview from another computer, the app shows the computer's name and **untrusted page**. The page cannot cover or change that bar.
- **Only your computers.** Only computers that you added and confirmed can be shown.
- **Do not type secrets into a preview.** No pairing links, no passwords. The real app never asks for them inside a preview.

## Who can type in a session?

- Only one device at a time controls a session, by holding a lease (a short-lived permission to type).
- Control never moves to a device by itself. A reconnect always starts read-only. Termivoa takes control by itself only to type the start command of a session you just started, and only when no other device has control.
- Typing is never queued or resent after a lost connection.
- Phones never receive the raw terminal byte stream and cannot send raw bytes, only keys, text, pastes, scroll steps and answer-card choices.

## What does the phone see and do with herdr?

A paired phone can already run any command in a Termivoa session, so herdr gives it no new power on the computer.

| | |
| --- | --- |
| The phone gets from herdr | Project and worktree names, the branch names of worktrees that you closed, tab labels, agent kinds and state words. |
| The list and the notifications never have | Terminal titles, screen text, paths or prompts. |
| When you open a herdr terminal | The phone gets that terminal's screen, like any Termivoa session. |
| A herdr notification has | The provider `herdr`, an opaque terminal ID, the fixed title `herdr · <project> · <worktree>` and a fixed text such as `<agent> needs you. Tap to answer.` It is encrypted to your phone, like other [notifications](/docs/guides/notifications/). |

- **Same access as the Sessions list.** The herdr requests need the same pairing cookie and the same check of where a request comes from. Another computer in your list reads its herdr only through its own Termivoa.
- **Socket and program, both yours.** Termivoa uses herdr's own socket to list, read state and create worktrees. It also runs the herdr program: once for each open terminal, and to check what that herdr version can do. It checks that the socket and the herdr program belong to you.
- **Limits for each phone.** Open 20 per minute, create 3 per minute, settings 10 per minute, manage 30 per minute.
- **Manage asks first and never forces.** The phone can close a herdr workspace or one of its tabs, remove a worktree, and make a tab or a workspace in one of your Termivoa projects. A removal is refused when the worktree has changes that are not committed; it does delete files that git ignores, and the app says so before you confirm. The phone sends a project, never a folder path. It can also start a coding agent, with a first prompt that you write, in a herdr workspace that exists, and open a closed worktree again. A closed worktree is named by a branch name that herdr itself lists. Starting an agent counts toward the 3 creates a minute.
- **Termivoa downloads no program.** **Install herdr** opens a session with herdr's own install command typed in; it runs only when you press Enter. **Start herdr** runs the herdr program that is already on the computer, after the same checks.
- **Opening never takes control.** It is read-only until you tap **Take control**.
- **No takeover.** Termivoa never kicks another herdr client off a terminal.
- **No herdr prefix keys.** Termivoa sends `ctrl+b` to the agent as a plain `ctrl+b`, so the phone cannot send herdr's prefix commands. If you set another prefix key in herdr's `config.toml`, a phone with control could send that key; `pterm doctor` warns about it.
- **Off switch.** **On** in the **herdr** part of a computer's page in **Settings** stops Termivoa from reading herdr on that computer.

## What does GitHub change?

When you tap **Connect** on a computer's page in **Settings**, that computer logs in to GitHub with the Termivoa GitHub App. Nothing changes on a computer that you do not connect.

| Question | Answer |
| --- | --- |
| Where is the login? | On that computer only, in a file in Termivoa's data folder that only your user account can read. Each computer has its own login. It never goes to the phone, to another computer or to a log. |
| What can it do? | Read and write the contents of the repositories that you chose on GitHub. It cannot change workflow files. |
| How long does it work? | GitHub's access token works for 8 hours. The computer gets a new one by itself with a refresh token, which works for up to 6 months. |
| What does the computer connect to? | `github.com` and `api.github.com`, over HTTPS. It starts when you tap **Connect**. After that, the computer also connects to get a new token, to read the list, to clone, and when git in a cloned folder asks for the login. |
| What does it download? | Only a repository that you set up. Termivoa runs nothing from it: no hooks, no submodules, no Git LFS download. It never deletes or overwrites a folder. |
| Who on the computer can use it? | git in a folder that Termivoa cloned, through `pterm`. Any program that runs as your user can also get the access token, or read the login file, as it can read a `gh` login. Malware that runs as your user is not covered. |

- **A paired phone cannot read the login.** It can see the names of your chosen repositories (also private ones), clone one into `~/<name>`, stop a clone, and disconnect. It cannot pick a folder, delete a folder, or clone from another site. Lose your phone? Revoke it with `pterm revoke` as usual.
- **Enter a code only when you started Connect yourself.** If someone gets you to enter a code that they started, they get access to your chosen repositories, and Termivoa cannot see it. Only GitHub's list of authorized apps shows it. This is true of every login of this kind, also `gh`.
- **A stolen login works until you revoke it on GitHub.** Connecting again does not stop a thief. When the computer sees that another program used its login, the row says "Another program used this login. Revoke Termivoa on GitHub, then connect again."
- **A backup of the computer holds the login.** It holds a refresh token that can work for up to 6 months. Disconnect before you give a backup away.

### How do I remove Termivoa's access on GitHub?

**Disconnect** on a computer's page in **Settings** (or `pterm github disconnect`) asks GitHub to revoke the login and removes it from the computer. To be sure, or for a computer that you lost, open GitHub's [Authorized GitHub Apps](https://github.com/settings/applications) and revoke **Termivoa**. This ends the login of every computer that you connected. Connect each computer again after that.

Confirmed `pterm uninstall --delete-data` on macOS and Linux tries to revoke this computer's saved GitHub login before deleting it locally. If GitHub does not confirm revocation, Termivoa prints the [Authorized GitHub Apps](https://github.com/settings/applications) link so you can revoke manually. Canceling while revocation is running stops before local deletion. A copied login can remain usable when revocation is unconfirmed; check GitHub rather than assuming it ended.

When Termivoa removes its saved GitHub login, it deletes only its own file name. It does not overwrite the contents. If another file is a hard link to the same contents, that file stays and still holds the login. Local deletion is not secure erasure: copies and backups can keep the login. Use GitHub's [Authorized GitHub Apps](https://github.com/settings/applications) to end access for a copied login.

## What does Chat view send to the phone?

**Next beta.** Termivoa's Chat view reads the coding agent's own conversation file on the computer and sends short, read-only parts of it to a phone that has Chat open. It is off on every computer until you run `pterm chat on` there. This beta adds only the computer side.

| Question | Answer |
| --- | --- |
| Who turns it on? | Only you, at the computer, with `pterm chat on` and a `[y/N]` answer. A phone can only turn it off. |
| Which agents? | Claude Code in a Termivoa session, on macOS and Linux. |
| What does a phone get? | Your prompts, the agent's answers, each tool's name and one summary line, a short preview of tool output (up to 2 KiB and 40 lines), and for an edit the line counts and up to 40 changed lines. |
| What never leaves the computer? | Thinking text, images, attachments, sub-agent conversations, system prompts, token counts, the file's path, process IDs and the agent's session ID. |
| Is it stored? | No. The computer and the phone keep it in memory only, while Chat is open. |
| Who can read it? | Any paired phone that can watch the session, the same as the terminal and **Files**. A small preview in the session list cannot. |
| Are secrets hidden? | Common key shapes (private key blocks, GitHub, OpenAI, Anthropic, AWS, Slack and Google keys, web tokens and `Authorization: Bearer` values) show as `[secret]`. This is not a guarantee: other secrets show as they are. |

- **What is new to the phone.** The terminal shows only part of the conversation. Chat can also show older turns and the start of output that the terminal had folded, including files the agent read outside the project.
- **The computer checks the file first.** It links a session only to the conversation file that the agent itself names, in the agent's own folder, and refuses a link, a file with a second name, a file of another user and a file that others can write.
- **Turn it off** with `pterm chat off`, or from any paired phone. Every open chat closes at once.

## Who can see changes, commit and push?

- Changes only **reads** git: uncommitted changes and untracked files that git does not ignore.
- [Files](/docs/guides/files/) only **reads** the session's project: the top of its git repository, or its folder. Paths cannot leave that folder, not even through a link, and `.git` is never shown. Any paired phone that watches the session can read it, as it could read the terminal.
- **Any paired phone can see these diffs**, including secrets in files that are not ignored. Keep secrets in ignored files such as `.env`.
- Commit and push need **control**, which the computer checks right before git starts.
- A commit includes only ticked files that git lists as changed, and runs your repository's own hooks. Timed-out writes stop git's ordinary hook children before returning; this is cleanup, not a sandbox. Hooks remain trusted programs and can deliberately launch detached programs on macOS or Linux.
- Push **never forces**, uses your normal git and ssh settings, and never asks for a password on the phone. In a folder that Termivoa cloned from GitHub, git uses Termivoa's GitHub login for that repository. See [What does GitHub change?](#what-does-github-change)

## What does Termivoa store?

| Stored | Not stored |
| --- | --- |
| Paired phones, projects, shell profiles, session names, settings | Terminal output and history |
| Notification subscriptions | Your typing |
| The private key that signs notifications to the push service, in the database | Pairing links and phone sign-in cookies in clear text (only a one-way hash is kept) |
| Logs | The GitHub login on the phone or in a log |
| The GitHub login of that computer, if you connected one, in a file that only your user account can read. It is not encrypted. | |

Home also stores each phone's list of other computers (addresses and names), and a peer stores the home address and Tailscale node of each phone that uses it from there. The phone keeps the same list of computers, never their sessions.

**Scan QR** in **+ Add a computer** uses the phone's camera only while you scan. The app reads the camera pictures on the phone to find the QR code. It does not send them to any computer and does not save them. A scanned link is checked like a pasted one, and never adds a computer by itself.

All data is private to your user account ([Where does Termivoa store data?](/docs/reference/update-and-uninstall/#where-does-termivoa-store-data)). Photos go to the project's `.termivoa/uploads/`. Snippets and the Wall tile order (session IDs only) stay on the phone.

## Can a worktree escape its folder?

No. [Worktree](/docs/guides/worktrees/) requests send only a session, a name and a branch. The computer checks them and builds the path under `~/termivoa/worktrees` itself, so the phone cannot pick another folder. Removal needs control, keeps the branch, and asks before it deletes uncommitted changes.

## How do other features stay private?

[Notifications](/docs/guides/notifications/) carry only the session name, project name, status, how long the agent worked and the session ID, encrypted to your phone. [Dev-server previews](/docs/guides/preview/) use a separate address and show only that session's servers; a preview from another computer is marked **untrusted page**. Only the device in control can send [photos](/docs/guides/photo-and-voice/).

With several computers, each computer sends its own notifications straight to your phone's push service; no computer sees another computer's notifications. The app puts the computer's name at the start of the title and text from its own list of computers. That computer writes the words after the name, so a hacked computer can send misleading text or many notifications until you remove it.

## What is not protected?

- A session runs with your normal user permissions. A project folder is not a sandbox (a closed-off area).
- Malware already running as your user is not covered.
- With several computers, malware or a hacked account on home reaches every computer added through it.
- With **Found on your network** anyone who can reach home in your tailnet can make **Add** fail for a while. Use the QR code or the link then.
- A preview from another computer can show false content, and in the worst case can sign the phone out of your computers. See [Can a preview from another computer harm the app?](#can-a-preview-from-another-computer-harm-the-app)
- The app's offline copy keeps the Content Security Policy (the list of addresses the app may talk to) from the time it was saved. While home cannot be reached, an app saved before you removed a computer may still be allowed to talk to it. The removed computer still checks the phone's own access.
- If you change herdr's prefix key from `ctrl+b`, a phone with control of a herdr terminal could send that prefix key to herdr. At worst it detaches the phone. `pterm doctor` warns about it.
- A GitHub code that someone else started, a stolen GitHub login, or a backup of a connected computer gives access to your chosen repositories until you revoke **Termivoa** on GitHub. See [What does GitHub change?](#what-does-github-change)
- The controls have automated tests, but no independent security audit yet.

## Common questions

### What do I do if I lose my phone?

Run `pterm devices` for its ID, then `pterm revoke <device-id>`. It loses access and notifications at once. If it used [several computers](/docs/guides/computers/#what-do-i-do-if-i-lose-my-phone), revoke it on each one.

### Can someone on the internet reach my terminal?

No. Only devices in your tailnet can reach it, and only phones you paired can use it.

### Does this website count visitors?

Yes, only this website (termivoa.com). It counts page views with Vercel Web Analytics, which sets no cookies. The Termivoa app and your computers send nothing to it.

## Report a vulnerability

Do not open a public issue for a security problem.

Email **aojharaj2004@gmail.com**. Start the subject with `[Termivoa security]`.
Include only the minimum safe details. Never send real secrets, pairing links or terminal content.

This is a one-person project. You get a reply within 7 days, and a first assessment within 30 days.

## How are private saved files checked?

Termivoa checks a private file before repairing its permissions.
Linked files, shared hard links, unsafe types and disallowed owners are refused.
Normal owned files keep their identity while their permissions are tightened.

| Boundary | Meaning |
| --- | --- |
| macOS and Linux | A valid private file belongs to your account and is readable and writable only by you |
| Windows | A valid private file has protected permissions for your account; the existing recovery for files created by an elevated Termivoa run remains |
| Linked private file | It is refused before repair can change the linked target |
| Another program in your account | This is not a sandbox against that program; it can already access your private files |

A refusal does not automatically delete the file or create a new login.
Inspect unexpected local-file changes before reconnecting. See [Why is a
private file refused?](/docs/reference/troubleshooting/#why-is-a-private-file-refused).
