# UI revamp: side drawer, Settings by computer, Geist (2026-10-10)

- Status: **approved by the owner** on 2026-10-10 ("herdr B, Settings S2, font A … ship it"). Not critical under AGENTS.md rule 24 (no change to authentication, pairing, trust, network surface, protocol or stored secrets).
- Pictures (Paper, demo data): [`docs/evidence/ui-revamp/`](evidence/ui-revamp/). Chosen: `design-B1-switcher.png`, `design-B2-drawer.png`, `design-S2-computers.png`, `design-S2b-mac.png`, `design-font-a-geist.png`.

## 1. Why

With two or more computers, the herdr tab and Sessions list every computer's projects in one long list with a small "on mac" tag, and the computer chips crowd the top. Settings is one long page; **Integrations** has one large row for each computer and provider. The serif titles, bone colour and spaced mono caps read like another product, and the caps make every screen feel full of text.

## 2. Rules for the build

- **No control is removed.** Every button, switch, line and state that the app has today exists after the change, maybe on another screen.
- **No protocol, host or stored-data change.** Web app only (and the e2e harness, docs and screenshots that follow it).
- **Texts stay exact** where the docs or tests quote them, unless this document changes them.
- A phone with **one computer** sees no drawer button: its top bar stays as today.

## 3. Typography (font A)

| Part | Today | After |
| --- | --- | --- |
| Body | `system-ui` | **Geist**, bundled with the app (npm `geist`, SIL Open Font License 1.1; woff2 files served by the gateway, nothing from the internet) |
| Titles (`h1`, `h2`) | serif (`ui-serif`, New York), weight 400 | Geist 600, letter spacing -0.03em |
| Code, branch and worktree names, build IDs, history text | `ui-monospace` | **Geist Mono** |
| The live terminal (xterm) | system mono list | **Unchanged.** xterm measures its cells once; a font that arrives late (`font-display: swap`) would break the grid |
| `.caps` section labels | mono 11px, uppercase, 0.14em spacing | Geist 13px, weight 500, muted, **no uppercase**, normal spacing |
| State words (`.state-word`, `.computer-state`, `.status-pill`, `.controller`, `.group-where`, `.faint`, `.host-line`) | mono 11px uppercase | Geist 12px, weight 500, no uppercase |
| `.row-meta` | mono 12px | Geist 13px (stays mono where it shows a path or code) |

Text written in upper case in the code for display ("CONNECTED", "GIT MISSING") becomes sentence case ("Connected", "git missing").

## 4. Side drawer (design B)

- With two or more computers, the Sessions top bar gets a **menu button** (≡, label "Computers and menu") at the left and a **computer pill** next to it: "All computers", or "● mac" for one computer. Tapping either opens the drawer.
- An **amber pill** ("vps 1") shows when a computer that is not the chosen one has items that need you (blocked or waiting agents, as counted by Needs you). Tapping it chooses that computer.
- The **drawer** slides in from the left (300px, dimmed page behind; Escape, a tap outside, or a choice closes it; focus moves in and comes back to the button):
  - "Computers": **All computers**, then one row per computer: a letter tile, the label, one line of state (sessions and agents, or "can't reach", "pair again"), and an amber count of what needs you there. The chosen row is highlighted.
  - At the bottom: **Needs you** (with its count), **Wall**, **Settings**, **+ Add a computer** (opens Settings at Add a computer).
- Choosing a computer filters the whole app the way the computer chips do today: Sessions, herdr and Needs you. The computer chips leave the top bar.
- The tabs under the title become **Sessions · herdr · Needs you** (today's "All" is "Sessions"; provider tabs keep their counts; Needs you shows while something waits or while it is open, as today).
- In **All computers**, the Sessions and herdr lists get a small header per computer (label and state) so that projects of different computers do not mix.
- No swipe gesture in this version (it can fight with scrolling and with the terminal).

## 5. Settings by computer (design S2)

Settings has two levels.

**Main page:**

1. **Computers**: one card per computer, home first. A card shows a status dot, the label, a "Home" badge for home, and chips: **herdr** (dot: running green, not running amber, off or not installed grey) when that computer lists herdr; **GitHub** (connected green, "Connect GitHub" dashed when not connected, "git missing" amber) when it reports GitHub; "N projects". A computer that cannot be reached shows a dashed card with "Can't reach" and today's health line. Tap a card: its computer page.
2. **Found on your network** (when there are offers) and **+ Add a computer**, as today.
3. **This phone**: Name (today's device name form), Notifications (today's Notifications section, on its own page), Terminal text size (A−, size, A+ in the row), the web and host build, and Log out (today's Log out section).

**Computer page** (header "‹ Settings", the label, its state and address):

- **herdr**: today's Integrations row for that computer (state, On, Buzz when blocked, Buzz when finished, Install herdr, Start herdr, the "This phone gets no buzz yet." line).
- **GitHub**: today's GitHub row for that computer (Connect, Set up a repository, Disconnect, Install git, Check again and every line).
- **This computer**: Projects (count), Check again, Rename, Remove (today's texts and confirmations).

The phone's Back (history) goes from a computer page to the main page.

## 6. Follow-ups

- The e2e harness and the docs screenshots follow the new screens in the same pull request.
- Done (2026-10-10): every "Settings → Integrations" path in docs/ and the docs site now says "Settings → (the computer)". "Settings → Computers" stays where it quotes the `pterm pair --via` output, which still prints it.
