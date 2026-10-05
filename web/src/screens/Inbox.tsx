import type { Computer } from '../computers';
import type { HTTPProviderItem } from '../generated/protocol';
import { agentBadge, hasNewOutput, since, viewedKey, type ViewedTimes } from './activity';
import { sameSession, type HostedSession } from './allSessions';
import { sessionName } from './folder';
import { dimmed, Names, type ProviderTap } from './Herdr';
import { groupsOf, shown, type ProviderEntry } from './providers';
import { ComputerTag, ended } from './Sessions';

/*
 * The "Needs you" inbox (docs/needs-you-inbox.md): one list of what waits for
 * the user on every computer, built only from what the app already reads
 * (the session lists and the provider lists). It shows no terminal text but
 * the question the Sessions row already shows. Names from a peer or from a
 * provider are text only (bdi), and never part of an address.
 */

export interface InboxItem {
  /** The same while the item waits: the queue remembers what it showed by this. */
  readonly key: string;
  readonly computer: Computer;
  /** True: it waits for an answer (the count and the queue). False: it finished and was not looked at yet. */
  readonly waits: boolean;
  readonly word: 'waiting' | 'blocked' | 'your turn' | 'done';
  /** When the state began; undefined when unknown. */
  readonly since: string | undefined;
  /** ["api"], or ["shop", "silver-meadow"] (project, worktree). */
  readonly names: ReadonlyArray<string>;
  /** A waiting session's question, as its Sessions row shows it. Terminal output: text only. */
  readonly question?: string | undefined;
  /** "herdr" for a provider terminal. */
  readonly provider?: string | undefined;
  readonly open:
    | { readonly kind: 'session'; readonly session: HostedSession }
    | { readonly kind: 'provider'; readonly tap: ProviderTap; readonly item: HTTPProviderItem };
}

/** What is on screen in Live: a session, or a provider terminal. */
export type Place = { readonly session: HostedSession } | { readonly tap: ProviderTap };

const started = (item: InboxItem): number => {
  const at = Date.parse(item.since ?? '');
  return Number.isNaN(at) ? Infinity : at;
};

/**
 * Everything that needs the user, most urgent first: what waits for an answer
 * (sessions in `waiting`, provider items in `blocked`), then what finished and
 * was not looked at (sessions with "Your turn", provider items in `done` not
 * opened here since). In each part, the longest wait is first. A computer or
 * provider that did not answer has nothing here: its own notice says so.
 */
export function inboxItems(sessions: ReadonlyArray<HostedSession>, providers: ReadonlyArray<ProviderEntry>, viewed: ViewedTimes): InboxItem[] {
  const fromSessions = sessions.flatMap((session): InboxItem[] => {
    const badge = ended(session) ? undefined : agentBadge(session, hasNewOutput(session, viewed));
    if (badge === undefined || badge.label === 'Working') return [];
    const waits = badge.label === 'Waiting';
    return [{
      key: `s:${viewedKey(session)}`, computer: session.computer, waits, word: waits ? 'waiting' : 'your turn', since: session.agent?.stateSince,
      names: [sessionName(session)], question: waits ? session.agent?.question?.text : undefined, open: { kind: 'session', session },
    }];
  });
  const fromProviders = providers.filter(shown).flatMap((entry) => groupsOf(entry).flatMap((group) => group.items.flatMap((item): InboxItem[] => {
    const waits = item.state === 'blocked';
    if (item.openTarget === undefined || !(waits || (item.state === 'done' && !dimmed(entry.computer, item)))) return [];
    return [{
      key: `p:${entry.computer.host}/${entry.info.id}/${item.id}`, computer: entry.computer, waits, word: waits ? 'blocked' : 'done', since: item.stateSince,
      names: group.name === item.name ? [item.name] : [group.name, item.name], provider: entry.info.name,
      open: { kind: 'provider', tap: { computer: entry.computer, provider: entry.info.id, targetId: item.openTarget }, item },
    }];
  })));
  // sort is stable: equal waits keep the computers' order.
  return [...fromSessions, ...fromProviders].sort((a, b) => Number(b.waits) - Number(a.waits) || started(a) - started(b));
}

/** The count on the "Needs you" tab and in the queue: what waits for an answer. */
export const waitingCount = (items: ReadonlyArray<InboxItem>): number => items.filter((item) => item.waits).length;

/** The "Needs you" tab shows only while something waits, or while it is the open tab (so "Nothing needs you." has a place). */
export const inboxTabShown = (count: number, open: boolean): boolean => count > 0 || open;

