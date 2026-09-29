---
title: Introduction
description: What Termivoa is, who it is for, and how it links your phone to the terminal on your own computer.
---

This page tells you what Termivoa is and what you need to start.

## What Termivoa is

Termivoa lets you use the terminal sessions on your computer from your phone's browser.
A session can be a normal shell or an AI coding agent, such as Claude Code or Codex.

Your sessions run on your computer, not in a cloud.
When you close the phone app or lose signal, the sessions keep running.
There is no account, no cloud service and no telemetry (usage data sent to a company).

The phone side is a web app. You do not install anything from an app store.
The command-line tool on your computer is called `pterm`.

:::note
Termivoa is a **private beta** (`v0.1.0-beta.2`). Only invited testers can download it.
:::

## Who it is for

Termivoa is for people who run long terminal jobs or coding agents and want to check on them away from the desk.
For example, you can answer a Claude Code question from the sofa, or watch a build from another room.

## How it works

1. **Your computer runs `pterm`.** It keeps your sessions alive and serves a private web page.
2. **Tailscale links your devices.** Tailscale is a free app that joins your devices in a private network. Only your own devices can reach the page.
3. **Your phone opens the page in its browser.** You pair the phone once. Then you can watch, type and answer from anywhere.

## What you need

- A computer that runs macOS (Apple Silicon), Linux (Ubuntu 22.04 or 24.04) or Windows 11 (x64).
- A phone with a modern browser: iPhone (Safari) or Android (Chrome).
- [Tailscale](https://tailscale.com/download) on the computer and on the phone, signed in to the same account.
- The computer must stay awake and logged in while you use it from the phone.

## Next steps

- [Quick start](/docs/quick-start/): go from zero to a paired phone.
- [Install on macOS](/docs/install/macos/), [Linux](/docs/install/linux/) or [Windows](/docs/install/windows/).
