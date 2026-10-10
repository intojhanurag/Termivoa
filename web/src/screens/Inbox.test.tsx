import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { viewedKey } from './activity';
import { hosted, type HostedSession } from './allSessions';
import { markOpened } from './Herdr';
import { InboxList, inboxItems, inboxTabShown, itemAt, needsYouText, NEXT_AFTER_MS, nextFilled, nextLabel, nextReady, NO_ANSWER, NOTHING, QueueNext, queueStep, waitingCount } from './Inbox';
import { group, item, mac, okEntry, providerEntry, ready, shopGroups, snapshot, target, vps } from './providerFixtures';
import { providerSources } from './providers';
import { ComputerChips, Sessions } from './Sessions';

const NOW = Date.parse('2026-10-04T10:00:00Z');
let ids = 0;
const session = (computer: typeof mac, title: string, extra: Partial<BrokerRPCSessionRecord> = {}): HostedSession => hosted({
  id: `84444444-4444-4444-8444-${String(++ids).padStart(12, '0')}`, generation: '85555555-5555-4555-8555-555555555555', title,
  projectId: 'p', shellProfileId: 's', state: 'running', columns: 80, rows: 24, createdAt: '2026-10-04T08:00:00Z', updatedAt: '2026-10-04T08:00:00Z', ...extra,
}, computer);
const waiting = (computer: typeof mac, title: string, since: string, text?: string) => session(computer, title, {
  agent: { state: 'waiting', stateSince: since, ...(text === undefined ? {} : { question: { id: 'q', text, options: [{ label: 'Yes' }] } }) },
});
const turn = (computer: typeof mac, title: string) => session(computer, title, {
  lastOutputAt: '2026-10-04T09:30:00Z', agent: { state: 'idle', stateSince: '2026-10-04T09:30:00Z', afterWork: true },
});
const names = (items: ReturnType<typeof inboxItems>) => items.map((one) => `${one.computer.label}:${one.names.join('/')}:${one.word}`);

describe('inboxItems', () => {
  const api = waiting(mac, 'api', '2026-10-04T09:55:00Z', 'Run the test suite?');
  const web = waiting(vps, 'web', '2026-10-04T09:40:00Z');
  const all = [session(mac, 'idle shell'), session(mac, 'busy', { agent: { state: 'working', stateSince: '2026-10-04T09:00:00Z' } }), api, turn(mac, 'docs'), web];

  it('lists what waits on every computer, the longest wait first, then what finished and was not looked at', () => {
    const items = inboxItems(all, [ready(mac)], {});
    // silver-meadow is blocked since 09:51; lucky-valley is done (no time: last in its part).
    expect(names(items)).toEqual(['vps:web:waiting', 'mac:shop/silver-meadow:blocked', 'mac:api:waiting', 'mac:docs:your turn', 'mac:shop/lucky-valley:done']);
    expect(waitingCount(items)).toBe(3);
    expect(items[2]?.question).toBe('Run the test suite?');
    expect(items[1]).toMatchObject({ provider: 'herdr', open: { kind: 'provider', tap: { computer: mac, provider: 'herdr', targetId: 'w8:p1.term_b1' } } });
  });

  it('leaves out working and idle sessions, ended ones, output this phone already saw, and a herdr item with nothing to open', () => {
    const seen = turn(mac, 'seen');
    const gone = { ...waiting(mac, 'gone', '2026-10-04T09:00:00Z'), state: 'exited' as const };
    const { openTarget: _, ...noTarget } = item('w1', 'main', [target('t1', 'blocked')]);
    expect(inboxItems([seen, gone], [ready(mac, [group('g', 'shop', [noTarget])])], { [viewedKey(seen)]: NOW })).toEqual([]);
  });

  it('a herdr item in done leaves once it was opened here; one named like its project shows one name', () => {
    const groups = [group('ws', 'notes', [item('w5', 'notes', [target('t5', 'done')], { stateSince: '2026-10-04T09:00:00Z' })])];
    expect(names(inboxItems([], [ready(vps, groups)], {}))).toEqual(['vps:notes:done']);
    markOpened(vps, groups[0]!.items[0]!);
    expect(inboxItems([], [ready(vps, groups)], {})).toEqual([]);
  });

  it('shows nothing from a provider that cannot be reached, is off, is not running, or whose computer is below the provider revision', () => {
    expect(inboxItems([], [providerEntry(mac, { kind: 'unreachable' }), providerEntry(mac, { kind: 'loading' })], {})).toEqual([]);
    expect(inboxItems([], [providerEntry(mac, { kind: 'ok', snapshot: snapshot(shopGroups(), { state: 'off' }), at: 0 })], {})).toEqual([]);
    expect(inboxItems([], [providerEntry(mac, { kind: 'ok', snapshot: snapshot(shopGroups(), { state: 'notRunning' }), at: 0 })], {})).toEqual([]);
    // Below revision 4 the app reads no provider at all, so there is no entry to list.
    expect(providerSources([okEntry(vps, { protocolRevision: 3 })])).toEqual([]);
  });

  it('gives two computers with the same session id and the same herdr ids different keys', () => {
    const same = { id: '84444444-4444-4444-8444-444444444444' };
    const items = inboxItems([{ ...waiting(mac, 'a', '2026-10-04T09:00:00Z'), ...same }, { ...waiting(vps, 'a', '2026-10-04T09:00:00Z'), ...same }], [ready(mac), ready(vps)], {});
    expect(new Set(items.map((one) => one.key)).size).toBe(items.length);
    expect(items).toHaveLength(6);
  });
});

