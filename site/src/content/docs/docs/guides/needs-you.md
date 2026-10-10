---
title: Needs you
description: Termivoa's Needs you tab lists every agent that waits for you on all your computers, and a Next button takes you from one answer to the next.
lastUpdated: 2026-10-10
---

Termivoa puts every coding agent that waits for you, on all your computers, in one list called **Needs you**, and a **Next** button takes you from one to the next.

:::note[Next beta]
This page describes a feature that is not in `v0.1.0-beta.3` yet. It is a change in the phone app only. Update Termivoa on your home computer to the next beta to get it.
:::

## Where is the Needs you tab?

On the Sessions list, **Needs you** is the last tab in the tab row, after **Sessions** (and **herdr**, when a computer has it). An amber number on it counts what waits for an answer, for example **Needs you 3**.

| Case | What you see |
| --- | --- |
| At least one agent waits for an answer | The **Needs you** tab, with the count. |
| Nothing waits | No **Needs you** tab. |
| One computer and no herdr | The tab row shows only while something waits, with **Sessions** and **Needs you**. |
| You are on the tab and the last item is answered | The tab stays while it is open. The list says "Nothing needs you." |
| A computer does not answer | The list says "Some computers did not answer. See Sessions." It does not say "Nothing needs you." then. |

With one computer chosen in the [side menu](/docs/guides/computers/#how-do-i-switch-between-computers), the tab counts and lists only that computer. In **All computers**, it counts all your computers. The side menu shows the count of each computer, and its **Needs you** row opens the tab for all computers.

## What is in the list?

The list has two parts.

| Part | Row | State word | Also on the row |
| --- | --- | --- | --- |
| On top: waits for an answer | A session whose agent asks a question | `waiting · 4m` (amber) | The question and **Answer ›**, when Termivoa has the question |
| On top: waits for an answer | A [herdr](/docs/guides/herdr/) worktree or workspace that is blocked | `blocked · 9m` (amber) | The names "project · worktree", and the word "herdr" |
| Under **Finished** | A session with **Your turn**: the agent finished and this phone has not seen its new output | `your turn` | – |
| Under **Finished** | A herdr worktree or workspace that is done, and that you did not open on this phone since you opened the app | `done` | The word "herdr" |

- In each part, the longest wait is first.
- With several computers and **All computers** chosen, each row shows the computer's colour dot and name.
- The count on the tab and the **Next** button use only the top part. **Finished** rows are there to read, not to answer.
- On this tab, the status line under **Sessions** says "3 need you" or "1 needs you". When nothing waits, the list says "Nothing needs you."

## How do I answer and go to the next one?

1. Tap **Needs you**.
2. Tap a row. A session opens in the [live terminal](/docs/guides/live-terminal/), with its [answer card](/docs/guides/answer-cards/). A herdr row opens that herdr terminal.
3. Answer. Without control, tap **Take control to answer** first (in a herdr terminal: **Take control**). Opening an item never takes control.
4. Under the status line, a row says **Needs you** and has one button: **Next · 2 left ›**. Tap it to open the next agent that waits.
5. On the last one, the button says **Inbox ›**. It goes back to the **Needs you** tab.

| Question | Answer |
| --- | --- |
| What does the number mean? | How many items still wait, without the one on screen and without the ones this round already showed. |
| Does it move on by itself after I answer? | No. When your computer accepts your answer, the **Next** button turns into a filled button. You tap it. |
| Does **Next** send anything to the agent? | No. **Next** only opens another item. It never types, answers or takes control. |
| I tapped **Next** without an answer. | That item stays in **Needs you**. This round does not show it again. |
| An agent got its answer somewhere else. | **Next** never opens it. The list and the number follow what waits now. |
| I tapped **Next** twice by mistake. | The second tap does nothing. **Next** takes no tap for a moment after it moved, so no item is skipped. |
| The next item is gone. | A session that ended shows "This session is gone" with the **Next** row above it. A herdr terminal that is gone takes you back to the **Needs you** tab, which says "That terminal is gone." |
| Where do **‹ Sessions** and Back go? | To the **Needs you** tab, while the round is on. |
| When does the round end? | When you leave the terminal: Sessions, Wall, Settings or a new session. Open a row in **Needs you** to start again. A session that you open from the tabs on top keeps the round. |
| Where did the button go while I type? | The row hides while the keyboard is up. |

## What does a notification tap do?

A tap on a [notification](/docs/guides/notifications/) opens that session or herdr terminal, as before. Now the **Next** button is there too, so you can answer and go to the rest.

| Phone | What a notification can do |
| --- | --- |
| iPhone and iPad | Notifications need the Home Screen app. Termivoa shows no answer buttons in a notification; a tap opens the item. You answer there. |
| Android | Termivoa shows no answer buttons in a notification; a tap opens the item. You answer there. |

On both, you cannot answer from the lock screen. Every answer is a tap inside the open app.

A link to a session, a reload, and Back or Forward do not start it. Only a tap on a row in **Needs you** or on a notification starts it. If it does not show after a tap, see [Next is not there after a notification tap](/docs/reference/troubleshooting/#next-is-not-there-after-a-notification-tap).

## What does the list not show?

| Not shown | Why |
| --- | --- |
| Terminal text | A row shows names and a state word. The only screen text is the question, the same one the Sessions row shows. |
| A computer that cannot be reached | Its notice is on the **Sessions** tab, and the list says "Some computers did not answer. See Sessions." Its agents come back when the computer answers. |
| herdr that is off, not running or too old | Its notice is on the **herdr** tab. See [What if herdr has a problem?](/docs/guides/herdr/#what-if-herdr-has-a-problem) |
| Sessions that work, exited or are lost | They do not wait for you. They are on the **Sessions** tab. |

## Common questions

### Why is a herdr row under Finished again?

Termivoa remembers which done herdr rows you opened only until you close or reload the app. herdr itself keeps "done" until someone looks at that terminal in herdr on the computer.

### Two agents are blocked in one herdr worktree. Why is there one row?

A herdr worktree or workspace is one row. Open it and use the tab chips on top to go to its other tab.

### Does Needs you change anything on my computers?

No. It shows what the Sessions list and the herdr tab already know, in one place.
