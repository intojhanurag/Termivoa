---
title: Changes
description: See what the agent changed in your project from your phone, as a list of files and a red and green diff for each file.
---

This page helps you see what a coding agent changed in your project.

## What Changes shows

**Changes** shows the uncommitted changes in the git project of the session's folder. These are the same changes that `git status` and `git diff` show on your computer, plus new files that git does not ignore.

Changes only reads your files. It never changes, stages or commits them.

You do not need control. Changes also works when you only watch.

## See the changed files

1. Open the session.
2. Tap **changes** in the dock. When you only watch, tap **Changes** under **Take control**.
3. Read the list. The line at the top shows the folder, the branch, the number of files and the total lines added and removed.

Each row shows one file:

- a letter for the change: **M** modified, **A** added, **D** deleted, **R** renamed, **U** new and not yet added to git, **T** type changed;
- the file path;
- the lines added in green and the lines removed in red.

Tap **Refresh** to read the changes again. Tap **Done** to go back to the live screen.

## See one file

1. Tap a file in the list.
2. Read the diff. Added lines are green. Removed lines are red. The small numbers at the left are the old and new line numbers.
3. Tap the back arrow to go back to the list.

Binary files, such as images, are not shown.

## Ask the agent about a file

1. Open a file.
2. Tap **Ask agent about this file**. The compose sheet opens with `@` and the file path in it.
3. Write your question and tap **Send ⏎**.

Nothing is sent until you tap **Send ⏎**. To send, you need control.

## Limits

- A project with very many changes shows only the first 300 files.
- A very long diff is cut short. The screen tells you when this happens. See the rest on your computer.
- If the folder is not in a git project, Changes says so.

:::caution
Any phone paired with this computer can see these diffs, also when it only watches. Diffs can contain secrets, such as keys in a changed `.env` file.
:::