describe('the Needs you tab', () => {
  it('shows only while something waits, or while it is open', () => {
    expect(inboxTabShown(0, false)).toBe(false);
    expect(inboxTabShown(1, false)).toBe(true);
    expect(inboxTabShown(0, true)).toBe(true);
  });

  it('says how many need you', () => {
    expect([1, 3].map(needsYouText)).toEqual(['1 needs you', '3 need you']);
    expect(NOTHING).toBe('Nothing needs you.');
  });
});

describe('queueStep', () => {
  const a = waiting(mac, 'a', '2026-10-04T09:00:00Z');
  const b = waiting(vps, 'b', '2026-10-04T09:10:00Z');
  const items = () => inboxItems([a, b, turn(mac, 'docs')], [ready(mac)], {});
  const [first, second, third] = items().map((one) => one.key) as [string, string, string];

  it('goes to the next waiting item from each position, and counts what is left; finished items are never a step', () => {
    expect(queueStep(items(), new Set([first]), first)).toMatchObject({ next: { key: second }, left: 2 });
    expect(queueStep(items(), new Set([first, second]), second)).toMatchObject({ next: { key: third }, left: 1 });
    // Opened in the middle: the ones before it are still left.
    expect(queueStep(items(), new Set([second]), second)).toMatchObject({ next: { key: first }, left: 2 });
  });

  it('ends after the last one: no next, back to the inbox, also while skipped items still wait', () => {
    expect(queueStep(items(), new Set([first, second, third]), third)).toEqual({ next: undefined, left: 0 });
    expect(nextLabel(2)).toBe('Next · 2 left ›');
    expect(nextLabel(0)).toBe('Inbox ›');
  });

  it('an item that stopped waiting while open is not current any more, and the rest still counts', () => {
    const later = inboxItems([{ ...a, agent: { state: 'working', stateSince: '2026-10-04T10:00:00Z' } }, b], [ready(mac)], {});
    expect(itemAt(later, { session: a })).toBeUndefined();
    expect(queueStep(later, new Set([first]), undefined)).toMatchObject({ next: { key: second }, left: 2 });
  });

  it('an item that left the lists is never the next step', () => {
    const later = inboxItems([a], [ready(mac, [])], {});
    expect(queueStep(later, new Set([first]), first)).toEqual({ next: undefined, left: 0 });
  });

  it('a notification tap starts with nothing shown: the open item is skipped by where the user is', () => {
    expect(queueStep(items(), new Set(), itemAt(items(), { session: b })?.key)).toMatchObject({ next: { key: first }, left: 2 });
  });
});

describe('itemAt', () => {
  it('finds a session by computer, id and generation, and a herdr item by any of its terminals on that computer', () => {
    const a = waiting(mac, 'a', '2026-10-04T09:00:00Z');
    const two = [group('g', 'shop', [item('w8', 'silver-meadow', [target('t1', 'blocked'), target('t2', 'working')])])];
    const items = inboxItems([a], [ready(mac, two)], {});
    expect(itemAt(items, { session: a })?.names).toEqual(['a']);
    expect(itemAt(items, { session: { ...a, computer: vps } })).toBeUndefined();
    expect(itemAt(items, { tap: { computer: mac, provider: 'herdr', targetId: 't2' } })?.names).toEqual(['shop', 'silver-meadow']);
    expect(itemAt(items, { tap: { computer: vps, provider: 'herdr', targetId: 't2' } })).toBeUndefined();
  });
});

