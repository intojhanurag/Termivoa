# The "Needs you" inbox: design

Status: **built (2026-10-06), in the next beta.** Web app only: no new request type, no protocol change, no host change, no database change. It is not a critical feature under AGENTS.md rule 24 (no change to authentication, pairing, trust, the network surface, the protocol or stored secrets). The proposal in the last section **is** critical and is **not built**.

What it does. One list of everything that waits for the user, over every computer and over Termivoa sessions and herdr together, and a **Next** button that goes from one waiting agent to the next.

| Part | What the user sees |
| --- | --- |
| The tab | **Needs you** with an amber count (`Needs you 3`) in the Sessions tab row, right after **All** |
| The list | What waits for an answer, the longest wait first; then **Finished** |
| The queue | In Live and in a herdr terminal: a row `NEEDS YOU` with one button, `Next · 2 left ›`, or `Inbox ›` on the last one |
| A notification tap | Opens the item as before, and now also starts the queue |

## 1. Placement

**Chosen:** a tab (chip) in the Sessions tab row, right after **All**: `All · Needs you 3 · herdr 2 · mac · vps`.

- It shows only while at least one item waits for an answer, or while it is the open tab (so "Nothing needs you." has a place to show).
- With one computer and no herdr there was no tab row. Now the row appears with **All** and **Needs you**, only while something waits.
- No existing control was removed, and nothing moved in front of **All**.
- The count is the same on every tab and for every chosen computer: it is computed from all computers.

**Rejected:** make the Sessions status line ("2 computers · 1 waiting · 1 working") a button.

| Reason | Detail |
| --- | --- |
| It is a status text | The line is a live region (`role="status"`). A live region that is also a control reads badly in a screen reader. |
| Hard to find | Nothing tells the user that a line of text can be tapped. |
| Its words are wrong for this | The line counts Termivoa sessions only. The inbox also counts herdr. |

## 2. Data it reuses

| Need | Source (unchanged) |
| --- | --- |
| Sessions of every computer | `useAllSessions` (`allSessions.ts`) |
| herdr items of every computer | `useProviders` (`providers.ts`) |
| "Your turn" (output this phone has not seen) | `hasNewOutput` and `agentBadge` (`activity.ts`), with the stored view times (`readViewed`) |
| herdr `done` that was opened here | `dimmed` and `markOpened` (`Herdr.tsx`) |

One change in polling scope: the provider read ran only while Sessions, a herdr terminal or Settings showed. It now also runs while a session is open in Live **in a queue run**, so that Next knows about blocked herdr agents. Outside a queue run it is as before.

## 3. The list

`inboxItems(sessions, providers, viewed)` builds it. Two parts, in this order:

| Part | Item | State word | Extra |
| --- | --- | --- | --- |
| Waits for an answer | A Termivoa session whose agent state is `waiting` | `waiting · 4m` (amber) | The question line and `Answer ›`, when the app has the question |
| Waits for an answer | A herdr item (worktree or workspace) in `blocked` | `blocked · 9m` (amber) | Name "project · worktree"; meta line "herdr" |
| **Finished** | A session with "Your turn": idle after work, with output this phone has not seen | `your turn` (green) | – |
| **Finished** | A herdr item in `done` that was not opened on this phone in this visit | `done` (green) | Meta line "herdr" |

- In each part the longest wait is first (`stateSince`; an unknown time sorts last). The sort is stable: equal waits keep the computers' order.
- With several computers, each row shows the computer's colour dot and label (`ComputerTag`).
- The **Finished** label (caps) shows only when that part has rows.
- The count on the tab, the status line and the queue use only the first part (`waitingCount`).
- On the tab the Sessions status line says `3 need you` or `1 needs you` (`needsYouText`) with an amber dot while something waits. While nothing waits, the status line is the usual one and the list has one line: `Nothing needs you.` (`NOTHING`); what finished still shows under it.
- A session that exited or was lost is never an item. A session that is `working` is never an item.
- A herdr item without an `openTarget` is never an item (nothing to open).
- A row is one button. A tap opens the item (section 4).

