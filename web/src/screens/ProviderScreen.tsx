import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, client, providerWord } from '../api/http';
import type { Computer } from '../computers';
import type { HTTPProviderItem, HTTPProviderSession } from '../generated/protocol';
import { fitGrid, MONOSPACE } from '../terminal/TerminalView';
import { hosted, type HostedSession } from './allSessions';
import { GONE, Names, StateWord, wordText } from './Herdr';
import { Live } from './Live';
import { findTarget, groupsOf, type ProviderEntry } from './providers';
import { Button, Notice } from './ui';

/*
 * One provider terminal in Live (docs/herdr-provider.md, section 6, rules 8–10).
 * It opens at once only after a tap (a row or chip in the app, or a
 * notification tap the service worker confirmed); any other arrival shows the
 * terminal's names and Open. It never takes control. While the page is
 * hidden the stream is closed, so the computer gets its pane size back; it
 * opens again on return, but not after the attach ended (Live shows Open again).
 */

export const sizeLine = (label: string): string => `${label} shows this terminal at phone size while it is open here.`;

/** Height the Live screen keeps for the chips, the status line, the size line and the read-only dock, in CSS pixels. */
const CHROME_PX = 340;

/** Width of one monospace cell at this text size; 0.6 em where the page cannot measure. */
function cellWidth(fontSize: number): number {
  const context = globalThis.document?.createElement('canvas').getContext('2d');
  if (context === null || context === undefined) return fontSize * 0.6;
  context.font = `${fontSize}px ${MONOSPACE}`;
  return context.measureText('W').width || fontSize * 0.6;
}

/**
 * The grid a new attach starts at: the phone's screen at its text size. Only
 * the start size; once someone takes control, Live fits the real grid.
 * ponytail: estimated from the window, not measured from xterm (it is not open yet).
 */
export function phoneGrid(width: number, height: number, fontSize: number, measure: (size: number) => number = cellWidth): { columns: number; rows: number } {
  return fitGrid(width, height - CHROME_PX, measure(fontSize), fontSize * 1.2);
}

/** "herdr is off on mac…" or "herdr is not running on mac.", from the provider's latest state; unknown says both. */
export function unavailableText(name: string, label: string, state: string | undefined): string {
  switch (state) {
    case 'off': return `${name} is off on ${label}. Turn it on in Settings → Integrations.`;
    case 'notRunning': return `${name} is not running on ${label}.`;
    default: return `${name} is off or not running on ${label}.`;
  }
}

/**
 * What a failed open says, for the cases the app words itself; `gone` sends the user back to the provider tab.
 * `limit_exceeded` is the cap of open terminals or this phone's open rate; the answer does not say which, so the text is true for both.
 */
export function openProblem(error: unknown, name: string, label: string, state?: string): { readonly gone: boolean; readonly text: string } | undefined {
  switch (error instanceof ApiError ? error.code : undefined) {
    case 'session_missing': return { gone: true, text: GONE };
    case 'limit_exceeded': return { gone: false, text: `${label} cannot open more ${name} terminals just now. Close one you do not use, or wait a minute.` };
    case 'host_unavailable': return { gone: false, text: unavailableText(name, label, state) };
    default: return undefined;
  }
}

/** The session Live shows: a provider session has no project or shell profile. */
export function providerHosted(session: HTTPProviderSession, computer: Computer): HostedSession {
  return hosted({
    id: session.id, generation: session.generation, title: session.title, state: session.state, columns: session.columns, rows: session.rows,
    projectId: '', shellProfileId: '', createdAt: '', updatedAt: '',
  }, computer);
}

/**
 * A read that is ready, complete, newer than this screen, and lacks the target: it left.
 * A cut (truncated) list may just not show it; a read from before the screen (the list before a create) does not know it yet.
 */
export function targetLeft(entry: ProviderEntry | undefined, found: boolean, since: number): boolean {
  return entry?.status.kind === 'ok' && entry.status.snapshot.state === 'ready' && entry.status.snapshot.truncated !== true
    && entry.status.at >= since && !found;
}

/** The phase after an open answered: paused when the page was hidden meanwhile, so the 10 s end rule runs. */
export function openedPhase(session: HTTPProviderSession, hidden: boolean): Phase {
  return hidden ? { kind: 'paused' } : { kind: 'open', session };
}