/** The one line of the "Needs you" tab while nothing waits. */
export const NOTHING = 'Nothing needs you.';
/** The Sessions status line on the "Needs you" tab while something waits: "1 needs you", "3 need you". */
export const needsYouText = (count: number): string => (count === 1 ? '1 needs you' : `${count} need you`);

/** The inbox item that is on screen, or undefined (it stopped needing the user, or it was never in the inbox). */
export function itemAt(items: ReadonlyArray<InboxItem>, place: Place): InboxItem | undefined {
  return items.find((item) => ('session' in place
    ? item.open.kind === 'session' && sameSession(item.open.session, place.session)
    : item.open.kind === 'provider' && item.computer.origin === place.tap.computer.origin && item.open.tap.provider === place.tap.provider
      && item.open.item.targets.some((target) => target.id === place.tap.targetId)));
}

/**
 * The queue's next step from the item on screen: the first item that still
 * waits and was not shown in this run (`visited`), and how many such items
 * are left. No `next`: the run is over, back to the inbox. Read from the
 * newest lists each time, so an item that was answered or that left is never
 * a step, and a skipped item is not offered twice in one run.
 */
export function queueStep(items: ReadonlyArray<InboxItem>, visited: ReadonlySet<string>, current: string | undefined): { readonly next: InboxItem | undefined; readonly left: number } {
  const rest = items.filter((item) => item.waits && item.key !== current && !visited.has(item.key));
  return { next: rest[0], left: rest.length };
}

/** "Next · 2 left ›", or "Inbox ›" on the last one. */
export const nextLabel = (left: number): string => (left > 0 ? `Next · ${left} left ›` : 'Inbox ›');

/** What Live needs for the queue: present only for an item opened from the inbox or from a notification tap. */
export interface Queue {
  readonly left: number;
  readonly onNext: () => void;
}

/** The queue's one control in Live, under the status line. Bold once this phone's answer was accepted. */
export function QueueNext({ queue, answered = false }: { readonly queue: Queue; readonly answered?: boolean }) {
  return (
    <div className="queue-bar">
      <span className="caps">Needs you</span>
      <button type="button" className={answered ? 'compact primary' : 'compact'} onClick={queue.onNext}>{nextLabel(queue.left)}</button>
    </div>
  );
}

function InboxRow({ item, multi, now, onOpen }: { readonly item: InboxItem; readonly multi: boolean; readonly now: number; readonly onOpen: (item: InboxItem) => void }) {
  const waited = since(item.since, now);
  return (
    <li>
      <button type="button" className="session-row inbox-row" onClick={() => onOpen(item)}>
        <span className="row-text">
          <span className="row-head">
            <span className="row-title"><Names parts={item.names} /></span>
            <span className={`state-word ${item.waits ? 'amber' : 'green'}`}>{waited === '' ? item.word : `${item.word} · ${waited}`}</span>
          </span>
          {(multi || item.provider !== undefined) && (
            <span className="row-meta">
              {multi && <ComputerTag computer={item.computer} />}
              {multi && item.provider !== undefined && ' · '}
              {item.provider !== undefined && <bdi>{item.provider}</bdi>}
            </span>
          )}
          {item.question !== undefined && (
            <>
              <span className="question">{item.question}</span>
              <span className="answer-link">Answer ›</span>
            </>
          )}
        </span>
      </button>
    </li>
  );
}

/** The "Needs you" tab: what waits (or one line when nothing does), then, under "Finished", what is done and not looked at. */
export function InboxList({ items, multi, now, onOpen }: {
  readonly items: ReadonlyArray<InboxItem>;
  /** The phone uses more than one computer: name each row's computer. */
  readonly multi: boolean;
  readonly now: number;
  readonly onOpen: (item: InboxItem) => void;
}) {
  const waiting = items.filter((item) => item.waits);
  const finished = items.filter((item) => !item.waits);
  const rows = (list: ReadonlyArray<InboxItem>) => list.length > 0 && (
    <ul className="rows">{list.map((item) => <InboxRow key={item.key} item={item} multi={multi} now={now} onOpen={onOpen} />)}</ul>
  );
  return (
    <section aria-label="Needs you">
      {waiting.length === 0 && <p className="muted">{NOTHING}</p>}
      {rows(waiting)}
      {finished.length > 0 && <h2 className="caps list-label">Finished</h2>}
      {rows(finished)}
    </section>
  );
}
