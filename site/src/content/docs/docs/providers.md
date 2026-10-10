---
title: Providers
description: A provider is a terminal workspace tool on your computer that Termivoa shows on your phone. herdr is the first provider. More can follow.
lastUpdated: 2026-10-10
---

A provider is a terminal workspace tool on your computer, such as herdr, whose projects and coding agents Termivoa shows on your phone.

## Which providers does Termivoa support?

| Provider | Needs | Computers | Guide |
| --- | --- | --- | --- |
| [herdr](https://herdr.dev) | herdr 0.8.2 or later | macOS, Linux | [Termivoa + herdr](/docs/guides/herdr/) |

herdr is the only provider today. Termivoa is built so that other workspace tools can be added later; none is promised yet.

## What does a provider add to the phone?

| Where | What you get |
| --- | --- |
| Sessions | A tab with the provider's name, such as **herdr**, with its projects, worktrees and agent state words. |
| Live | One terminal of the provider, opened with a tap. It is read-only until you tap **Take control**. |
| The **⋯** menu on a row | **New tab**, **Close tab**, **Close workspace** and **Remove worktree**, each after a confirmation where it ends or deletes something. **New workspace** is under the list. |
| Notifications | A buzz when an agent is blocked or finished. |
| **Settings**, a computer's page | A **herdr** part on the page of each computer. With the provider installed: the switches **On**, **Buzz when blocked** and **Buzz when finished**, and **Start herdr** when it does not run. Without it: **Not installed** and **Install herdr**. |

A computer without a provider shows no tab. Its page in **Settings** says **Not installed** in the **herdr** part.

## How do I turn a provider on?

You do not. Termivoa finds a supported provider on the computer by itself. If the provider is not installed, **Install herdr** opens a terminal with the install command typed in, and you press Enter. See [Termivoa + herdr](/docs/guides/herdr/#how-do-i-install-or-start-herdr-from-the-phone). To turn it off for one computer, open **Settings**, tap that computer's card, and turn **On** off.

## What can a phone see and do through a provider?

A paired phone sees the provider's project names, worktree names, tab labels and agent kinds, can type into a terminal that it opens, and can close a workspace, remove a worktree that has no uncommitted change, and make a tab or a workspace. It does not get folder paths or terminal titles in the list or in notifications. See [Security](/docs/reference/security/).
