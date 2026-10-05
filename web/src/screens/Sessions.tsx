import { useEffect, useRef, useState } from 'react';
import type { Computer } from '../computers';
import type { BrokerRPCSessionRecord, HTTPProviderGroup, HTTPProviderItem, HTTPSettingsResponse } from '../generated/protocol';
import { dismissHomeScreenHint, dismissPushCard, enablePush, shouldOfferHomeScreen, shouldOfferPush } from '../push';
import { agentBadge, hasNewOutput, readViewed, since, type AgentBadge } from './activity';
import { firstLabel, homeOrigin } from '../api/http';
import {
  atLeast, byChip, cantReach, checkAgain, CHECKING_MIN_MS, chipsFor, connectingText, JUST_ADDED, keyOf, OUT_OF_DATE, pairAgain, type ComputerEntry, type ComputerStatus, type HostedSession,
} from './allSessions';
import { sessionName } from './folder';
import { InboxList, inboxItems, inboxTabShown, needsYouText, waitingCount, type InboxItem } from './Inbox';
import { firstBlocked, ProviderSection, ProviderTree, sectionParts, type ProviderTap } from './Herdr';
import { ProviderCreateSheet } from './ProviderCreate';
import { ItemMenuSheet, WorkspaceSheet } from './ProviderManage';
import { NewSessionHelp } from './NewSession';
import { ADD_FOLDER_COMMAND, providerTabs, startInstall, wordSummary, type ProviderEntry } from './providers';
import { Button, Notice, PairVia } from './ui';
import { WorktreeBadge } from './Worktree';

type Tone = 'accent' | 'amber' | 'muted';

/** Who controls the session, as the list shows it; undefined when unknown. */
export function controllerText(session: BrokerRPCSessionRecord): { readonly text: string; readonly tone: Tone } | undefined {
  switch (session.state) {
    case 'exited':
      return { text: session.exitCode === undefined ? 'Exited' : `Exited · ${session.exitCode}`, tone: 'muted' };
    case 'lost': return { text: 'Lost', tone: 'muted' };
    case 'exiting': return { text: 'Ending', tone: 'muted' };
    case 'starting': return { text: 'Starting', tone: 'muted' };
    default:
  }
  if (session.controllerIsYou === true) return { text: 'You', tone: 'accent' };
  if (session.controlled === true) return { text: 'Other device', tone: 'amber' };
  if (session.controlled === false) return { text: 'No one', tone: 'muted' };
  return undefined;
}

