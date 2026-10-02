import { useEffect, useState } from 'react';
import type { Computer } from '../computers';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { dismissPushCard, enablePush, shouldOfferPush } from '../push';
import { agentBadge, hasNewOutput, readViewed, since, type AgentBadge } from './activity';
import { firstLabel, homeOrigin } from '../api/http';
import {
  byChip, cantReach, chipsFor, keyOf, pairAgain, type ComputerEntry, type HostedSession,
} from './allSessions';
import { sessionName } from './folder';
import { NewSessionHelp } from './NewSession';
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

const ended = (session: BrokerRPCSessionRecord) => session.state === 'exited' || session.state === 'lost';

function PromptGlyph() {
  return (
    <svg width="18" height="16" viewBox="0 0 18 16" aria-hidden="true">
      <path d="M2 3l5 5-5 5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M9.5 13H16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  );
}

function CheckGlyph() {
  return (
    <svg width="18" height="14" viewBox="0 0 18 14" aria-hidden="true">
      <path d="M2 7.5l4.5 4.5L16 2" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** The agent status pill: Working, Waiting or Your turn, with how long. */
export function StatusPill({ badge, duration }: { readonly badge: AgentBadge; readonly duration: string }) {
  return (
    <span className={`status-pill ${badge.tone}`}>
      {badge.tone !== 'green' && <span className={`dot ${badge.tone}`} aria-hidden="true" />}
      {badge.label}{duration !== '' && <span className="pill-time"> · {duration}</span>}
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
 * rendered as text only. A running agent gets one status pill at the top
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
  const you = session.controllerIsYou === true && !done;
  const when = age(done ? session.endedAt ?? session.updatedAt : session.startedAt ?? session.createdAt, now);
  const badge = done ? undefined : agentBadge(session, fresh);
  const question = session.agent?.state === 'waiting' ? session.agent.question?.text : undefined;
  const duration = since(session.agent?.stateSince, now);
  const tile = done ? 'tile dashed'
    : badge !== undefined ? `tile ${badge.tone}`
      : you ? 'tile accent' : 'tile';
  const glyph = done ? <span className="stop-glyph" />
    : badge?.tone === 'amber' ? '?'
      : badge?.tone === 'accent' ? <span className="spinner" />
        : badge?.tone === 'green' ? <CheckGlyph /> : <PromptGlyph />;
  const meta = [project, shell, when, badge !== undefined && controller !== undefined ? controller.text : ''];
  return (
    <li>
      <button
        type="button"
        className={`session-row${you ? ' you' : ''}${done ? ' ended' : ''}${badge?.tone === 'amber' ? ' asking' : ''}`}
        disabled={done}
        onClick={() => onOpen(session)}
      >
        <span className={tile} aria-hidden="true">{glyph}</span>
        <span className="row-text">
          <span className="row-head">
            <span className="row-title">
              {sessionName(session)}
              {fresh && <><span className="dot accent" aria-hidden="true" /><span className="visually-hidden">, new output</span></>}
              {badge?.tone === 'amber' && <span className="visually-hidden">, waiting for you</span>}
            </span>
            {badge !== undefined ? <StatusPill badge={badge} duration={duration} /> : controller !== undefined && (
              <span className={done ? 'status-pill plain muted' : `controller ${controller.tone}`}>{controller.text}</span>
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

function BellGlyph() {
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" fill="none" aria-hidden="true">
      <path d="M4.5 12.5V8a4.5 4.5 0 0 1 9 0v4.5l1.5 1.5H3z" stroke="currentColor" strokeWidth="1.7" strokeLinejoin="round" />
      <path d="M7.3 15.8a1.8 1.8 0 0 0 3.4 0" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
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
    <section className="card push-card" aria-labelledby="push-card-title">
      <span className="tile accent" aria-hidden="true"><BellGlyph /></span>
      <div className="push-card-body">
        <h2 id="push-card-title" className="push-card-title">Get a buzz when an agent needs you</h2>
        <p className="hint-text">Only the session name and status. Never terminal text.</p>
        {note !== undefined && <p role="alert" className="error hint-text">{note}</p>}
        <div className="push-card-actions">
          <button type="button" className="primary" disabled={busy} onClick={turnOn}>Turn on</button>
          <button type="button" disabled={busy} onClick={() => { dismissPushCard(); onClose(); }}>Not now</button>
        </div>
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
function ago(at: number, now: number): string {
  const text = age(new Date(at).toISOString(), now);
  return text === 'now' || text === 'yesterday' ? `last seen ${text === 'now' ? 'just now' : text}` : `last seen ${text} ago`;
}

/** What to run so a peer accepts this phone again: "Run pterm pair --via mac on vps". */
export function pairHint(label: string, home = firstLabel(homeOrigin())): string {
  return `Run pterm pair --via ${home} on ${label}`;
}

/** The line for a computer whose sessions cannot be listed, or undefined when it answered (or is still loading). */
export function problemText(entry: ComputerEntry, now: number): string | undefined {
  const { computer, status } = entry;
  switch (status.kind) {
    case 'unreachable':
      return status.lastSeen === undefined ? cantReach(computer.label) : `${cantReach(computer.label)} · ${ago(status.lastSeen, now)}`;
    case 'pair': return `${pairAgain(computer.label)} · ${pairHint(computer.label)}`;
    case 'outdated': return status.message;
    default: return undefined;
  }
}

/** One row per computer that did not answer: "vps · can't reach" with Retry, "vps · pair again", "Update Termivoa on vps". */
export function ComputerProblem({ entry, now, onRetry, onSettings }: {
  readonly entry: ComputerEntry;
  readonly now: number;
  readonly onRetry: (origin: string) => void;
  readonly onSettings: () => void;
}) {
  const text = problemText(entry, now);
  if (text === undefined) return null;
  const { computer, status } = entry;
  return (
    <li className="computer-problem">
      <span className="dot" style={{ background: computer.color }} aria-hidden="true" />
      <span className="computer-problem-text">{text}</span>
      {status.kind === 'unreachable' && <button type="button" className="link" onClick={() => onRetry(computer.origin)}>Retry</button>}
      {status.kind === 'pair' && <button type="button" className="link" onClick={onSettings}>Computers</button>}
    </li>
  );
}

/** "All" plus one chip per computer; the pressed chip shows one computer. */
export function ComputerChips({ computers, chosen, all = true, disabled, onChoose }: {
  readonly computers: ReadonlyArray<Computer>;
  readonly chosen: string | undefined;
  /** False to pick one computer (New session): no "All" chip. */
  readonly all?: boolean;
  /** Computers that cannot be picked now. */
  readonly disabled?: (host: string) => boolean;
  readonly onChoose: (host: string | undefined) => void;
}) {
  const chips = chipsFor(computers).filter((chip) => all || chip.host !== undefined);
  if (chips.length === 0) return null;
  return (
    <div className="chips" role="group" aria-label="Computers">
      {chips.map((chip) => (
        <button
          key={chip.host ?? ''}
          type="button"
          className="chip"
          aria-pressed={chosen === chip.host}
          disabled={chip.host !== undefined && disabled?.(chip.host) === true}
          onClick={() => onChoose(chip.host)}
        >
          {chip.color !== undefined && <span className="dot" style={{ background: chip.color }} aria-hidden="true" />}
          {chip.label}
        </button>
      ))}
    </div>
  );
}

const canStart = (entry: ComputerEntry) => entry.status.kind === 'ok'
  && entry.status.settings.projects.length > 0 && entry.status.settings.shellProfiles.length > 0;

export function Sessions({
  entries, sessions, message, offerPush: pushAllowed, onOpen, onNew, onWall, onSettings, onReload, onRetry, onFailure, onUpdate,
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
  readonly onReload: () => Promise<void>;
  readonly onRetry: (origin: string) => void;
  readonly onFailure: (error: unknown) => string;
  /** Present when a new version waits; tapping it reloads into that version. */
  readonly onUpdate?: () => void;
}) {
  const [error, setError] = useState<string | undefined>(message);
  const [busy, setBusy] = useState(false);
  const [offerPush, setOfferPush] = useState(false);
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
  const reachable = entries.every((entry) => entry.status.kind === 'ok' || entry.status.kind === 'loading');
  const summary = multi
    ? hostSummary(sessions, `${computers.length} computers`)
    : reachable ? hostSummary(sessions) : 'Your computer · not reachable, retrying';

  useEffect(() => {
    if (!pushAllowed) return undefined;
    let live = true;
    void shouldOfferPush().then((offer) => { if (live) setOfferPush(offer); });
    return () => { live = false; };
  }, [pushAllowed]);

  async function refresh() {
    setBusy(true);
    setError(undefined);
    try {
      await onReload();
    } catch (failure) {
      setError(onFailure(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="page screen">
      <header className="screen-header sessions-header">
        <div>
          <h1 className="display">Sessions</h1>
          <p className="host-line" role="status">
            <span className={reachable ? 'dot green' : 'dot amber'} aria-hidden="true" />
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
      {error !== undefined && <p role="alert" className="error">{error}</p>}

      <ComputerChips computers={computers} chosen={chip} onChoose={setChip} />

      {help?.kind === 'ok' && (
        <section className="card stack" aria-label="Before your first session">
          <NewSessionHelp settings={help.settings} />
          <button type="button" disabled={busy} onClick={() => { void refresh(); }}>Check again</button>
        </section>
      )}

      {offerPush && <PushCard onClose={() => setOfferPush(false)} onFailure={onFailure} />}

      <section aria-label="Your sessions" className="list-section">
        {loading && <p role="status" className="muted">Loading…</p>}
        {!loading && answered.length > 0 && list.length === 0 && <p className="muted empty">No sessions yet.</p>}
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

      {canCreate && (
        <div className="bottom-bar">
          <button type="button" className="primary wide" onClick={onNew}>
            <span aria-hidden="true" className="prompt">❯</span> {multi ? 'new session on…' : 'new session'}
          </button>
        </div>
      )}
    </main>
  );
}
