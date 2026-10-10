import { useEffect, useRef, type RefObject } from 'react';
import '../styles/drawer.css';
import type { Computer } from '../computers';
import type { ComputerEntry } from './allSessions';
import type { InboxItem } from './Inbox';
import { countWords, groupsOf, shown, targetsOf, type ProviderEntry } from './providers';

/*
 * The side drawer (docs/ui-revamp.md, section 4): with two or more computers,
 * the computer to show (or All computers) and the ways out of Sessions.
 */

/** What waits for an answer on each computer, by host: the count of the Needs you tab, split by computer. */
export function needsByComputer(items: ReadonlyArray<InboxItem>): ReadonlyMap<string, number> {
  const counts = new Map<string, number>();
  for (const item of items) if (item.waits) counts.set(item.computer.host, (counts.get(item.computer.host) ?? 0) + 1);
  return counts;
}

/** The chosen computer's host while it is still in the list; undefined (All computers) once it left. */
export const chosenHost = (computers: ReadonlyArray<Pick<Computer, 'host'>>, chosen: string | undefined): string | undefined =>
  (computers.some((computer) => computer.host === chosen) ? chosen : undefined);

/** The amber pill: the first computer that is not the chosen one and has something that needs you. None in All computers. */
export function amberPill(computers: ReadonlyArray<Computer>, counts: ReadonlyMap<string, number>, chosen: string | undefined):
  { readonly computer: Computer; readonly count: number } | undefined {
  if (chosen === undefined) return undefined;
  const computer = computers.find((one) => one.host !== chosen && (counts.get(one.host) ?? 0) > 0);
  return computer === undefined ? undefined : { computer, count: counts.get(computer.host) ?? 0 };
}