/** Short age such as "35 min", "2 h", "yesterday" or "3 d". */
export function age(iso: string | undefined, now: number): string {
  const then = Date.parse(iso ?? '');
  if (Number.isNaN(then)) return '';
  const minutes = Math.max(0, Math.floor((now - then) / 60_000));
  if (minutes < 1) return 'now';
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h`;
  return hours < 48 ? 'yesterday' : `${Math.floor(hours / 24)} d`;
}

export const ended = (session: BrokerRPCSessionRecord) => session.state === 'exited' || session.state === 'lost';

/** The agent status pill: Working, Waiting or Your turn, with how long. */
export function StatusPill({ badge, duration }: { readonly badge: AgentBadge; readonly duration: string }) {
  return (
    <span className={`status-pill ${badge.tone}`}>
      {badge.tone !== 'green' && <span className={`dot ${badge.tone}`} aria-hidden="true" />}
      {badge.label}{duration !== '' && ` · ${duration}`}
    </span>
  );
}

/** The colour dot and label of a computer; the colour is never the only cue. */
export function ComputerTag({ computer }: { readonly computer: Pick<Computer, 'label' | 'color'> }) {
  return (
    <span className="computer-tag">
      <span className="dot" style={{ background: computer.color }} aria-hidden="true" />
      {computer.label}
    </span>
  );
}

/**
 * One session. Title, preview and question are terminal-controlled text:
 * rendered as text only. A running agent gets one status word at the top
 * right; the controller then moves into the meta line.
 */
export function SessionRow<T extends BrokerRPCSessionRecord>({ session, computer, project, shell, fresh, now, onOpen }: {
  readonly session: T;
  /** Shown in the meta line when the phone uses more than one computer. */
  readonly computer?: Pick<Computer, 'label' | 'color'> | undefined;
  readonly project: string;
  readonly shell: string;
  readonly fresh: boolean;
  readonly now: number;
  readonly onOpen: (session: T) => void;
}) {
  const done = ended(session);
  const controller = controllerText(session);
  const when = age(done ? session.endedAt ?? session.updatedAt : session.startedAt ?? session.createdAt, now);
  const badge = done ? undefined : agentBadge(session, fresh);
  const question = session.agent?.state === 'waiting' ? session.agent.question?.text : undefined;
  const duration = since(session.agent?.stateSince, now);
  const meta = [project, shell, when, badge !== undefined && controller !== undefined ? controller.text : ''];
  return (
    <li>
      <button
        type="button"
        className={`session-row${done ? ' ended' : ''}`}
        disabled={done}
        onClick={() => onOpen(session)}
      >
        <span className="row-text">
          <span className="row-head">
            <span className="row-title">
              {sessionName(session)}
              {fresh && <><span className="dot accent" aria-hidden="true" /><span className="visually-hidden">, new output</span></>}
              {badge?.tone === 'amber' && <span className="visually-hidden">, waiting for you</span>}
            </span>
            {badge !== undefined ? <StatusPill badge={badge} duration={duration} /> : controller !== undefined && (
              <span className={`controller ${controller.tone}`}>{controller.text}</span>
            )}
          </span>
          <span className="row-meta">
            {computer !== undefined && <><ComputerTag computer={computer} /> · </>}
            {session.worktree !== undefined && <><WorktreeBadge worktree={session.worktree} /> · </>}
            {meta.filter((part) => part !== '').join(' · ')}
          </span>
          {question !== undefined ? (
            <>
              <span className="question">{question}</span>
              <span className="answer-link">Answer ›</span>
            </>
          ) : session.preview !== undefined && session.preview !== '' && !done && (
            <span className="preview">{session.preview}</span>
          )}
        </span>
      </button>
    </li>
  );
}

/** Four squares: the Wall. */
export function WallGlyph() {
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" fill="none" aria-hidden="true">
      <rect x="2" y="2" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
      <rect x="10.5" y="2" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
      <rect x="2" y="10.5" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
      <rect x="10.5" y="10.5" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
    </svg>
  );
}

/** One-time offer to turn on push. Hidden for good after "Not now" or once push is on. */
export function PushCard({ onClose, onFailure }: {
  readonly onClose: () => void;
  readonly onFailure: (error: unknown) => string;
}) {
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string>();

  function turnOn() {
    setBusy(true);
    setNote(undefined);
    // enablePush asks for permission before its first await, inside this tap (iOS needs that).
    enablePush().then((result) => {
      if (result.ok) {
        onClose();
        return;
      }
      setNote(result.message);
      setBusy(false);
    }, (failure: unknown) => {
      setNote(onFailure(failure));
      setBusy(false);
    });
  }

  return (
    <section className="push-card" aria-labelledby="push-card-title">
      <h2 id="push-card-title" className="push-card-title">Get a buzz when an agent needs you</h2>
      <p className="hint-text">Only the session name and status. Never terminal text.</p>
      {note !== undefined && <Notice tone="danger" role="alert" title={note} />}
      <div className="push-card-actions">
        <Button className="secondary" busy={busy} onClick={turnOn}>Turn on</Button>
        <button type="button" className="link" disabled={busy} onClick={() => { dismissPushCard(); onClose(); }}>Not now</button>
      </div>
    </section>
  );
}

/** iPhone in a Safari tab: one line and the way to the steps (Settings → Notifications). Hidden for good after "Not now". */
export function HomeScreenHint({ onSteps, onClose }: { readonly onSteps: () => void; readonly onClose: () => void }) {
  return (
    <section className="push-card" aria-labelledby="home-screen-title">
      <h2 id="home-screen-title" className="push-card-title">For notifications, add Termivoa to your Home Screen</h2>
      <div className="push-card-actions">
        <button type="button" className="secondary" onClick={onSteps}>See how</button>
        <button type="button" className="link" onClick={() => { dismissHomeScreenHint(); onClose(); }}>Not now</button>
      </div>
    </section>
  );
}

/** "Your computer · 1 waiting · 1 working", or "· connected" when no agent is busy. */
export function hostSummary(sessions: ReadonlyArray<BrokerRPCSessionRecord> | undefined, name = 'Your computer'): string {
  const count = (state: string) => (sessions ?? []).filter((session) => session.agent?.state === state).length;
  const parts = [[count('waiting'), 'waiting'], [count('working'), 'working']]
    .filter(([n]) => n !== 0)
    .map(([n, label]) => `${n} ${label}`);
  return [name, ...(parts.length === 0 ? ['connected'] : parts)].join(' · ');
}

/** Short time since, for "last seen 3 h ago". */
export function ago(at: number, now: number): string {
  const text = age(new Date(at).toISOString(), now);
  return text === 'now' || text === 'yesterday' ? `last seen ${text === 'now' ? 'just now' : text}` : `last seen ${text} ago`;
}

/** The command that pairs a computer with this phone again, through home: "pterm pair --via mac". */
export const pairCommand = (home = firstLabel(homeOrigin())): string => `pterm pair --via ${home}`;

/** What to run so a peer accepts this phone again: "Run pterm pair --via mac on vps". */
export function pairHint(label: string, home = firstLabel(homeOrigin())): string {
  return `Run ${pairCommand(home)} on ${label}`;
}

/** The line for a computer whose sessions cannot be listed, or undefined when it answered (or is still loading). problemDetail has the line under it. */
export function problemText(entry: ComputerEntry, now: number): string | undefined {
  const { computer, status } = entry;
  switch (status.kind) {
    case 'unreachable': case 'blocked': return cantReach(computer.label);
    case 'connecting': return connectingText(computer.label);
    case 'pair': return `${pairAgain(computer.label)} · ${pairHint(computer.label)}`;
    case 'outdated': return status.message;
    default: return undefined;
  }
}

/** Asked under "vps · can't reach" after a network error. The app cannot see why nothing answered, so it names no cause. */
export const REACH_QUESTION = 'Is it on, and in your tailnet?';

/** The line under problemText: "Last seen 3 h ago. Is it on, and in your tailnet?", "Just added. Its sessions show in a moment.", OUT_OF_DATE, or undefined. */
export function problemDetail(entry: ComputerEntry, now: number): string | undefined {
  const { status } = entry;
  if (status.kind === 'connecting') return JUST_ADDED;
  if (status.kind === 'blocked') return OUT_OF_DATE;
  if (status.kind !== 'unreachable') return undefined;
  const seen = status.lastSeen === undefined ? undefined : `${ago(status.lastSeen, now).replace(/^l/u, 'L')}.`;
  const parts = [seen, status.network === true ? REACH_QUESTION : undefined].filter((part) => part !== undefined);
  return parts.length === 0 ? undefined : parts.join(' ');
}

/**
 * One notice per computer that did not answer: "vps · can't reach" (amber,
 * with problemDetail and Retry), "Connecting to vps…" (cyan), "vps · pair again" (red, with the
 * command to run there) or "Update Termivoa on vps" (grey).
 */
export function ComputerProblem({ entry, now, onRetry, onSettings }: {
  readonly entry: ComputerEntry;
  readonly now: number;
  readonly onRetry: (origin: string) => Promise<void>;
  readonly onSettings: () => void;
}) {
  const [retrying, setRetrying] = useState(false);
  if (problemText(entry, now) === undefined) return null;
  const detail = problemDetail(entry, now);
  const { computer, status } = entry;
  // As Check again: stay busy long enough to see, even when the answer is instant.
  const retry = () => {
    setRetrying(true);
    void atLeast(onRetry(computer.origin).catch(() => undefined), CHECKING_MIN_MS).then(() => setRetrying(false));
  };
  return (
    <li className="computer-problem">
      {(status.kind === 'unreachable' || status.kind === 'blocked') && (
        <Notice
          tone="warning"
          title={cantReach(computer.label)}
          {...(status.kind === 'unreachable' ? { action: <Button className="link" busy={retrying} busyLabel="Retrying…" onClick={retry}>Retry</Button> } : {})}
        >{detail}</Notice>
      )}
      {status.kind === 'connecting' && <Notice tone="working" title={connectingText(computer.label)}>{detail}</Notice>}
      {status.kind === 'pair' && (
        <Notice
          tone="danger"
          title={pairAgain(computer.label)}
          action={<button type="button" className="link" onClick={onSettings}>Computers</button>}
        >
          This phone lost access on {computer.label}. On {computer.label}, run:
          <br /><PairVia className="notice-command" home={firstLabel(homeOrigin())} />
        </Notice>
      )}
      {status.kind === 'outdated' && (
        <Notice tone="neutral" title={status.message}>
          It runs an older version than this app. Your other computers still work.
        </Notice>
      )}
    </li>
  );
}

/** "All", "Needs you 3" while something waits, the provider tabs ("herdr 2"), then one tab per computer; the pressed one shows one of them. */
export function ComputerChips({ computers, chosen, all = true, disabled, onChoose, providers = [], provider, onProvider, needs, needsOpen = false, onNeeds }: {
  readonly computers: ReadonlyArray<Computer>;
  readonly chosen: string | undefined;
  /** False to pick one computer (New session): no "All" chip. */
  readonly all?: boolean;
  /** Computers that cannot be picked now. */
  readonly disabled?: (host: string) => boolean;
  readonly onChoose: (host: string | undefined) => void;
  /** Provider tabs with their count of blocked and done terminals. */
  readonly providers?: ReadonlyArray<{ readonly id: string; readonly name: string; readonly count: number }>;
  /** The provider tab that is pressed, if any. */
  readonly provider?: string | undefined;
  readonly onProvider?: (id: string) => void;
  /** The "Needs you" tab with its count of what waits; undefined: no such tab. */
  readonly needs?: number | undefined;
  readonly needsOpen?: boolean;
  readonly onNeeds?: () => void;
}) {
  const chips = (computers.length < 2 && (providers.length > 0 || needs !== undefined) && all ? [{ host: undefined, label: 'All' }] : chipsFor(computers))
    .filter((chip) => all || chip.host !== undefined);
  if (chips.length === 0) return null;
  const [first, ...rest] = chips;
  const chip = (item: (typeof chips)[number]) => (
    <button
      key={item.host ?? ''}
      type="button"
      className="chip"
      aria-pressed={provider === undefined && !needsOpen && chosen === item.host}
      disabled={item.host !== undefined && disabled?.(item.host) === true}
      onClick={() => onChoose(item.host)}
    >
      {'color' in item && item.color !== undefined && <span className="dot" style={{ background: item.color }} aria-hidden="true" />}
      {item.label}
    </button>
  );
  return (
    <div className="chips" role="group" aria-label={providers.length > 0 || needs !== undefined ? 'Show' : 'Computers'}>
      {first !== undefined && chip(first)}
      {first?.host === undefined && needs !== undefined && (
        <button type="button" className="chip" aria-pressed={needsOpen} onClick={onNeeds}>
          Needs you
          {needs > 0 && <span className="chip-count">{needs}</span>}
        </button>
      )}
      {first?.host === undefined && providers.map((tab) => (
        <button key={`provider:${tab.id}`} type="button" className="chip" aria-pressed={provider === tab.id} onClick={() => onProvider?.(tab.id)}>
          {tab.name}
          {tab.count > 0 && <><span className="chip-count" aria-hidden="true">{tab.count}</span><span className="visually-hidden">, {tab.count} need you</span></>}
        </button>
      ))}
      {rest.map(chip)}
    </div>
  );
}

const canStart = (entry: ComputerEntry) => entry.status.kind === 'ok'
  && entry.status.settings.projects.length > 0 && entry.status.settings.shellProfiles.length > 0;

/** The settings an open sheet shows: the computer's newest, or the last good ones while it does not answer (the sheet stays, with its own messages). */
export const keptSettings = (kept: HTTPSettingsResponse | undefined, status: ComputerStatus | undefined): HTTPSettingsResponse | undefined =>
  (status?.kind === 'ok' ? status.settings : kept);

export function Sessions({
  entries, sessions, message, offerPush: pushAllowed, onOpen, onNew, onWall, onSettings, onHomeScreenSteps, onReload, onRetry, onFailure, onUpdate,
  providers = [], tab, inbox: startInbox = false, onOpenItem, onOpenTarget, onProviderRetry, onCreated, onTypeIn,
}: {
  /** Every computer, home first, with what it answered. */
  readonly entries: ReadonlyArray<ComputerEntry>;
  /** All answered computers' sessions, waiting first. */
  readonly sessions: ReadonlyArray<HostedSession>;
  readonly message?: string;
  /** Push comes from home only, so the card waits until home answers. */
  readonly offerPush: boolean;
  readonly onOpen: (session: HostedSession) => void;
  readonly onNew: () => void;
  readonly onWall: () => void;
  readonly onSettings: () => void;
  /** Opens Settings at Notifications, where the Add to Home Screen steps are. Absent: no hint. */
  readonly onHomeScreenSteps?: (() => void) | undefined;
  readonly onReload: () => Promise<void>;
  readonly onRetry: (origin: string) => Promise<void>;
  /** `from`: the computer the failure came from; without it, it counts as home's. */
  readonly onFailure: (error: unknown, from?: Computer) => string;
  /** Present when a new version waits; tapping it reloads into that version. */
  readonly onUpdate?: () => void;
  /** Providers (herdr) of every computer; their trouble shows only in their own part. */
  readonly providers?: ReadonlyArray<ProviderEntry>;
  /** The provider tab to open on (after "That terminal is gone."). */
  readonly tab?: string | undefined;
  /** Open on the "Needs you" tab (the queue's last Next comes back here). */
  readonly inbox?: boolean | undefined;
  /** A tap on a "Needs you" row: open it and start the queue. Absent: no "Needs you" tab. */
  readonly onOpenItem?: ((item: InboxItem) => void) | undefined;
  readonly onOpenTarget?: (tap: ProviderTap) => void;
  readonly onProviderRetry?: (key: string) => Promise<void>;
  /** A worktree was made with the + of a project row (milestone M2), or a tab, a workspace or a closed worktree was opened: open its terminal; `notice` says what did not start. */
  readonly onCreated?: ((tap: ProviderTap, notice: string | undefined, key: string) => void) | undefined;
  /** Add a folder: a new session opens in Live with this command typed, not run. */
  readonly onTypeIn?: ((session: HostedSession, command: string) => void) | undefined;
}) {
  const [creating, setCreating] = useState<{ readonly entry: ProviderEntry; readonly group: HTTPProviderGroup }>();
  // The ⋯ of an item, and New workspace (docs/herdr-manage.md): one sheet at a time.
  const [menu, setMenu] = useState<{ readonly entry: ProviderEntry; readonly group: HTTPProviderGroup; readonly item: HTTPProviderItem }>();
  const [workspace, setWorkspace] = useState<ProviderEntry>();
  const [error, setError] = useState<string | undefined>(message);
  const tabs = providerTabs(providers);
  const [chosenProvider, setProvider] = useState<string | undefined>(tab);
  const [inboxChosen, setInbox] = useState(startInbox);
  const inbox = inboxChosen && onOpenItem !== undefined;
  const provider = inbox ? undefined : tabs.some((item) => item.id === chosenProvider) ? chosenProvider : undefined;
  const [busy, setBusy] = useState(false);
  const [offerPush, setOfferPush] = useState(false);
  const [hintClosed, setHintClosed] = useState(false);
  const [chip, setChip] = useState<string>();
  const computers = entries.map((entry) => entry.computer);
  const multi = computers.length > 1;
  const shown = byChip(entries, chip);
  const answered = shown.filter((entry) => entry.status.kind === 'ok');
  const loading = answered.length === 0 && shown.some((entry) => entry.status.kind === 'loading');
  const list = byChip(sessions, chip);
  const canCreate = entries.some(canStart);
  const help = canCreate ? undefined : entries.find((entry) => entry.status.kind === 'ok')?.status;
  const names = new Map(entries.flatMap((entry) => (entry.status.kind === 'ok' ? [[entry.computer.origin, {
    project: new Map(entry.status.settings.projects.map((item) => [item.id, item.name])),
    shell: new Map(entry.status.settings.shellProfiles.map((item) => [item.id, item.name])),
  }] as const] : [])));
  const viewed = readViewed();
  const now = Date.now();
  // Every computer's, whatever tab or computer is chosen: the count is the same everywhere.
  const needsYou = inboxItems(sessions, providers, viewed);
  const needs = waitingCount(needsYou);
  const reachable = entries.every((entry) => entry.status.kind === 'ok' || entry.status.kind === 'loading' || entry.status.kind === 'connecting');
  const shownProviders = provider === undefined ? byChip(providers, chip) : providers.filter((entry) => entry.info.id === provider);
  // The provider tab says its own counts; the sessions line never depends on a provider.
  const summary = inbox && needs > 0 ? needsYouText(needs) : provider !== undefined
    ? wordSummary(shownProviders) ?? `${tabs.find((item) => item.id === provider)?.name ?? provider} · nothing running`
    : multi
      ? hostSummary(sessions, `${computers.length} computers`)
      : reachable ? hostSummary(sessions) : 'Your computer · not reachable, retrying';
  const blocked = provider === undefined ? undefined : firstBlocked(shownProviders);
  const openTarget = onOpenTarget ?? (() => undefined);
  const retryProvider = onProviderRetry ?? (async () => undefined);
  const reloadProvider = (key: string) => retryProvider(key).catch(() => undefined);
  // The projects New workspace lists: that computer's own, read live, so Check again shows a new one. A read that
  // fails while the sheet is open keeps the last good ones: the sheet stays, shows its own error and uses its answer.
  const statusOf = (entry: ProviderEntry) => entries.find((one) => one.computer.origin === entry.computer.origin)?.status;
  const workspaceKept = useRef<HTTPSettingsResponse | undefined>(undefined);
  workspaceKept.current = workspace === undefined ? undefined : keptSettings(workspaceKept.current, statusOf(workspace));
  const workspaceSettings = workspaceKept.current;
  // Opens with the settings the tap saw, so the sheet never waits for a later read to show.
  const openWorkspace = (entry: ProviderEntry) => {
    const settings = keptSettings(undefined, statusOf(entry));
    if (settings === undefined) return;
    workspaceKept.current = settings;
    setWorkspace(entry);
  };

  useEffect(() => {
    if (!pushAllowed) return undefined;
    let live = true;
    void shouldOfferPush().then((offer) => { if (live) setOfferPush(offer); });
    return () => { live = false; };
  }, [pushAllowed]);

  async function refresh() {
    setBusy(true);
    setError(undefined);
    setError(await checkAgain(onReload(), onFailure));
    setBusy(false);
  }

  return (
    <main className="page screen">
      <header className="screen-header sessions-header">
        <div>
          <h1 className="display">Sessions</h1>
          <p className="host-line" role="status">
            <span className={inbox && needs > 0 ? 'dot amber' : provider !== undefined ? (blocked === undefined ? 'dot accent' : 'dot amber') : reachable ? 'dot accent' : 'dot amber'} aria-hidden="true" />
            {summary}
          </p>
        </div>
        <div className="header-actions">
          {onUpdate !== undefined && (
            <button type="button" className="update-pill" aria-label="Update to the new version" onClick={onUpdate}>
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
                <path d="M12 7a5 5 0 1 1-1.5-3.6M12 1.5v2.9H9.1" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
              update
            </button>
          )}
          <button type="button" className="round" aria-label="Wall" onClick={onWall}>
            <WallGlyph />
          </button>
          <button type="button" className="round" aria-label="Settings" onClick={onSettings}>
            <svg width="20" height="18" viewBox="0 0 20 18" aria-hidden="true">
              <path d="M2 4h9M15 4h3M2 14h3M9 14h9" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
              <circle cx="13" cy="4" r="2.2" fill="none" stroke="currentColor" strokeWidth="1.7" />
              <circle cx="7" cy="14" r="2.2" fill="none" stroke="currentColor" strokeWidth="1.7" />
            </svg>
          </button>
        </div>
      </header>
      {error !== undefined && <Notice tone="danger" role="alert" title={error} />}

      <ComputerChips
        computers={computers}
        chosen={chip}
        onChoose={(host) => { setInbox(false); setProvider(undefined); setChip(host); }}
        providers={tabs}
        provider={provider}
        onProvider={(id) => { setInbox(false); setChip(undefined); setProvider(id); }}
        needs={onOpenItem !== undefined && inboxTabShown(needs, inbox) ? needs : undefined}
        needsOpen={inbox}
        onNeeds={() => { setChip(undefined); setInbox(true); }}
      />

      {inbox && onOpenItem !== undefined && <InboxList items={needsYou} multi={multi} now={now} partial={!reachable} onOpen={onOpenItem} />}

      {provider !== undefined && (
        <ProviderTree entries={shownProviders} multi={multi} now={now} onOpen={openTarget} onCreate={onCreated === undefined ? undefined : (entry, group) => setCreating({ entry, group })} onRetry={retryProvider}
          manage={onCreated === undefined ? undefined : { onMenu: (entry, group, item) => setMenu({ entry, group, item }), onWorkspace: openWorkspace, onOpened: (entry, tap) => onCreated(tap, undefined, entry.key), onFailure }}
        />
      )}

      {!inbox && provider === undefined && help?.kind === 'ok' && (
        <section className="card stack" aria-label="Before your first session">
          <NewSessionHelp settings={help.settings} />
          <Button className="secondary" busy={busy} busyLabel="Checking…" onClick={() => { void refresh(); }}>Check again</Button>
        </section>
      )}

      {offerPush && <PushCard onClose={() => setOfferPush(false)} onFailure={onFailure} />}
      {pushAllowed && onHomeScreenSteps !== undefined && !hintClosed && shouldOfferHomeScreen() && (
        <HomeScreenHint onSteps={onHomeScreenSteps} onClose={() => setHintClosed(true)} />
      )}

      {!inbox && provider === undefined && <ProviderSection entries={shownProviders} multi={multi} now={now} onOpen={openTarget} />}

      {!inbox && provider === undefined && (
      <section aria-label="Your sessions">
        {sectionParts(shownProviders).length > 0 && list.length > 0 && <h2 className="caps list-label">Termivoa sessions</h2>}
        {loading && <p role="status" className="muted">Loading…</p>}
        {!loading && answered.length > 0 && list.length === 0 && <p className="muted">No sessions yet.</p>}
        <ul className="rows">
          {list.map((session) => {
            const name = names.get(session.computer.origin);
            return (
              <SessionRow
                key={`${keyOf(session)}/${session.generation}`}
                session={session}
                computer={multi ? session.computer : undefined}
                project={name?.project.get(session.projectId) ?? 'Unknown project'}
                shell={name?.shell.get(session.shellProfileId) ?? 'Unknown shell'}
                fresh={!ended(session) && hasNewOutput(session, viewed)}
                now={now}
                onOpen={onOpen}
              />
            );
          })}
          {multi && shown.map((entry) => (
            <ComputerProblem key={entry.computer.origin} entry={entry} now={now} onRetry={onRetry} onSettings={onSettings} />
          ))}
        </ul>
      </section>
      )}

      {provider !== undefined && blocked !== undefined && (
        <div className="bottom-bar">
          <button type="button" className="primary wide" onClick={() => openTarget(blocked)}>Open the blocked agent</button>
        </div>
      )}

      {creating !== undefined && onCreated !== undefined && (
        <ProviderCreateSheet
          entry={creating.entry}
          group={creating.group}
          onCreated={(tap, notice) => onCreated(tap, notice, creating.entry.key)}
          onFailure={onFailure}
          onReload={() => { void retryProvider(creating.entry.key).catch(() => undefined); }}
          onClose={() => setCreating(undefined)}
        />
      )}

      {menu !== undefined && onCreated !== undefined && (
        <ItemMenuSheet
          entry={menu.entry}
          group={menu.group}
          item={menu.item}
          onOpened={(tap, notice) => onCreated(tap, notice, menu.entry.key)}
          onReload={() => reloadProvider(menu.entry.key)}
          onFailure={onFailure}
          onClose={() => setMenu(undefined)}
        />
      )}

      {workspace !== undefined && workspaceSettings !== undefined && onCreated !== undefined && (
        <WorkspaceSheet
          entry={workspace}
          settings={workspaceSettings}
          onOpened={(tap) => onCreated(tap, undefined, workspace.key)}
          onReload={() => reloadProvider(workspace.key)}
          onCheck={() => onRetry(workspace.computer.origin)}
          onAddFolder={onTypeIn === undefined ? undefined : async () => {
            const session = await startInstall(workspace.computer, workspaceSettings);
            if (session !== undefined) onTypeIn(session, ADD_FOLDER_COMMAND);
            return session !== undefined;
          }}
          onFailure={onFailure}
          onClose={() => setWorkspace(undefined)}
        />
      )}

      {provider === undefined && canCreate && (
        <div className="bottom-bar">
          <button type="button" className="primary wide" onClick={onNew}>
            <span aria-hidden="true" className="prompt">❯</span> {multi ? 'new session on…' : 'new session'}
          </button>
        </div>
      )}
    </main>
  );
}