## 4. Open an item

| Item | Opens | Control |
| --- | --- | --- |
| A session | Live. Its answer card is there. | Answering needs control: the existing **Take control to answer**. |
| A herdr item | The herdr terminal (`/p/…`), as a tap on its herdr row does | Read-only until the user takes control. Opening never takes control. |

A tap on any row (also a **Finished** row) starts a queue run. A tap on a herdr `done` row marks it as opened (`markOpened`), as a tap on its herdr row does.

## 5. The queue

A "queue run" is the state `visited` in `App.tsx`: the set of item keys this run already showed, or `undefined` when no run is on.

| Rule | Detail |
| --- | --- |
| Where | A row under the status line of Live, and above the **Open** page of a herdr terminal: the caps label `Needs you` and one button |
| The button | `Next · 2 left ›`. The number counts the items that still wait, were not shown in this run, and are not the one on screen. With none left: `Inbox ›` |
| `Inbox ›` | Goes to Sessions on the **Needs you** tab. That tab says `Nothing needs you.` when nothing waits |
| Always fresh | `queueStep` is computed from the newest lists on every render. An item that was answered, or that is gone, is never a step |
| No repeats | An item shown in this run is not offered again in the same run. An item the user skipped stays in the inbox |
| End of a run | The run ends when the user leaves the terminal screens (Sessions, Wall, Settings, New session). `queueScreen` names the screens where it stays on: Live, a herdr terminal, and the preview of the session on screen |
| No auto-advance | When the host accepts this phone's answer (answer phase `sent`), the Next button turns into the primary (filled) style. Nothing moves by itself |
| Keyboard | The row hides while the keyboard is up |

The queue never sends input, never takes control, and never queues or sends anything again (AGENTS.md rule 10). It is navigation only.

## 6. A notification tap starts the queue

A tap on a notification opens the item as before. Now it also starts a queue run with nothing shown yet, so Next is there.

The address never carries this. The guard is the one-time tap record that `/p/…` already had ([notifications.md](notifications.md), "The tap record"):

1. On `notificationclick`, before the window moves, the service worker stores a one-time record for the path (Cache Storage `pterm-taps`, about 30 s; memory only if the cache cannot be opened).
2. It now does this for `/sessions/<id>` and `/c/<host>/sessions/<id>` too (`SESSION_PATH` in `sw.js`), not only for `/p/…`.
3. The page asks the worker (`tap?`). The first answer uses the record up.
4. A fresh record: the queue starts (`queueOnArrival` for a session; `providerArrival` with `tapped` for a herdr terminal).

A link, a typed address, a reload, Back and Forward find no record and start no queue. For a session, the record changes nothing else: a session address opened Live at once before, and still does.

## 7. What it never does

- It shows no terminal text other than the question line that the Sessions row already shows.
- Names from a peer or from herdr are text only (`<bdi>`). They are never part of an address.
- It shows nothing from a computer that cannot be reached.
- It shows no herdr item from a herdr that is off, not installed, not running or unsupported, or from a computer below the provider revision (`FEATURE_REVISION.providers` = 4). Sessions of such a computer still show.
- It adds no notice of its own for these cases. The existing notices stay where they are (the **All** tab and the **herdr** tab).
- It sends no input, takes no control, and makes no new kind of request.

## 8. Known limits

| Limit | Why |
| --- | --- |
| herdr `done` "seen" is remembered only for this visit | The existing behaviour of the herdr rows (`seenDone` is in memory). After a reload, an opened `done` item is in **Finished** again until someone looks at it in herdr |
| A herdr item with two blocked agents is one row | herdr items are per worktree or workspace: the host gives one `openTarget` for each item |
| A session's question shows only when the app has it | The row shows what the session list carries; a waiting session without a question has no question line and no `Answer ›` |
| Not checked on a real phone | A real notification tap on an iPhone and on Android that starts the queue ([owner-verification.md](owner-verification.md)) |

## 9. Code map