describe('InboxList', () => {
  const list = (sessions: HostedSession[], multi: boolean, groups = shopGroups()) => renderToStaticMarkup(
    <InboxList items={inboxItems(sessions, [ready(mac, groups)], {})} multi={multi} now={NOW} onOpen={() => undefined} />,
  );

  it('shows the name, the state word with the wait, the question and Answer; the computer only with several computers', () => {
    const html = list([waiting(vps, 'api', '2026-10-04T09:56:00Z', 'Run the test suite?')], true);
    expect(html).toContain('<span class="row-title"><bdi>shop</bdi> · <bdi>silver-meadow</bdi></span><span class="state-word amber">blocked · 9m</span>');
    expect(html).toContain('<span class="state-word amber">waiting · 4m</span>');
    expect(html).toContain('<span class="question">Run the test suite?</span><span class="answer-link">Answer ›</span>');
    expect(html).toContain('vps</span>');
    expect(html).toContain('mac</span> · <bdi>herdr</bdi>');
    expect(html).toContain('<h2 class="caps list-label">Finished</h2>');
    expect(html.indexOf('Finished')).toBeLessThan(html.indexOf('lucky-valley'));
    const one = list([waiting(mac, 'api', '2026-10-04T09:56:00Z')], false);
    expect(one).not.toContain('computer-tag');
    expect(one).not.toContain('Answer ›');
  });

  it('renders names and the question as text only, each name isolated, and never the terminal preview line', () => {
    const hostile = waiting(vps, '<img src=x onerror=alert(1)>', '2026-10-04T09:56:00Z', '<b>rm -rf</b>?');
    const html = list([{ ...hostile, preview: 'SECRET terminal line' }], true, [group('g', '‮evil<script>', [item('w', 'x', [target('t', 'blocked')])])]);
    expect(html).not.toContain('<img');
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('<b>');
    expect(html).not.toContain('SECRET');
    expect(html).toContain('<bdi>‮evil&lt;script&gt;</bdi>');
    expect(html).not.toContain('href');
  });

  it('with nothing finished, has no Finished label; with nothing waiting, one line, and what finished still shows', () => {
    expect(list([], false, [])).toBe('<section aria-label="Needs you"><p class="muted">Nothing needs you.</p></section>');
    expect(renderToStaticMarkup(<InboxList items={[]} multi loading now={NOW} onOpen={() => undefined} />)).toBe('<section aria-label="Needs you"></section>');
    const waits = list([waiting(mac, 'api', '2026-10-04T09:56:00Z')], false, []);
    expect(waits).not.toContain('Finished');
    expect(waits).not.toContain(NOTHING);
    const finished = list([turn(mac, 'docs')], false, []);
    expect(finished.indexOf(NOTHING)).toBeLessThan(finished.indexOf('Finished'));
    expect(finished).toContain('<span class="state-word green">your turn · 30m</span>');
  });
});

describe('InboxList while a computer did not answer', () => {
  const list = (sessions: HostedSession[]) => renderToStaticMarkup(<InboxList items={inboxItems(sessions, [], {})} multi partial now={NOW} onOpen={() => undefined} />);

  it('never says that nothing needs you: one short line instead, and the same line under what waits', () => {
    expect(NO_ANSWER).toBe('Some computers did not answer. See Sessions.');
    expect(list([])).toBe(`<section aria-label="Needs you"><p class="muted">${NO_ANSWER}</p></section>`);
    const waits = list([waiting(mac, 'api', '2026-10-04T09:56:00Z')]);
    expect(waits).not.toContain(NOTHING);
    expect(waits.indexOf('api')).toBeLessThan(waits.indexOf(NO_ANSWER));
    expect(waits.split(NO_ANSWER)).toHaveLength(2);
  });
});

describe('the Next button', () => {
  it('takes no tap for 400 ms after the queue moved, so a double tap does not skip an item', () => {
    expect(NEXT_AFTER_MS).toBe(400);
    expect(nextReady(1_000, 1_000)).toBe(false);
    expect(nextReady(1_000, 1_399)).toBe(false);
    expect(nextReady(1_000, 1_400)).toBe(true);
    expect(nextReady(0, 1_000)).toBe(true);
  });

  it('is filled after the accepted answer, also once the question left, until the session asks another question', () => {
    expect(nextFilled(undefined, 'q1')).toBe(false);
    expect(nextFilled(undefined, undefined)).toBe(false);
    expect(nextFilled('q1', 'q1')).toBe(true);
    expect(nextFilled('q1', undefined)).toBe(true);
    expect(nextFilled('q1', 'q2')).toBe(false);
  });
});

