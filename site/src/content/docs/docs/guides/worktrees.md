---
title: Worktrees
description: Start a new task on its own branch and folder from your phone, so several agents can work on one repository at the same time.
---

This page helps you start a new task in its own copy of a repository.

## What a worktree is

A git worktree is a second folder for the same repository, on its own branch. Each folder has its own files, so two agents in two worktrees do not change each other's files. All worktrees share one history, so you commit and push as usual.

Termivoa makes the folder and the branch for you, and opens a new session in it.

## Make a worktree

1. In a running session, go to your repository, for example `cd ~/code/myapp`.
2. Tap **⋯**, then **New worktree…**.
3. Check the fields:
   - **name**: a random name, such as `brave-otter`. You can change it. Use lowercase letters, digits and single hyphens, up to 40 characters. Tap 🎲 for another random name.
   - **from**: the branch to start from. The default is the repository's main branch. You can pick any local or remote branch.
   - **start with**: **Shell**, **Claude** or **Codex**. Claude and Codex start in the new folder at once.
4. Tap **Create**.

Termivoa makes:

- the folder `~/termivoa/worktrees/<repository>/<name>`;
- the branch `termivoa/<name>`, from the branch you picked.

A new tab opens in that folder. Its name is the worktree name.

The new branch does not track a remote branch. When you push it from [Changes](/docs/guides/changes/), it goes to `origin` as a new branch, ready for a pull request.

:::tip
Start from `main` for a new task. To continue another task, pick its branch in **from**.
:::

## See your worktrees

A session in a worktree shows a **⎇ name** badge on the Sessions list and in the line above the terminal.

All worktrees that Termivoa made are in `~/termivoa/worktrees` on your computer. You can open them in your editor like any other folder.

## Remove a worktree

When the task is done and pushed:

1. Open the worktree's session and take control.
2. Tap **⋯**, then **Remove worktree…**, then **Remove worktree**.

Termivoa deletes the folder and ends the session. The branch `termivoa/<name>` stays, so no commit is lost.

If the folder has uncommitted changes, new files, or files that git ignores (such as `node_modules` or `.env`), Termivoa stops and asks first. **Remove anyway** deletes them for good. **Keep it** stops.

## Limits

- The session's folder must be in a git repository.
- A name can be used once per repository. If the folder or the branch already exists, pick another name.
- One repository can have up to 50 worktrees in `~/termivoa/worktrees`.
- Removing needs control of the session.