| File | What |
| --- | --- |
| `web/src/screens/Inbox.tsx` | The whole feature: `inboxItems`, `waitingCount`, `inboxTabShown`, `needsYouText`, `itemAt`, `queueStep`, `nextLabel`, `QueueNext`, `InboxList` |
| `web/src/screens/Sessions.tsx` | The **Needs you** chip in `ComputerChips` (`needs`, `needsOpen`, `onNeeds`); the `inbox` tab state; the status line |
| `web/src/screens/App.tsx` | `visited` (the run), `queueScreen`, `queueNext`, `queueOnArrival`, `openItem`; the wider `useProviders` condition |
| `web/src/screens/Live.tsx` | The `queue` prop; `answered` (answer phase `sent`); renders `QueueNext` |
| `web/src/screens/ProviderScreen.tsx` | Passes `queue` to Live; renders `QueueNext` above the **Open** page |
| `web/src/sw.js` | `SESSION_PATH`: a session notification tap leaves the tap record |
| `web/src/styles/global.css` | `.inbox-row`, `.queue-bar` (hidden with `.live.keyboard`) |

## 10. Tests

| Where | What |
| --- | --- |
| `web/src/screens/Inbox.test.tsx` | The list, the count, the tab rule, the texts, the queue step |
| `web/src/screens/App.test.ts`, "the Needs you queue" | A confirmed tap starts a run and a link does not; the run is on only in terminal screens; Next order; answered and on-screen items |
| `web/src/sw.test.ts` | A session tap leaves one fresh tap for its address (home and peer); a tap that opens `/` or `/settings` leaves none |
| End to end | `N-inbox`, `N-open`, `N-fit`, `N-answer`, `N-next`, `N-tap`, `N-last` in `scripts/e2e-multicomputer/herdr.mjs` (see that folder's README; `--only N`) |
| Pictures | `node scripts/needs-you-pictures/capture.mjs` draws the app's components with demo data into `docs/evidence/needs-you-inbox/` and checks the widths at 375 and 390 px |

## Answer from the notification: what each phone can do

| Phone | Fact | What Termivoa does today |
| --- | --- | --- |
| iPhone, iPad | Web Push works only for a Home Screen app. iOS does **not** show custom notification action buttons: the user gets only the tap. | The tap opens the app on the item. The answer is one tap away (the answer card, after **Take control to answer** when needed), and Next goes to the rest. |
| Android (Chrome) | Chrome does support notification action buttons. | **Not built.** The same as iPhone: the tap opens the item, and Next goes to the rest. |

So "answer from the notification" on iPhone can only mean: the tap lands on the item with the answer close. That is built.

### Proposal (NOT built): answer buttons in an Android notification

**Critical feature under AGENTS.md rule 24.** It gives a locked phone a new authority. It needs its own ADR in `docs/decisions/`, a security review and a feasibility review (with alternatives), and the owner's approval **before any code**.

What it would need:

| Topic | What must be designed and decided |
| --- | --- |
| A new answer route | Today an answer goes over the live stream of an open session. A service worker has no live stream. It needs an authenticated HTTP route that the worker can call with the device cookie. That is a new route that sends input to a child (AGENTS.md rule 10: structured, authorized, one broker writer). |
| The question check | The host must check that the same question is still on screen: the question ID in the request must match, as the answer card does today. A changed question drops the answer. |
| Control | Today an answer needs the controller lease. Decide: the route takes control for one keypress, or it refuses when another device has control. |
| What a locked phone could then do | Press one of the offered answers on a waiting agent without an unlock, for example approve a command. Anyone who holds the locked phone could do it. |
| Limits and bounds | A rate limit for each device; one answer for each question; a bounded body; no retry and no queue (rule 10). |
| The payload | A push must still carry no terminal text. So the buttons could only be generic, unless the push carries the option labels. Option labels are terminal output: that is a privacy decision. |
| Opt-in | Off by default; on for each device, by the user. |
| iPhone | Gets nothing from this: iOS shows no action buttons. |
| Tests | Exactly one write for one tap; a stale question is refused; a revoked device is refused; another device's control is kept (or taken, as decided). |

Until that is approved and built, no client or service worker sends an answer, and every answer still needs the open app.