describe('QueueNext', () => {
  it('is one button with the count left; bold once the answer was accepted', () => {
    const html = renderToStaticMarkup(<QueueNext queue={{ left: 2, onNext: () => undefined }} />);
    expect(html).toBe('<div class="queue-bar"><span class="caps">Needs you</span><button type="button" class="compact">Next · 2 left ›</button></div>');
    expect(renderToStaticMarkup(<QueueNext queue={{ left: 0, onNext: () => undefined }} answered />)).toContain('class="compact primary">Inbox ›</button>');
  });
});

describe('Sessions with the Needs you tab', () => {
  const base = {
    offerPush: false, onOpen: () => undefined, onNew: () => undefined, onWall: () => undefined, onSettings: () => undefined,
    onReload: async () => undefined, onRetry: async () => undefined, onFailure: () => '', onOpenItem: () => undefined,
  };
  const api = waiting(mac, 'api', '2026-10-04T09:55:00Z', 'Run the test suite?');
  const TAB = /<button type="button" class="chip" aria-pressed="(true|false)">Needs you(?:<span class="chip-count">(\d+)<\/span>)?<\/button>/u;

  it('has the tab with its count after Sessions and herdr while something waits, on one computer too; the Sessions tab stays as it was', () => {
    const page = renderToStaticMarkup(<Sessions {...base} entries={[okEntry(mac), okEntry(vps)]} sessions={[api]} providers={[ready(mac)]} />);
    expect(TAB.exec(page)?.slice(1)).toEqual(['false', '2']);
    expect(page.indexOf('>Sessions</button>')).toBeLessThan(page.indexOf('>herdr'));
    expect(page.indexOf('>herdr')).toBeLessThan(page.indexOf('>Needs you'));
    expect(page).toContain('aria-label="Your sessions"');
    const one = renderToStaticMarkup(<Sessions {...base} entries={[okEntry(mac)]} sessions={[api]} />);
    expect(TAB.exec(one)?.slice(1)).toEqual(['false', '1']);
    expect(one).toContain('aria-pressed="true">Sessions</button>');
  });

  it('has no tab while nothing waits (finished items alone do not show it), or where the app gives no way to open an item', () => {
    const done = [group('g', 'shop', [item('w9', 'lucky-valley', [target('t9', 'done')])])];
    expect(renderToStaticMarkup(<Sessions {...base} entries={[okEntry(mac), okEntry(vps)]} sessions={[turn(mac, 'docs')]} providers={[ready(mac, done)]} />)).not.toContain('Needs you');
    expect(renderToStaticMarkup(<Sessions {...base} onOpenItem={undefined} entries={[okEntry(mac)]} sessions={[api]} />)).not.toContain('Needs you');
    expect(renderToStaticMarkup(<ComputerChips computers={[mac, vps]} chosen={undefined} onChoose={() => undefined} />)).not.toContain('Needs you');
  });

  it('opened on the tab: the list in place of the sessions, the count on the status line, the other tabs still there', () => {
    const page = renderToStaticMarkup(<Sessions {...base} inbox entries={[okEntry(mac), okEntry(vps)]} sessions={[api]} providers={[ready(mac)]} />);
    expect(TAB.exec(page)?.slice(1)).toEqual(['true', '2']);
    expect(page).toContain('<span class="dot amber" aria-hidden="true"></span>2 need you</p>');
    expect(page).toContain('<section aria-label="Needs you">');
    expect(page).not.toContain('aria-label="Your sessions"');
    expect(page).toContain('aria-pressed="false">Sessions</button>');
    expect(page).toContain('aria-pressed="false">herdr');
  });

  it('opened on the tab with nothing waiting: one line, and the tab stays while it is open', () => {
    const page = renderToStaticMarkup(<Sessions {...base} inbox entries={[okEntry(mac)]} sessions={[]} />);
    expect(TAB.exec(page)?.slice(1)).toEqual(['true', undefined]);
    expect(page).toContain('<span class="dot accent" aria-hidden="true"></span>Your computer · connected</p>');
    expect(page).toContain('<section aria-label="Needs you"><p class="muted">Nothing needs you.</p></section>');
    expect(page.match(/Nothing needs you\./gu)).toHaveLength(1);
  });

  it('opened on the tab while a computer cannot answer: it does not say that nothing needs you', () => {
    for (const kind of ['unreachable', 'pair', 'blocked'] as const) {
      const page = renderToStaticMarkup(<Sessions {...base} inbox entries={[okEntry(mac), { computer: vps, status: { kind } }]} sessions={[]} />);
      expect(page).toContain(`<section aria-label="Needs you"><p class="muted">${NO_ANSWER}</p></section>`);
      expect(page).not.toContain(NOTHING);
    }
  });
});
