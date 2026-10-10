---
title: Chat view
description: Termivoa's Chat view shows a Claude Code session as a readable chat on your phone, read from the agent's own conversation file. Next beta.
lastUpdated: 2026-10-10
---

**Next beta.** Termivoa's Chat view shows a coding agent's session as a chat on your phone: your messages, the agent's replies and its tool steps, read from the agent's own conversation file on your computer.

The terminal stays as it is. Chat is a second way to read the same session, and one tap takes you back.

## Which agents and computers does Chat work with?

| Item | Works with Chat |
| --- | --- |
| Claude Code in a Termivoa session | Yes |
| Codex, herdr panes, other agents | Not yet. Chat shows **Chat isn't available for this session** and the terminal works as always. |
| Computer | macOS and Linux. Windows computers show no Chat. |
| Termivoa on the computer | The next beta or later, on every computer you use Chat with |

## How do I turn Chat on?

Chat is off on every computer until you turn it on **at that computer**. A phone cannot turn it on.

1. On the computer, run `pterm chat on`.
2. Read the question and answer `y`.
3. On your phone, open a session where Claude Code runs.

Only the computer can turn Chat on, so a paired device alone can never start reading your conversations.

## How do I switch between Chat and the terminal?

Once Claude Code works or asks something in a session, the line under the session tabs shows a **Chat | Terminal** switch.

| Tap | What you see |
| --- | --- |
| **Chat** | The conversation as a chat |
| **Terminal** | The live terminal, as before |

Your phone remembers the choice for each session while the app is open. Terminal gestures (hold and slide, pinch, two-finger tap) work only in **Terminal**.

## What does Chat show?

| In Chat | What it is |
| --- | --- |
| Your messages | On the right, with **You** and how long ago |
| The agent's replies | On the left, under the agent's name. Formatting such as lists and code shows; pictures and web page code do not. |
| Tool steps | One box per group: **Read**, **Edit** with **+** and **−** line counts, **Ran** or **Run** with the command, and a coloured dot (green done, cyan running, red failed) |
| An edit | Tap it to see the changed lines (at most 40), then **Open in Changes ›** for the whole file in [Changes](/docs/guides/changes/) |
| **Task** | A helper agent's task: its summary and whether it is done. Its own steps are not shown. |
| **Thought for 14s** | How long the agent thought. What it thought is never sent. |
| **Conversation compacted** | The agent shortened its own memory of the conversation at this point |
| **↑ Earlier messages** | Loads older messages |
| **3 new ↓** | New messages arrived while you read further up. Tap to go to the newest. |
| **Working · 6s** and **esc stop** | The agent is working. **esc stop** sends Esc, as the agent key does in the terminal. |

## What does Chat never show?

- The agent's thinking text, only how long it thought.
- Pictures and attachments.
- The steps of helper agents.
- Long tool output: Chat shows a short preview (about 40 lines). The terminal and [Files](/docs/guides/files/) have the rest.

## How do I send a message from Chat?

The box at the bottom, **Message claude…**, is the same as **Compose** in the terminal. It needs control: without it, tap **Take control to send** first. Your message goes in as typed text and then Enter.

- A message of several lines opens **Compose**, which asks you to confirm before it sends.
- The microphone button opens **Compose** for voice and photos.

When the agent asks a question, the [answer card](/docs/guides/answer-cards/) takes the place of the message box until you answer.

## Why does a link ask before it opens?

Text in a conversation can come from web pages, files and tool output, not only from you and the agent. So a link in Chat opens only after you tap it and confirm in **Open this link?**, which shows the full address. Links to the address this app runs on stay plain text, so a faked pairing link cannot open.

## What does "Chat isn't available for this session" mean?

Chat shows nothing rather than a guess when it is not sure which conversation belongs to the session. Tap **Show terminal** to go back, or **Why?** for the reason. [Troubleshooting](/docs/reference/troubleshooting/#chat-isnt-available-for-this-session) lists every reason.

## How do I turn Chat off?

Any paired phone can turn Chat off:

1. Open **Settings** and tap the computer.
2. Under **Chat view**, turn the switch off.

Every open chat on that computer closes at once. When Chat is off, the row says **Turn it on at the computer: pterm chat on**. You can also run `pterm chat off` on the computer.

## Is anything saved?

No. The computer reads the conversation file only while a phone has Chat open, and keeps nothing. Your phone keeps the chat in memory only, never in its storage, and forgets it when you leave Chat. See [Security](/docs/reference/security/#what-does-chat-view-send-to-the-phone).
