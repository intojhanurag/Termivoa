---
title: Providers
description: A provider is a terminal workspace tool on your computer that Termivoa shows on your phone. herdr is the first provider. More can follow.
lastUpdated: 2026-10-04
---

A provider is a terminal workspace tool on your computer, such as herdr, whose projects and coding agents Termivoa shows on your phone.

:::note[Next beta]
This page describes features that are not in `v0.1.0-beta.3` yet. Update Termivoa on the computer to the next beta to get them.
:::

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
| Notifications | A buzz when an agent is blocked or finished. |
| **Settings → Integrations** | One card for each computer that has the provider, with the switches **On**, **Buzz when blocked** and **Buzz when finished**. |

A computer without a provider shows no tab and no card, and nothing changes.

## How do I turn a provider on?

You do not. Termivoa finds a supported provider on the computer by itself. To turn it off for one computer, open **Settings → Integrations** and turn **On** off.

## What can a phone see and do through a provider?

A paired phone sees the provider's project names, worktree names, tab labels and agent kinds, and can type into a terminal that it opens. It does not get folder paths or terminal titles in the list or in notifications. See [Security](/docs/reference/security/).
