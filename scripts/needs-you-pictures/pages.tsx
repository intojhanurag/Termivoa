/* The review pictures of the "Needs you" inbox: the app's own components, drawn once with demo data (capture.mjs). Not part of the app. */
import { renderToStaticMarkup } from 'react-dom/server';
import type { ReactElement } from 'react';
import type { BrokerRPCSessionRecord } from '../../web/src/generated/protocol';
import { INITIAL_STATUS, type ConnectionStatus } from '../../web/src/terminal/Connection';
import { hosted, type HostedSession } from '../../web/src/screens/allSessions';
import { AnswerCard } from '../../web/src/screens/AnswerCard';
import { controlBadge } from '../../web/src/screens/Dock';
import { QueueNext } from '../../web/src/screens/Inbox';
import { TabBar } from '../../web/src/screens/Live';
import { group, item, mac, okEntry, ready, target, vps } from '../../web/src/screens/providerFixtures';
import { ComputerTag, Sessions } from '../../web/src/screens/Sessions';

const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
let ids = 0;
const session = (computer: typeof mac, title: string, extra: Partial<BrokerRPCSessionRecord> = {}): HostedSession => hosted({
  id: `84444444-4444-4444-8444-${String(++ids).padStart(12, '0')}`, generation: '85555555-5555-4555-8555-555555555555', title,
  projectId: 'p1', shellProfileId: 's1', state: 'running', columns: 46, rows: 20, createdAt: ago(190), updatedAt: ago(1), startedAt: ago(190), ...extra,
}, computer);

const question = { id: 'q1', text: 'Run the test suite?', detail: 'Bash: pnpm test', options: [{ label: 'Yes' }, { label: 'Yes, and don\'t ask again' }, { label: 'No (esc)' }] as [{ label: string }, ...{ label: string }[]] };
const api = session(vps, 'api', { agent: { state: 'waiting', stateSince: ago(4), question } });
const web = session(mac, 'web', { agent: { state: 'waiting', stateSince: ago(11) } });
const docs = session(mac, 'docs', { lastOutputAt: ago(2), agent: { state: 'idle', stateSince: ago(2), afterWork: true } });
const build = session(mac, 'build', { agent: { state: 'working', stateSince: ago(7) }, preview: 'Compiling 42 modules…' });
const shop = () => [group('g1', 'shop', [
  item('w1', 'main', [target('w1:t1', 'working', { agent: 'claude' })]),
  item('w8', 'silver-meadow', [target('w8:t1', 'blocked', { agent: 'claude' })], { stateSince: ago(9) }),
])];

const settings = { projects: [{ id: 'p1', name: 'site' }], shellProfiles: [{ id: 's1', name: 'zsh' }], protocolRevision: 6 };
const entries = (macSessions: HostedSession[], vpsSessions: HostedSession[]) => [
  { ...okEntry(mac, settings), status: { ...okEntry(mac, settings).status, sessions: macSessions } },
  { ...okEntry(vps, settings), status: { ...okEntry(vps, settings).status, sessions: vpsSessions } },
] as ReturnType<typeof okEntry>[];
const noop = () => undefined;
const base = {
  offerPush: false, onOpen: noop, onNew: noop, onWall: noop, onSettings: noop, onReload: async () => undefined, onRetry: async () => undefined,
  onFailure: () => '', onOpenItem: noop, onOpenTarget: noop,
};
const waitingFirst = [api, web, docs, build];

const liveStatus: ConnectionStatus = { ...INITIAL_STATUS, link: 'live', control: 'observer', sessionState: 'running', columns: 46, rows: 20, agent: api.agent };
const badge = controlBadge(liveStatus);
/** Live with an item open from the inbox. Live itself needs a browser and a socket, so this is its parts in its order: tabs, status line, the queue's row, the terminal, the answer card. */
const live = (
  <main className="live">
    <TabBar tabs={[api, web, build]} current={api} viewed={{}} onOpen={noop} onNew={noop} onMenu={noop} />
    <div className="status-line" role="status">
      <span className="place">
        <span className="dot accent" aria-hidden="true" />
        <span className="place-computer"><ComputerTag computer={vps} /> · </span>
        <span className="place-text"><span className="place-end"><span className="place-name"><span dir="ltr">~/code/api</span></span><span className="place-tail"> · zsh · 46×20</span></span></span>
      </span>
      <span className={`badge-text ${badge.tone}`}>{badge.text}</span>
    </div>
    <QueueNext queue={{ left: 2, onNext: noop }} />
    <div className="terminal-area dim">
      <pre style={{ margin: 0, padding: '10px 12px', font: '12px/17px ui-monospace, Menlo, monospace', color: 'var(--soft)' }}>{'❯ claude\n\n● I changed the retry logic in client.ts.\n  Next I want to run the tests.\n\n  Bash(pnpm test)\n\n  Run the test suite?\n  ❯ 1. Yes\n    2. Yes, and don\'t ask again\n    3. No (esc)'}</pre>
    </div>
    <div className="answer-anchor">
      <AnswerCard title="api" question={question} stateSince={ago(4)} status={liveStatus} canTake onAnswer={noop} onTakeControl={noop} onHide={noop} />
    </div>
  </main>
);

const pages: Record<string, ReactElement> = {
  inbox: <Sessions {...base} inbox entries={entries([web, docs, build], [api])} sessions={waitingFirst} providers={[ready(mac, shop())]} />,
  'sessions-entry': <Sessions {...base} entries={entries([web, docs, build], [api])} sessions={waitingFirst} providers={[ready(mac, shop())]} />,
  'live-next': live,
  'inbox-empty': <Sessions {...base} inbox entries={entries([build], [])} sessions={[build]} providers={[ready(mac, [group('g1', 'shop', [item('w1', 'main', [target('w1:t1', 'working', { agent: 'claude' })])])])]} />,
};

export const html = (): Record<string, string> => Object.fromEntries(Object.entries(pages).map(([name, page]) => [name, renderToStaticMarkup(<div className="app">{page}</div>)]));
