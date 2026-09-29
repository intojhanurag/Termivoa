---
title: Changes
description: See what the agent changed in your project from your phone, as a list of files and a red and green diff for each file.
---

This page helps you see what a coding agent changed in your project.

## What Changes shows

**Changes** shows the uncommitted changes in the git project of the session's folder. These are the same changes that `git status` and `git diff` show on your computer, plus new files that git does not ignore.

To see changes, you do not need control. Changes also works when you only watch.

Changes does not change your files on its own. When you have control, you can commit and push. See [Commit and push](#commit-and-push).

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

## Ask about specific lines

1. Open a file.
2. Tap a line in the diff. It is highlighted.
3. To pick more lines, tap a second line. All lines between the two are picked. A range has 20 lines at most, and it stays in one part of the diff.
4. Tap **Ask agent about line 42** or **Ask agent about lines 40–42**. The compose sheet opens with the path, the line numbers and the picked lines.
5. Write your question under the lines and tap **Send ⏎**.

To clear the lines, tap a picked line or tap **Clear**. The numbers are the new line numbers. When you pick only removed lines, they are the old line numbers.

## Commit and push

You need control to commit. Without control, the list shows **Take control to commit**.

1. Open **Changes**. Each file has a checkbox. All files are ticked.
2. Untick the files that you do not want in the commit.
3. Tap **Commit N files**.
4. Read the message. Termivoa writes a first message from the file names, for example `Update app.js and 4 more files`. Change it if you want.
5. Tap **Commit**. Wait for the result.
6. After **Committed**, tap **Push** if you want to send the commit to the remote.
7. Read the question and tap **Push** again.
8. After **Pushed**, tap **Open pull request** to open the page that the remote gave, if there is one.

If the commit or push fails, the sheet shows the output from git, for example an error from a hook. The list reads the changes again after each commit.

If the connection stops while a commit or push runs, Termivoa does not send it again. Look at the Changes list before you try again.

:::caution
The commit runs your repository's own hooks, as a commit on your computer does. Push never forces, so it cannot overwrite commits on the remote. You need control to commit or push.
:::

## Limits

- A project with very many changes shows only the first 300 files.
- A very long diff is cut short. The screen tells you when this happens. See the rest on your computer.
- If the folder is not in a git project, Changes says so.

:::caution
Any phone paired with this computer can see these diffs, also when it only watches. Diffs can contain secrets, such as keys in a changed `.env` file.
:::