/** What a visibility change does. Hidden: close an open stream. Back: open again. Nothing once the attach ended. */
export function visibilityStep(phase: Phase['kind'], ended: boolean, hidden: boolean): 'pause' | 'open' | undefined {
  if (ended) return undefined;
  if (hidden) return phase === 'open' ? 'pause' : undefined;
  return phase === 'paused' ? 'open' : undefined;
}

/** A tap (in the app or a notification) opens, unless this terminal already shows here. */
export const tapOpens = (phase: Phase['kind'], ended: boolean): boolean => phase !== 'open' || ended;

const DOTS: Partial<Record<string, string>> = { blocked: 'amber', working: 'accent', done: 'green' };

/** The workspace's tabs as chips (H2), and the way back. A tab this provider version cannot open is off. */
export function ProviderChips({ item, current, onChip, onSessions }: {
  readonly item: HTTPProviderItem | undefined;
  readonly current: string;
  readonly onChip: (targetId: string) => void;
  readonly onSessions: () => void;
}) {
  const targets = item?.targets ?? [];
  const repeated = (label: string) => targets.filter((target) => target.label === label).length > 1;
  return (
    <nav className="tabs provider-chips" aria-label="Tabs of this workspace">
      <button type="button" className="link provider-back" onClick={onSessions}>‹ Sessions</button>
      <div className="tab-scroll">
        {targets.map((target) => {
          const active = target.id === current;
          const dot = active ? undefined : DOTS[providerWord(target.state)];
          return (
            <button
              key={target.id}
              type="button"
              className={active ? 'tab active' : 'tab'}
              disabled={!active && !target.canOpen}
              {...(active ? { 'aria-current': 'page' as const } : {})}
              {...(target.canOpen ? {} : { title: 'Update herdr to open this tab' })}
              onClick={() => { if (!active && target.canOpen) onChip(target.id); }}
            >
              <span className="tab-face">
                <span className="tab-label"><bdi>{target.label}</bdi>{repeated(target.label) && target.agent !== undefined && <> · <bdi>{target.agent}</bdi></>}</span>
                {dot !== undefined && <><span className={`dot ${dot}`} aria-hidden="true" /><span className="visually-hidden">, {providerWord(target.state)}</span></>}
                {!target.canOpen && <span className="visually-hidden">, update herdr to open this tab</span>}
              </span>
            </button>
          );
        })}
      </div>
    </nav>
  );
}

export type Phase =
  | { readonly kind: 'ask' }
  | { readonly kind: 'opening' }
  | { readonly kind: 'open'; readonly session: HTTPProviderSession }
  /** The page is hidden: the stream is closed; back in front it opens again. */
  | { readonly kind: 'paused' }
  | { readonly kind: 'error'; readonly text: string };

