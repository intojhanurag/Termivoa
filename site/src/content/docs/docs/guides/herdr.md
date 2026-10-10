---
title: Termivoa + herdr
description: Termivoa shows your computers' herdr projects on your phone, buzzes when an agent is blocked or done, opens herdr terminals, and manages workspaces.
lastUpdated: 2026-10-10
---

Termivoa shows the projects of your computers' herdr on your phone, so you can see which coding agent is blocked, open its terminal and answer it.

:::note[Next beta]
This page describes features that are not in `v0.1.0-beta.3` yet. Update Termivoa on the computer to the next beta to get them.
:::

## What is herdr?

[herdr](https://herdr.dev) is a terminal workspace tool, and the first [provider](/docs/providers/) that Termivoa supports. It runs coding agents in workspaces, tabs and panes. Termivoa reads each computer's herdr and shows its projects on the phone. It opens one herdr terminal at a time in [Live](/docs/guides/live-terminal/).

A computer without herdr shows no herdr tab, and nothing changes.

## What do I need?

| Need | Detail |
| --- | --- |
| herdr 0.8.2 or later | Installed on that computer: Homebrew `herdr`, or in `~/.local/bin` or `~/.cargo/bin`, or on the `PATH` that Termivoa runs with. |
| herdr running | Its server must run. If it does not, tap **Start herdr** on the phone. Only the default herdr session shows. Named herdr sessions do not show. |
| macOS or Linux | Windows is not supported. |
| Termivoa from the next beta | On that computer. |
| A setting | None. Termivoa finds herdr by itself within about 30 seconds, also when you install herdr while Termivoa runs. The phone shows it the next time you open Termivoa or switch back to it. |

## What do I see on the phone?

On the Sessions list, a **herdr** tab sits between **Sessions** and **Needs you**. A number on the tab counts the terminals that are blocked or done, on the computer you chose in the [side menu](/docs/guides/computers/#how-do-i-switch-between-computers) (or on all computers in **All computers**).

![The herdr tab in Sessions with All computers: the status line 1 blocked · 1 done · 2 working, the tabs Sessions, herdr 2 and Needs you 1, the header mac · 0 sessions · 1 working over the project shop with 1 blocked and a + button, its worktrees shop (working · 10s, claude · 2 tabs), silver-meadow (blocked · 10s, claude · tab 1) and lucky-valley (done · 10s, codex · tab 1), the button New workspace on mac, then the header vps · 0 sessions · 1 working over the project acme-web with 1 working, and New workspace on vps.](../../../../assets/docs/phone-herdr-tab.webp)

In the **herdr** tab:

- One row for each project (a git repository). Tap ▸ or ▾ to fold or unfold it.
- Under a project, its worktrees, with the main checkout first. A workspace that is not in git is one row. A git project with only its main checkout is one row too, and keeps its **+**.
- With more than one computer and **All computers** chosen, each computer's projects are under a header with its name and state, such as **mac** and **2 sessions · 1 working**.
- A folded project with several worktrees says **2 worktrees**.
- A line under a worktree names the agent and the tab, for example `claude · tab 1`. The list never shows terminal titles or screen text. You see a terminal's screen only when you open it.

A state word shows on the right of a row:

| Word | Meaning |
| --- | --- |
| **blocked** (amber) | The agent waits for you. |
| **done** | The agent finished. It stays until someone looks at it in herdr on the computer. A done row that you opened on the phone is dimmed. |
| **working** | The agent works. |
| **idle** | The agent is idle. |
| no word | The state is unknown, or there is no agent. |

A project shows its most urgent word with a count, such as **1 blocked** or **2 working**.

The status line says, for example, `1 blocked · 1 done · 2 working`, or `herdr · nothing running`. When something is blocked, the button **Open the blocked agent** opens the first blocked agent.

The **Sessions** tab also lists the herdr worktrees that have an agent with a state, under **herdr**, above **Termivoa sessions**. In **All computers**, they are under each computer's header.

### What if herdr has a problem?

The problem shows on the **herdr** tab only. The Sessions list never waits for herdr.

| Message | What to do |
| --- | --- |
| **herdr is not running on mac** | Tap **Start herdr** on that notice. If there is no button, start herdr on that computer. Its projects then show here. |
| **Update herdr on mac** | This version is too old for Termivoa. Update to herdr 0.8.2 or later. |
| **herdr on vps · can't reach** | Tap **Retry**. |

## How do I open a terminal and answer?

1. In the **herdr** tab, tap a worktree row.
2. The herdr terminal opens full screen in Live. The tabs of that herdr workspace show as chips on top. **‹ Sessions** goes back.
3. It opens read-only. Tap **Take control** to type. See [Take control to type](/docs/guides/live-terminal/#take-control-to-type).

![A herdr terminal in Live: the chip 1 next to ‹ Sessions, the line mac · herdr shop · silver-meadow Read-only, the note mac shows this terminal at phone size while it is open here., an agent asking Run the cart tests? with the answers 1. Yes, 2. Yes, and don't ask again and 3. No, tell the agent what to do (esc), the words No one has control, the line You are watching. The agent keeps working., and the buttons Take control, Changes and Sessions.](../../../../assets/docs/phone-herdr-live.webp)

The keys, typing, compose, snippets, answer cards and Changes work as in any session. These are not offered for a herdr terminal: History, rename, end session, photos, project buttons that start a new session, preview and the worktree menu.

A tab without an agent opens too (herdr 0.8.2 and later). If herdr cannot open it, its chip is off (greyed). Update herdr to open that tab.

If the herdr pane closes while you watch, Live says **Closed here** and "This terminal is no longer open on this phone. Its row in Sessions stays." Tap **Open again**.

## When does the phone buzz?

The phone buzzes when a herdr agent is blocked or finished. Turn on notifications first: see [Turn on notifications](/docs/guides/notifications/#turn-on-notifications).

| When | Text |
| --- | --- |
| An agent is blocked | `<agent> needs you. Tap to answer.` |
| An agent finished after work | `<agent> finished after <N> min.`, or `<agent> finished.` under a minute |

- The title is `herdr · <project> · <worktree>` (one name when both are equal). A name is cut to 40 characters.
- A finished buzz needs at least 20 seconds of work that Termivoa saw.
- A blocked buzz needs the agent to stay blocked for about 2 seconds, so a short blip does not buzz.
- A tap opens that terminal at once.
- herdr buzzes have their own limit, 6 per minute, so they never use up the buzzes of your normal sessions.
- The text has only the agent and the project and worktree names, never terminal text.
- A sound on the computer and nothing on the phone? See [I hear a sound but see no notification](/docs/reference/troubleshooting/#i-hear-a-sound-but-see-no-notification).

## How do I turn herdr off or the buzz off?

Open **Settings** and tap the computer's card, such as **mac**. Its page has a **herdr** part with the row **herdr on mac**, the herdr version and a state word: **Running**, **Not running**, **Update** or **Off**. The card on the Settings main page shows the same state as a dot on its **herdr** chip.

![The page of the computer mac: ‹ Settings, mac · Online · Home, mac.tailx.ts.net, Termivoa private-dev · last seen just now; under herdr the row herdr on mac 0.8.2, Running, with the switches On, Buzz when blocked and Buzz when finished turned on and the note This phone gets no buzz yet. with Add to Home Screen; under GitHub the row GitHub on mac, not connected, with Connect; then This computer.](../../../../assets/docs/phone-settings-computer.webp)

| Switch | What it does |
| --- | --- |
| **On** | Off: the herdr tab leaves out that computer, the computer stops reading herdr and closes its open herdr terminals, and there is no buzz. This is for every phone. The card says "Off for every phone: mac does not show herdr or buzz for it." |
| **Buzz when blocked** | Turns the blocked buzz on or off. |
| **Buzz when finished** | Turns the finished buzz on or off. |

The switches are saved on that computer, for every phone.

**Next beta:** under the switches, "This phone gets no buzz yet." shows while a buzz switch is on and Termivoa can tell that this phone has no notifications from that computer. Its link (**Turn on notifications**, **Add to Home Screen** or **See why**) goes to **Settings → Notifications**.

## Which agents can I start?

**Next beta:** Termivoa offers up to 16 agent kinds that herdr supports starting,
with kinds already running listed first. You can start an installed agent in
**New worktree** or **New tab** without starting it manually first.

| Situation | What appears |
| --- | --- |
| The agent command is installed and herdr supports starting it | The kind can appear even with no running agents |
| The last agent of that kind stops | Its installed kind remains eligible for the 16 choices |
| An agent already runs through a shell alias or custom launcher | Its supported kind can appear while it runs |
| A command is newly installed or removed | Choices refresh within about 30 seconds; **Check again** requests a fresh check |
| The agent is installed only through settings in herdr's shell | It may need to run once in herdr before it appears |

Termivoa checks command presence without running an agent. An offered kind may
still fail to start if herdr's shell has different settings, the installation
is broken, or the agent needs sign-in. Open the new terminal to see the cause.

## How do I create a worktree?

A worktree is a second folder of a git repository on its own branch. herdr makes it and can start an agent in it.

1. In the **herdr** tab, tap **+** on a git project row.
2. In the sheet **New worktree**, type a **Branch name**: lowercase letters and digits joined by `-`, `.`, `_` or `/`, such as `fix/login-redirect`. It can have up to 64 characters and at most 4 parts, and cannot end in `.lock`.
3. Under **Start an agent in it**, tap an available agent (such as `claude` or `codex`), or **No agent**. **Next beta:** installed agents can appear before any agent runs in herdr, and stay listed after their last running agent stops. The sheet reads the choices again when it opens. If your choice is no longer available, the sheet goes to **No agent** and says, for example, "codex is not available in herdr now." It never starts another agent in its place.
4. Optional: write a **First prompt (optional)**. It cannot have control characters such as Esc.
5. Tap **Create and start** (or **Create** with **No agent**).

![The New worktree sheet for herdr · shop · on mac: Branch name fix-login, the note herdr makes the folder and a new branch from where shop is now., Start an agent in it with claude selected, codex and No agent, First prompt (optional) Fix the login redirect after sign-in., and the Create and start button.](../../../../assets/docs/phone-herdr-create.webp)

The sheet says "herdr makes the folder and a new branch from where shop is now." It takes up to about 45 seconds. Then the new terminal opens. Termivoa sends the first prompt when herdr says that the agent is ready for input.

### When does a create use my allowance?

**Next beta:** Termivoa allows three attempted creates per minute for each
phone on each computer, shared by **New worktree** and **New tab** with an
agent. A refusal before creation starts keeps that allowance. Correct the
choice and try again yourself; Termivoa does not retry a create automatically.

| What happened | Uses the create allowance? |
| --- | --- |
| A project or workspace is gone, a kind is unavailable, or herdr is busy before creation | No |
| Creation was attempted, even if it failed or the answer was lost | Yes |
| A tab or worktree exists but its agent or first prompt failed | Yes; open the existing terminal to continue |

If something goes wrong, the app says why:

| Message | What to do |
| --- | --- |
| “fix-login” already exists in shop. Pick another name. | Use another branch name. |
| shop is not in git, so it cannot have worktrees. | Use a project that is in git. |
| herdr could not make the worktree. Nothing was opened. Try again or pick another name. | Try again, or pick another name. |
| too many requests; wait a minute and try again | This phone has 3 pending or attempted creates in a minute, shared by new worktrees and agent tabs. Wait a minute. |
| herdr is busy with another change; try again | Another change runs in herdr on that computer. Try again in a few seconds. |
| herdr on vps is busy, or this phone asked for too many worktrees just now. Wait a minute, then try again. | The same two causes, on a computer that you added. Wait a minute. |
| The worktree is ready, but the agent did not start. | Open the worktree and start the agent yourself. If its terminal says "command not found", install that agent on the computer. |
| The agent started and waits at a question. Your prompt was not sent. | Answer the question, then send the prompt. With no first prompt, the text ends after "question." |
| The agent started, but your first prompt was not sent. | The agent was not ready in time. Send the prompt yourself. |

## How do I manage workspaces and worktrees?

In the **herdr** tab, each row has a **⋯** button. It opens a menu for that workspace.

| Action | What it does | Before it acts |
| --- | --- | --- |
| **New tab** | Makes a tab in that workspace and opens its terminal. Pick **Shell**, or an agent such as `claude`; for an agent you can type a **First prompt (optional)** and tap **Start**. | Nothing to confirm. |
| **Close tab** | Ends one tab of that workspace. The menu lists the tabs. The last tab has no **Close tab**: use **Close workspace**. | Asks "Close tab 2? Its terminals end." |
| **Close workspace** | Ends the terminals of that workspace. | Asks "Close silver-meadow? Its terminals end. Files stay." |
| **Remove worktree** | Closes a worktree and deletes its folder. Only a worktree has this action, never the main checkout. | Asks "Remove the worktree silver-meadow? Its folder is deleted, also files that git ignores (such as .env). The branch stays." |
| **New workspace** | A button under the list. Pick one of that computer's Termivoa projects; herdr opens a workspace in its folder and the phone opens its terminal. For a folder that is not a project yet, tap **Add a folder** in that sheet, or **From GitHub** to put one of your repositories on that computer (**Next beta**, see [GitHub](/docs/guides/github/)). | Nothing to confirm. |

A worktree that you closed keeps its folder. It then shows under its project as a dimmed row with **closed**. Tap **Open** to get its workspace back.

A herdr terminal has the same **⋯** button at the right of its tabs, so you can make or close a tab, or close the workspace, without going back to the list.

What Termivoa never does:

- **It never removes work that is not committed.** The app then says "silver-meadow has changes that are not committed. Commit or discard them first." and nothing changes.
- **It never closes two workspaces in one tap.** For a project's main checkout with open worktrees, **Close workspace** is off and says "Close its worktrees first." When herdr lists two workspaces under one project and both are at that project's main folder (herdr has its worktree record of the same repository for each), it is off for both and says "herdr closes the workspaces at this project's main folder only together. Close them in herdr on mac." A second workspace in the same folder that herdr lists as its own project closes alone.
- **It checks the name again.** If the list changed after you looked, the app says "The list changed. Look again, then try again." and nothing changes.

**Remove worktree** deletes files that git ignores, such as `.env` and build output, with the folder. The branch and its commits stay.

**Add a folder** opens a terminal on that computer with `pterm project add ` typed in. Type the folder, such as `~/shop`, press Enter, go back, and tap **Check again** in the **New workspace** sheet. The phone never sends a folder path by itself: you type it in a terminal on that computer.

The **+** on a project row shows for every project whose folder is a git repository. A project that is not in git has no **+**.

## How do I install or start herdr from the phone?

Open **Settings** and tap the computer's card. The page of each computer (macOS or Linux) has a **herdr** part.

| The herdr part says | What you can do |
| --- | --- |
| **Not installed**, with **Install herdr** | Tap it. Termivoa opens a new session on that computer with herdr's install command typed in: `curl -fsSL https://herdr.dev/install.sh \| sh`. Read it, then press Enter there to run it. Termivoa itself downloads nothing. Then tap **Check again**. |
| **Not installed**, with "Run `pterm doctor` on mac." | A herdr is on that computer, but Termivoa will not use it. `pterm doctor` says why. |
| **Not running**, with **Start herdr** | Tap it. Termivoa starts herdr's server on that computer. The same button is on the notice "herdr is not running on mac" in the **herdr** tab. |

A herdr that Termivoa started keeps running when you leave the app. On Linux it also keeps running when Termivoa restarts, if the computer has `systemd-run`; without it, herdr ends when Termivoa's service stops.

## Why is the pane small on my laptop?

herdr gives a pane the size of whoever attached last. Termivoa opens the herdr terminal at the phone's size, so it fits the screen. The line under the tab chips says it: "mac shows this terminal at phone size while it is open here."

The laptop pane takes the phone's size, and you can still type there. When you leave Live, or lock the phone or switch apps, the phone stops watching. About 10 seconds later, herdr gives the laptop pane its own size back. When you come back, the terminal opens again.

## What are the limits?

| Limit | Detail |
| --- | --- |
| Open terminals | At most 4 herdr terminals open at once for each computer. Each phone can also open 20 a minute. At either limit the app says "mac cannot open more herdr terminals just now. Close one you do not use, or wait a minute." They do not count toward Termivoa's own session limit. |
| Panes | One pane for each open terminal. No split panes. |
| Input | No mouse. No herdr prefix keys: Termivoa sends `ctrl+b` to the agent as a plain `ctrl+b`, so the phone cannot send herdr's prefix commands. |
| Custom prefix | If you set another prefix key in herdr's `config.toml`, the phone can send that key. At worst it closes the phone's own view of the terminal. `pterm doctor` warns about it. |
| Control | Opening never takes control. You tap **Take control**. |
| Where they show | herdr terminals are not in the Sessions list, the Wall or `pterm list`. |
| After a restart | Termivoa does not keep them when it restarts. Open the row again. |
| New agent tab | A new tab that starts an agent counts toward the same limit as making a worktree: 3 a minute for each phone. |
| Manage | Each phone can do 30 manage actions a minute, and one at a time for each computer. A workspace has at most 32 terminals and herdr at most 64 projects in the list. |
| Not from the phone | Force-removing a worktree, renaming, split panes, updating herdr. |
| Platforms | macOS and Linux. Not Windows. |
| herdr sessions | Only the default herdr session. Not named sessions. |
| herdr versions | Termivoa is tested with herdr 0.8.2 and herdr 0.9.3. |

## Common questions

### Why is there no herdr tab?

herdr is not installed on that computer, or Termivoa cannot find it, or **On** is off on that computer's page in **Settings**, or Termivoa there is older than the next beta. Run `pterm doctor` on the computer. See [The herdr tab is missing](/docs/reference/troubleshooting/#the-herdr-tab-is-missing).

### Can I open a herdr terminal from a link?

Yes. A link to a herdr terminal, or a reload of its page, first shows its names and an **Open** button: "Open shows it here, read-only." It opens only after you tap **Open** in the app, or tap a notification. If the terminal is gone, the herdr tab says "That terminal is gone."

### Can the phone kick my laptop out of a pane?

No. Termivoa never takes a pane over from another herdr client. See [What does the phone see and do with herdr?](/docs/reference/security/#what-does-the-phone-see-and-do-with-herdr)

### Does this work with several computers?

Yes. Each computer reads its own herdr. The **herdr** tab shows all of them in **All computers**, each computer under its own header, or one computer that you choose in the side menu. See [Use several computers](/docs/guides/computers/#how-do-i-add-a-computer) to add one.

## Why does starting an agent wait for a check?

**Next beta:** Termivoa checks that computer's offered agents when **New worktree** opens, when its workspace menu opens, and when **New tab** opens.

| What you see | What to do |
| --- | --- |
| **Checking available agents…** | Wait for the check before **Create and start** or **Start**. **No agent** and **Shell** remain available. |
| “claude is not running in herdr now.” | The selected agent is no longer offered. Choose an available agent; Termivoa does not pick another one for you. |
| The check could not complete | The last known choices remain. A start may still be refused if that agent no longer runs. Look at herdr on the computer before trying again. |
| **Close workspace** is off and the workspace shares the project's main folder | Close those workspaces in herdr on the computer. The last tab ends with its workspace. |
| A close says the list changed | Inspect the list again. Close linked worktrees first; workspaces sharing the main folder must be closed in herdr on the computer. |

An agent can stop after a successful check. Termivoa still checks each requested action on the computer and never retries an agent start by itself.
