---
title: Answer cards
description: Answer Claude Code and Codex questions with one tap, and why a tap never answers the wrong question.
---

This page helps you answer coding-agent questions with one tap.

## What an answer card is

Claude Code and Codex often stop and ask, for example "Do you want to proceed?".
When this happens, the live screen shows a card at the bottom. The card shows:

- the question;
- what you approve, such as the exact command;
- one big button for each option.

Termivoa also knows numbered menus from other programs, and `[y/N]` prompts.

## Answer a question

1. Open the session. A waiting session has the **Waiting** badge.
2. Read the question and what it approves.
3. Tap one option.

One tap sends one answer. Termivoa never answers or retries on its own.

If you do not have control, the card shows **Take control to answer**. Tap it first.

To see the terminal behind the card, tap **terminal**. The card folds into an **↑ answer** pill. Tap the pill to open it again.

## Why it is safe

Before your answer reaches the program, your computer checks the screen again:

- The same question must still be on screen.
- What it approves must be the same. Two prompts that ask the same thing about different commands count as different questions.

If the question changed, your answer is not sent. The card says **The question changed — check the new one**. You keep control and can decide again.

Your computer checks one more time just before it types the keys. If the question changed in that moment, it drops the answer and shows the new state.

## Limits

- Menus without numbers, such as Claude Code's folder-trust dialog, get no card. Use the arrow keys and `⏎` in the key bar.
- If the same prompt, with the same command, shows twice in a row, an answer for the first can answer the second. Both approve exactly the same thing.
- New agent versions can change their screens. If a question gets no card, answer it with the [agent keys](/docs/guides/live-terminal/#keys).
