---
title: Set up a GitHub repository
description: Termivoa connects each of your computers to GitHub from the phone, lists your repositories, and clones one onto a computer with one tap.
lastUpdated: 2026-10-07
---

Termivoa connects a computer to GitHub from your phone, shows your repositories, and clones the one you pick onto that computer, ready for a new session.

:::note[Next beta]
This page describes a feature that is not in `v0.1.0-beta.3` yet. Update Termivoa on the computer to the next beta to get it.
:::

## What do I need?

| Need | Detail |
| --- | --- |
| git 2.31 or later | On that computer. Nothing else: no `gh`, no git login. |
| macOS or Linux | Windows shows no GitHub row. |
| A GitHub account on github.com | GitHub Enterprise is not supported. |
| Termivoa from the next beta | On that computer. |

## How do I connect GitHub?

Each computer connects on its own. Do these steps one time for each computer.

1. Open **Settings → Integrations**. Each computer has a row **GitHub on mac** (with the name of that computer). Tap **Connect**.
2. The sheet shows an 8-character code. Tap **Copy code and open GitHub**. This copies the code and opens <https://github.com/login/device>.
3. On GitHub, paste the code and authorize **Termivoa**.
4. One time for each GitHub account: the sheet says **Choose repositories**. Tap **Choose on GitHub** and pick **All repositories** or only some. GitHub can send you to a page that says "Done. Go back to Termivoa." Go back to the app; it continues by itself.
5. The row says **Connected** and shows your GitHub login.

The code works for 15 minutes. The sheet shows the time that is left.

:::caution
Enter a code only when you started **Connect** yourself. A person who gets you to enter their code gets access to your chosen repositories.
:::

### What does the GitHub login allow?

The login is a GitHub App named **Termivoa**.

| Permission | Why |
| --- | --- |
| Contents: Read and write | Clone, pull and push |
| Metadata: Read-only | The list of repositories |

It reaches only the repositories that you chose on GitHub. It cannot change workflow files in `.github/workflows`.

## How do I put a repository on a computer?

1. In **Settings → Integrations**, tap **Set up a repository** on the row of that computer. Or, in herdr's **New workspace** sheet, tap **From GitHub** next to **Add a folder**.
2. Search the list, then tap **Set up** on a repository.
3. The sheet shows the clone with a percent and **Stop**. You can lock the phone; the computer continues.
4. When it says **Ready on mac**, tap **New session** to open a session in the repository. When herdr runs on that computer, **New workspace in herdr** opens it in herdr.

| Detail | Value |
| --- | --- |
| Where it goes | `~/<name>` in your home folder on that computer, for example `~/notes` |
| Branch | The repository's default branch |
| Project | Termivoa adds the folder as a project, so it shows in **New session** |
| Large repository (1 GB or more) | The size shows in amber, and Termivoa asks first: "notes is 1.4 GB. Set it up on mac?" |

### What does Set up never do?

- It never deletes or overwrites a folder. When a folder of that name is already there, nothing changes.
- It runs nothing from the repository: no hooks, no submodules, and no Git LFS download.
- It does not offer a repository whose name starts with `.` or `-`.

## What do "on mac" and "name taken" mean?

| Tag on a row | Meaning | What a tap does |
| --- | --- | --- |
| **Set up** | No folder `~/<name>` is on that computer | Clones the repository |
| **on mac** | This repository is already on that computer, in `~/<name>` or in a saved project | Makes it a project if it is not one yet, then shows **Already on mac.** with **New session**. Nothing is cloned |
| **name taken** | Another folder named `~/<name>` is on that computer: another repository, a plain folder, a file or a link | Nothing. The row says "Another folder named notes is on mac." |

Two repositories with the same name from two owners cannot both be set up on one computer. The second one says **name taken**.

## What can a phone do with GitHub?

| A paired phone can | A paired phone cannot |
| --- | --- |
| Connect and disconnect a computer | Read the GitHub login (the token) |
| See the names of your chosen repositories, also private ones | Choose the folder that a clone goes to |
| Clone a chosen repository into `~/<name>` and stop a clone | Delete or overwrite a folder |
| Open a session in it | Clone from a site other than github.com |

The login stays on the computer. The phone gets only the code, the account name and the list. See [Security](/docs/reference/security/#what-does-github-change).

### How do pull and push work in a cloned folder?

In a folder that Termivoa cloned, git uses Termivoa's login for that one repository. So `git pull` and `git push` work in a session, and **Commit** and **Push** in [Changes](/docs/guides/changes/) work, also on a computer that has no git login of its own. Termivoa does not write the login to the macOS Keychain or to `~/.git-credentials`.

A folder that was already there (**on mac**) keeps your own git login.

When the repository and your git settings have no commit name, Termivoa sets the repository's own name to your GitHub login and the email to GitHub's no-reply address. Your global git settings do not change.

## How do I disconnect?

1. In **Settings → Integrations**, tap **Disconnect** on the row.
2. The app asks: "Disconnect GitHub on mac?". Tap **Disconnect**.

Termivoa removes the login at GitHub, then deletes it from the computer. When GitHub did not confirm the removal, the row shows **Also remove Termivoa on GitHub.** Tap it and remove **Termivoa** under **Authorized GitHub Apps**.

On the computer you can also run `pterm github disconnect`. See the [CLI reference](/docs/reference/cli/#github).

After Disconnect, `git pull` and `git push` in a folder that Termivoa cloned fail with "Termivoa is not connected to GitHub on this computer. Connect in Settings → Integrations." Connect again to use them.

## What are the limits?

| Limit | Value |
| --- | --- |
| Connect | One at a time on each computer. The code works for 15 minutes |
| Set up | One at a time on each computer. At most 30 minutes |
| Free space | 3 times the repository's size, plus 1 GB |
| Repository list | At most 1000 repositories. A new read comes at most every 60 seconds |
| Projects | 200 on each computer |
| Login | GitHub's access token works for 8 hours, and the computer gets a new one by itself. Without use for 6 months, you connect again |
| Requests from each phone, each minute | Read 60, list 10, connect 3, set up 3, cancel, disconnect or stop 10 |

Not in this release: GitHub Enterprise, Windows, a branch choice, another folder than `~/<name>`, creating a repository, pull requests, and changes to workflow files.

## Common questions

### Do I need `gh`?

No. Termivoa has its own login. A computer with `gh` also uses **Connect**. Termivoa does not use or change the `gh` login.

### Why one code for each computer?

Each computer keeps its own login, and the login never moves between computers. If you lose one computer, you remove only its login on GitHub, and the other computers keep working.

### Does push work?

Yes, in a folder that Termivoa cloned, and in a folder that has your own git login. GitHub refuses a push that changes a file in `.github/workflows`: the login does not have that permission. Push such a change from a computer with your own git login.

## Back to Termivoa

Done. Go back to Termivoa.