const plural = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`;

/** One line of state for a computer: "2 sessions · 1 working", "can't reach", "pair again". */
export function stateLine(entry: ComputerEntry, providers: ReadonlyArray<ProviderEntry>): string {
  const { status } = entry;
  switch (status.kind) {
    case 'loading': return 'loading…';
    case 'connecting': return 'connecting…';
    case 'unreachable': case 'blocked': return "can't reach";
    case 'pair': return 'pair again';
    case 'outdated': return 'update Termivoa';
    default:
  }
  const mine = providers.filter((one) => shown(one) && one.computer.host === entry.computer.host);
  const working = status.sessions.filter((session) => session.agent?.state === 'working').length
    + (countWords(mine.flatMap((one) => targetsOf(groupsOf(one)))).get('working') ?? 0);
  return [plural(status.sessions.length, 'session'), ...(working > 0 ? [`${working} working`] : [])].join(' · ');
}

/** A count of what needs you, with words for screen readers. */
function Count({ count }: { readonly count: number }) {
  if (count === 0) return null;
  return <><span className="drawer-count" aria-hidden="true">{count}</span><span className="visually-hidden">, {count} {count === 1 ? 'needs' : 'need'} you</span></>;
}

/** The small header over one computer's part of a list in All computers: its dot, label and state. */
export function ComputerGroupHead({ entry, line }: { readonly entry: ComputerEntry; readonly line: string }) {
  return (
    <h2 className={`computer-group is-${entry.status.kind}`}>
      <span className="dot" style={{ background: entry.computer.color }} aria-hidden="true" />
      <bdi>{entry.computer.label}</bdi>
      <span className="computer-group-state"><span className="visually-hidden"> · </span>{line}</span>
    </h2>
  );
}

/** ≡, the pill with the chosen computer, and the amber pill. Shown only with two or more computers. */
export function DrawerBar({ chosen, open, amber, menuRef, onOpen, onChoose }: {
  readonly chosen: Computer | undefined;
  /** The ≡ button: focus comes back to it when the drawer closes (iOS does not focus a tapped button). */
  readonly menuRef?: RefObject<HTMLButtonElement | null> | undefined;
  readonly open: boolean;
  readonly amber: ReturnType<typeof amberPill>;
  readonly onOpen: () => void;
  readonly onChoose: (host: string) => void;
}) {
  return (
    <div className="drawer-bar">
      <button ref={menuRef} type="button" className="round drawer-menu" aria-label="Computers and menu" aria-haspopup="dialog" aria-expanded={open} onClick={onOpen}>
        <svg width="18" height="14" viewBox="0 0 18 14" aria-hidden="true">
          <path d="M1 1.5h16M1 7h16M1 12.5h16" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
        </svg>
      </button>
      <button type="button" className="drawer-pill" aria-haspopup="dialog" aria-expanded={open} onClick={onOpen}>
        {chosen === undefined ? 'All computers' : <><span className="dot" style={{ background: chosen.color }} aria-hidden="true" /><bdi>{chosen.label}</bdi></>}
        <span className="drawer-caret" aria-hidden="true">▾</span>
      </button>
      {amber !== undefined && (
        <button type="button" className="drawer-amber" onClick={() => onChoose(amber.computer.host)}>
          <span className="dot amber" aria-hidden="true" />
          <bdi>{amber.computer.label}</bdi>
          <Count count={amber.count} />
        </button>
      )}
    </div>
  );
}

interface DrawerProps {
  readonly entries: ReadonlyArray<ComputerEntry>;
  readonly chosen: string | undefined;
  readonly counts: ReadonlyMap<string, number>;
  /** The count by Needs you, over every computer; undefined: no Needs you row (the app gives no way to open an item). */
  readonly needs: number | undefined;
  readonly line: (entry: ComputerEntry) => string;
  readonly onChoose: (host: string | undefined) => void;
  readonly onNeeds: () => void;
  readonly onWall: () => void;
  readonly onSettings: () => void;
  readonly onAddComputer: () => void;
  readonly onClose: () => void;
}

/** A modal dialog (native, so focus stays in it and the page behind is inert). Escape, a tap beside the panel, or a choice closes it. */
export function Drawer({ returnFocus, onClose, ...props }: DrawerProps & {
  /** Gets the focus back when the drawer closes. */
  readonly returnFocus?: RefObject<HTMLElement | null> | undefined;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    if (dialog !== null && !dialog.open) {
      dialog.showModal();
      // showModal focuses All computers, and iOS Safari then rings it; the dialog takes the focus, Tab still reaches every row.
      dialog.focus();
    }
    return () => {
      dialog?.close();
      returnFocus?.current?.focus();
    };
  }, [returnFocus]);
  return (
    <dialog
      ref={ref}
      className="drawer"
      tabIndex={-1}
      aria-labelledby="drawer-title"
      onCancel={(event) => { event.preventDefault(); onClose(); }}
      // The dialog fills the screen; a tap on it, not on the panel, is a tap beside the panel.
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <DrawerPanel {...props} onClose={onClose} />
    </dialog>
  );
}

/** What the drawer shows: Computers, then Needs you, Wall, Settings and + Add a computer. Each choice closes the drawer. */
export function DrawerPanel({ entries, chosen, counts, needs, line, onChoose, onNeeds, onWall, onSettings, onAddComputer, onClose }: DrawerProps) {
  const total = [...counts.values()].reduce((sum, count) => sum + count, 0);
  const pick = (host: string | undefined) => { onChoose(host); onClose(); };
  const go = (action: () => void) => () => { onClose(); action(); };
  return (
    <div className="drawer-panel">
        <h2 id="drawer-title" className="caps">Computers</h2>
        <ul className="drawer-list">
          <li>
            <button type="button" className="drawer-row" aria-current={chosen === undefined} onClick={() => pick(undefined)}>
              <span className="drawer-tile all" aria-hidden="true">
                <svg width="14" height="14" viewBox="0 0 18 18" fill="none">
                  <rect x="2" y="2" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
                  <rect x="10.5" y="2" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
                  <rect x="2" y="10.5" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
                  <rect x="10.5" y="10.5" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
                </svg>
              </span>
              <span className="drawer-text"><span className="drawer-label">All computers</span></span>
              <Count count={total} />
            </button>
          </li>
          {entries.map((entry) => (
            <li key={entry.computer.host}>
              <button
                type="button"
                className={`drawer-row is-${entry.status.kind}`}
                aria-current={chosen === entry.computer.host}
                onClick={() => pick(entry.computer.host)}
              >
                <span className="drawer-tile" aria-hidden="true">{[...entry.computer.label][0]?.toUpperCase()}</span>
                <span className="drawer-text">
                  <span className="drawer-label"><bdi>{entry.computer.label}</bdi></span>
                  <span className="drawer-state"><span className="visually-hidden">, </span>{line(entry)}</span>
                </span>
                <Count count={counts.get(entry.computer.host) ?? 0} />
              </button>
            </li>
          ))}
        </ul>
        <ul className="drawer-list drawer-foot">
          {needs !== undefined && (
            <li>
              <button type="button" className="drawer-row" onClick={go(onNeeds)}>
                <span className="drawer-label">Needs you</span>
                <Count count={needs} />
              </button>
            </li>
          )}
          <li><button type="button" className="drawer-row" onClick={go(onWall)}><span className="drawer-label">Wall</span></button></li>
          <li><button type="button" className="drawer-row" onClick={go(onSettings)}><span className="drawer-label">Settings</span></button></li>
          <li><button type="button" className="drawer-row drawer-add" onClick={go(onAddComputer)}><span className="drawer-label">+ Add a computer</span></button></li>
        </ul>
    </div>
  );
}
