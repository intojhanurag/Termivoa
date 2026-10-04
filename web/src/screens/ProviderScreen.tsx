import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, client, providerWord } from '../api/http';
import type { Computer } from '../computers';
import type { HTTPProviderItem, HTTPProviderSession } from '../generated/protocol';
import { fitGrid, MONOSPACE } from '../terminal/TerminalView';
import { hosted, type HostedSession } from './allSessions';
import { GONE, itemMeta, StateWord, wordText } from './Herdr';
import { Live } from './Live';
import { findTarget, groupsOf, type ProviderEntry } from './providers';
import { Button, Notice } from './ui';

/*
 * One provider terminal in Live (docs/herdr-provider.md, section 6, rules 8–10).
 * It opens at once only after a tap (a row or chip in the app, or a
 * notification tap the service worker confirmed); any other arrival shows the
 * terminal's names and Open. It never takes control. While the page is
 * hidden the stream is closed, so the computer gets its pane size back; it
 * opens again on return.
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

/** What a failed open says, for the cases the app words itself; `gone` sends the user back to the provider tab. */
export function openProblem(error: unknown, name: string, label: string): { readonly gone: boolean; readonly text: string } | undefined {
  switch (error instanceof ApiError ? error.code : undefined) {
    case 'session_missing': return { gone: true, text: GONE };
    case 'limit_exceeded': return { gone: false, text: `Too many ${name} terminals are open on ${label}. Close one, or wait a few seconds.` };
    case 'host_unavailable': return { gone: false, text: `${name} is not running on ${label}.` };
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
                <span className="tab-label">{repeated(target.label) && target.agent !== undefined ? `${target.label} · ${target.agent}` : target.label}</span>
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

type Phase =
  | { readonly kind: 'ask' }
  | { readonly kind: 'opening' }
  | { readonly kind: 'open'; readonly session: HTTPProviderSession }
  /** The page is hidden: the stream is closed; back in front it opens again. */
  | { readonly kind: 'paused' }
  | { readonly kind: 'error'; readonly text: string };

export function ProviderScreen({
  computer, provider, targetId, tapped, entry, fontSize, multi, onChip, onGone, onSessions, onSettings, onFailure, onRevoked,
}: {
  readonly computer: Computer;
  readonly provider: string;
  readonly targetId: string;
  /** A tap brought the user here: open at once. */
  readonly tapped: boolean;
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
  const [phase, setPhase] = useState<Phase>(tapped ? { kind: 'opening' } : { kind: 'ask' });
  const name = entry?.info.name ?? provider;
  const found = entry === undefined ? undefined : findTarget({ groups: groupsOf(entry) }, targetId);
  // A read that is ready and lacks the target: it left. Only before anything opened here.
  const left = entry?.status.kind === 'ok' && entry.status.snapshot.state === 'ready' && found === undefined;
  const busy = useRef(false);

  const open = useCallback(async () => {
    if (busy.current) return;
    busy.current = true;
    setPhase({ kind: 'opening' });
    try {
      const grid = phoneGrid(window.innerWidth, window.innerHeight, fontSize);
      const answer = await client(computer.origin).providerOpen(provider, { targetId, ...grid });
      setPhase({ kind: 'open', session: answer.session });
    } catch (error) {
      const problem = openProblem(error, name, computer.label);
      if (problem?.gone === true) onGone();
      // onFailure also handles home's 401 (Setup) and a peer's (pair again).
      else setPhase({ kind: 'error', text: problem?.text ?? onFailure(error) });
    } finally {
      busy.current = false;
    }
  }, [computer, provider, targetId, fontSize, name, onGone, onFailure]);

  useEffect(() => {
    // Once, for the tap that brought the user here.
    if (tapped) void open();
  }, []);

  useEffect(() => {
    if (left && (phase.kind === 'ask' || phase.kind === 'error')) onGone();
  }, [left, phase.kind, onGone]);

  // Hidden: close the stream (the host ends the attach 10 s later and the pane gets its size back). Back: open again.
  const phaseRef = useRef(phase);
  phaseRef.current = phase;
  useEffect(() => {
    const onVisibility = () => {
      if (document.visibilityState === 'hidden' && phaseRef.current.kind === 'open') setPhase({ kind: 'paused' });
      else if (document.visibilityState === 'visible' && phaseRef.current.kind === 'paused') void open();
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, [open]);

  const place = [found?.group.name, found?.item.name].filter((part) => part !== undefined && part !== '').filter((part, index, list) => list.indexOf(part) === index).join(' · ');
  const chips = <ProviderChips item={found?.item} current={targetId} onChip={onChip} onSessions={onSessions} />;

  if (phase.kind === 'open') {
    const noop = () => undefined;
    const none = async () => undefined;
    return (
      <Live
        key={`${phase.session.id}/${phase.session.generation}`}
        session={providerHosted(phase.session, computer)}
        sessions={undefined}
        settings={undefined}
        multi={multi}
        fontSize={fontSize}
        provider={{ name, place: place === '' ? phase.session.title : place, chips, sizeLine: sizeLine(computer.label), onOpenAgain: open }}
        onOpen={noop}
        onNew={noop}
        onSessions={onSessions}
        onSettings={onSettings}
        onChanged={none}
        onStartNew={none}
        onStartWorktree={none}
        onRevoked={onRevoked}
      />
    );
  }

  const word = providerWord(found?.target.state ?? 'none');
  return (
    <main className="live">
      {chips}
      <div className="page screen provider-open">
        <p className="caps">{name} · on {computer.label}</p>
        <h1>{found?.item.name ?? 'This terminal'}</h1>
        {found !== undefined && (
          <dl className="facts">
            {found.group.name !== found.item.name && <><dt>Project</dt><dd>{found.group.name}</dd></>}
            <dt>Tab</dt><dd>{found.target.label}</dd>
            {found.target.agent !== undefined && <><dt>Agent</dt><dd>{found.target.agent}</dd></>}
            {wordText(word, 1) !== undefined && <><dt>State</dt><dd><StateWord word={word} text={wordText(word, 1)} /></dd></>}
          </dl>
        )}
        {found !== undefined && <p className="hint-text">{itemMeta(found.item)}</p>}
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