export function ProviderScreen({
  computer, provider, targetId, tapped, notice, entry, fontSize, multi, onChip, onGone, onSessions, onSettings, onFailure, onRevoked,
}: {
  readonly computer: Computer;
  readonly provider: string;
  readonly targetId: string;
  /** 0: no tap. Else a tap brought the user here (open at once); a new number is a new tap, also for the same address. */
  readonly tapped: number;
  /** Said once over the terminal: what did not start after a new worktree. */
  readonly notice?: string | undefined;
  /** The provider's latest read on that computer, for names and chips; undefined until it answers. */
  readonly entry: ProviderEntry | undefined;
  readonly fontSize: number;
  readonly multi: boolean;
  readonly onChip: (targetId: string) => void;
  readonly onGone: () => void;
  readonly onSessions: () => void;
  readonly onSettings: () => void;
  readonly onFailure: (error: unknown) => string;
  readonly onRevoked: (reason: string | undefined) => void;
}) {
  const [phase, setPhase] = useState<Phase>(tapped > 0 ? { kind: 'opening' } : { kind: 'ask' });
  const [note, setNote] = useState(notice);
  const [since] = useState(Date.now);
  const name = entry?.info.name ?? provider;
  const state = entry === undefined ? undefined : entry.status.kind === 'ok' ? entry.status.snapshot.state : entry.info.state;
  const found = entry === undefined ? undefined : findTarget({ groups: groupsOf(entry) }, targetId);
  // Only before anything opened here.
  const left = targetLeft(entry, found !== undefined, since);
  const busy = useRef(false);
  // Live said the attach ended (Closed here): no pause or open again on hide and show.
  const ended = useRef(false);

  const open = useCallback(async () => {
    if (busy.current) return;
    busy.current = true;
    setPhase({ kind: 'opening' });
    try {
      const grid = phoneGrid(window.innerWidth, window.innerHeight, fontSize);
      const answer = await client(computer.origin).providerOpen(provider, { targetId, ...grid });
      ended.current = false;
      setPhase(openedPhase(answer.session, document.visibilityState === 'hidden'));
    } catch (error) {
      const problem = openProblem(error, name, computer.label, state);
      if (problem?.gone === true) onGone();
      // onFailure also handles home's 401 (Setup) and a peer's (pair again).
      else setPhase({ kind: 'error', text: problem?.text ?? onFailure(error) });
    } finally {
      busy.current = false;
    }
  }, [computer, provider, targetId, fontSize, name, state, onGone, onFailure]);

  const phaseRef = useRef(phase);
  phaseRef.current = phase;
  useEffect(() => {
    // Each tap: the one that brought the user here, and a later notification tap for this same address.
    if (tapped > 0 && tapOpens(phaseRef.current.kind, ended.current)) void open();
  }, [tapped]);

  useEffect(() => {
    if (left && (phase.kind === 'ask' || phase.kind === 'error')) onGone();
  }, [left, phase.kind, onGone]);

  // Hidden: close the stream (the host ends the attach 10 s later and the pane gets its size back). Back: open again.
  useEffect(() => {
    const onVisibility = () => {
      const step = visibilityStep(phaseRef.current.kind, ended.current, document.visibilityState === 'hidden');
      if (step === 'pause') setPhase({ kind: 'paused' });
      else if (step === 'open') void open();
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, [open]);

  const parts = [found?.group.name, found?.item.name].filter((part): part is string => part !== undefined && part !== '').filter((part, index, list) => list.indexOf(part) === index);

  const chips = <ProviderChips item={found?.item} current={targetId} onChip={onChip} onSessions={onSessions} />;

  if (phase.kind === 'open') {
    const noop = () => undefined;
    const none = async () => undefined;
    return (
      <>
      {note !== undefined && (
        <div className="banner" role="status">
          <span className="banner-text">{note}</span>
          <span className="banner-actions"><button type="button" className="link" onClick={() => setNote(undefined)}>Dismiss</button></span>
        </div>
      )}
      <Live
        key={`${phase.session.id}/${phase.session.generation}`}
        session={providerHosted(phase.session, computer)}
        sessions={undefined}
        settings={undefined}
        multi={multi}
        fontSize={fontSize}
        provider={{
          name, place: <Names parts={parts.length === 0 ? [phase.session.title] : parts} />, chips, sizeLine: sizeLine(computer.label), onOpenAgain: open,
          onEnded: () => { ended.current = true; },
        }}
        onOpen={noop}
        onNew={noop}
        onSessions={onSessions}
        onSettings={onSettings}
        onChanged={none}
        onStartNew={none}
        onStartWorktree={none}
        onRevoked={onRevoked}
      />
      </>
    );
  }

  const word = providerWord(found?.target.state ?? 'none');
  return (
    <main className="live">
      {chips}
      <div className="page screen provider-open">
        <p className="caps"><bdi>{name}</bdi> · on {computer.label}</p>
        <h1>{found === undefined ? 'This terminal' : <bdi>{found.item.name}</bdi>}</h1>
        {found !== undefined && (
          <dl className="facts">
            {found.group.name !== found.item.name && <><dt>Project</dt><dd><bdi>{found.group.name}</bdi></dd></>}
            <dt>Tab</dt><dd><bdi>{found.target.label}</bdi></dd>
            {found.target.agent !== undefined && <><dt>Agent</dt><dd><bdi>{found.target.agent}</bdi></dd></>}
            {wordText(word, 1) !== undefined && <><dt>State</dt><dd><StateWord word={word} text={wordText(word, 1)} /></dd></>}
          </dl>
        )}
        <p className="muted">Open shows it here, read-only. {sizeLine(computer.label)}</p>
        <div role="status" className="field-note">
          {phase.kind === 'error' && <Notice tone="danger" title={phase.text} />}
          {phase.kind === 'paused' && <span className="muted">Opening again…</span>}
        </div>
        <div className="bottom-bar">
          <Button className="primary wide" busy={phase.kind === 'opening' || phase.kind === 'paused'} busyLabel="Opening…" onClick={() => { void open(); }}>
            {phase.kind === 'error' ? 'Try again' : 'Open'}
          </Button>
        </div>
      </div>
    </main>
  );
}
