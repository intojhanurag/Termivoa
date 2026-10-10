import type { ReactNode } from 'react';
import { githubInfo } from './GitHub';
import type { ComputerEntry } from './allSessions';
import { providerKey, providerSources, providerState, type ProviderEntry } from './providers';
import { ago, problemDetail, problemText } from './Sessions';
import '../styles/settings.css';

/*
 * Settings by computer (docs/ui-revamp.md, section 5): the parts of the main page's computer cards and of the
 * computer page that need no lazy chunk. Computers.tsx (lazy) has the actions and the add flow.
 */

/** The state word of a computer: on its card and on its page. */
export function onlineText(entry: ComputerEntry): { readonly text: string; readonly tone: 'green' | 'muted' | 'amber' } {
  switch (entry.status.kind) {
    case 'ok': return { text: 'Online', tone: 'green' };
    case 'loading': return { text: 'Checking', tone: 'muted' };
    case 'connecting': return { text: 'Connecting', tone: 'muted' };
    case 'unreachable': return { text: 'Can\'t reach', tone: 'amber' };
    default: return { text: 'Needs you', tone: 'amber' };
  }
}

/** "2 sessions · vps.tailx.ts.net" ("2 sessions" for home, which has the Home badge), or the problem ("vps · can't reach"; problemDetail says more). */
export function computerDetail(entry: ComputerEntry, now: number): string {
  const problem = problemText(entry, now);
  if (problem !== undefined) return problem;
  const count = entry.status.kind === 'ok' ? entry.status.sessions.length : undefined;
  return [
    count === undefined ? undefined : `${count} ${count === 1 ? 'session' : 'sessions'}`,
    entry.computer.home ? undefined : entry.computer.host,
  ].filter((part) => part !== undefined).join(' · ');
}

/**
 * "Termivoa 0.1.0-beta.2+1a2b3c4d5e6f · last seen just now": the build the
 * computer runs (plain text, bounded by the schema) and when this phone last
 * read it. Undefined when it did not answer (computerDetail says why).
 */
export function healthText(entry: ComputerEntry, now: number): string | undefined {
  const { status } = entry;
  return status.kind === 'ok' ? `Termivoa ${status.settings.hostBuild} · ${ago(status.at, now)}` : undefined;
}

/** A computer that did not answer and will not by itself: its card is dashed and has Check again. */
export const troubled = (entry: ComputerEntry): boolean => !['ok', 'loading', 'connecting'].includes(entry.status.kind);

/** The colour of the dot before a computer's label. */
export function statusDot(entry: ComputerEntry): 'green' | 'amber' | 'red' | 'muted' {
  switch (entry.status.kind) {
    case 'ok': return 'green';
    case 'loading': case 'connecting': return 'muted';
    case 'pair': return 'red';
    default: return 'amber';
  }
}

export interface Chip {
  readonly text: string;
  /** `dashed`: an action waits there (Connect GitHub). */
  readonly tone: 'green' | 'amber' | 'muted' | 'dashed';
  /** Draws a dot before the text. */
  readonly dot: boolean;
}

/**
 * The chips of a computer's card: each provider it lists (herdr; the provider read's state is newer than the
 * settings'), GitHub where it reports it, and its projects. None until the computer answered.
 */
export function cardChips(entry: ComputerEntry, providers: ReadonlyArray<ProviderEntry> = []): Chip[] {
  if (entry.status.kind !== 'ok') return [];
  const { settings } = entry.status;
  const herdr = providerSources([entry]).map((source): Chip => {
    const read = providers.find((one) => one.key === providerKey(source.computer.origin, source.info.id));
    const state = read === undefined ? source.info.state : providerState(read);
    // As the state word on the computer's page: running green, not running (or too old) amber, off or not installed grey.
    const tone = state === 'ready' ? 'green' : state === 'notRunning' || state === 'unsupported' ? 'amber' : 'muted';
    return { text: source.info.name, tone, dot: true };
  });
  const github = githubInfo(settings);
  const chips: Chip[] = [...herdr];
  if (github !== undefined) {
    switch (github.state) {
      case 'ready': chips.push({ text: 'GitHub', tone: 'green', dot: true }); break;
      case 'notConnected': chips.push({ text: 'Connect GitHub', tone: 'dashed', dot: false }); break;
      case 'noGit': chips.push({ text: 'git missing', tone: 'amber', dot: true }); break;
      case 'needsInstall': chips.push({ text: 'GitHub', tone: 'amber', dot: true }); break;
      default: chips.push({ text: 'GitHub', tone: 'muted', dot: true });
    }
  }
  const count = settings.projects.length;
  chips.push({ text: `${count} ${count === 1 ? 'project' : 'projects'}`, tone: 'muted', dot: false });
  return chips;
}

/** One computer on the main page: tap it for its page. A dashed card also shows what is wrong, and `children` (Check again). */
export function ComputerCard({ entry, providers, now, onOpen, children }: {
  readonly entry: ComputerEntry;
  readonly providers?: ReadonlyArray<ProviderEntry> | undefined;
  readonly now: number;
  readonly onOpen: () => void;
  readonly children?: ReactNode;
}) {
  const { computer } = entry;
  const online = onlineText(entry);
  const chips = cardChips(entry, providers);
  const bad = troubled(entry);
  // "vps · can't reach" says what the state word on the card says already.
  const detail = entry.status.kind === 'ok' || entry.status.kind === 'unreachable' ? '' : computerDetail(entry, now);
  const more = problemDetail(entry, now);
  return (
    <li className={`computer-card sc-card is-${entry.status.kind}${bad ? ' dashed' : ''}`}>
      <button type="button" className="sc-open" onClick={onOpen}>
        <span className="sc-card-head">
          <span className={`dot ${statusDot(entry)}`} aria-hidden="true" />
          <span className="computer-name">{computer.label}</span>
          {computer.home && <span className="home-badge">Home</span>}
          {entry.status.kind === 'ok'
            // The green dot says it on screen; a screen reader hears the word.
            ? <span className="visually-hidden">, {online.text}</span>
            : <span className="computer-state sc-word">{online.text}</span>}
          <span className="sc-chevron" aria-hidden="true">›</span>
        </span>
        {chips.length > 0 && (
          <span className="sc-chips">
            {chips.map((chip) => (
              <span key={chip.text} className={`sc-chip ${chip.tone}`}>{chip.dot && <span className={`dot ${chip.tone}`} aria-hidden="true" />}{chip.text}</span>
            ))}
          </span>
        )}
        {detail !== '' && <span className="row-meta">{detail}</span>}
        {more !== undefined && <span className="hint-text">{more}</span>}
      </button>
      {children}
    </li>
  );
}

/** The head of a computer's page: its label, state word, Home, address, build, and what is wrong. */
export function ComputerHead({ entry, now, onBack }: { readonly entry: ComputerEntry; readonly now: number; readonly onBack: () => void }) {
  const { computer } = entry;
  const online = onlineText(entry);
  const health = healthText(entry, now);
  const detail = entry.status.kind === 'ok' ? '' : computerDetail(entry, now);
  const more = problemDetail(entry, now);
  return (
    <header className={`sc-page-head is-${entry.status.kind}`}>
      <button type="button" className="link accent sc-back" onClick={onBack}>‹ Settings</button>
      <div className="sc-title">
        <h1 className="display" tabIndex={-1}>{computer.label}</h1>
        <span className={`computer-state ${online.tone}`}>{online.text}{computer.home ? ' · Home' : ''}</span>
      </div>
      <span className="row-meta">{computer.host}</span>
      {health !== undefined && <span className="row-meta">{health}</span>}
      {detail !== '' && <span className="row-meta">{detail}</span>}
      {more !== undefined && <span className="hint-text">{more}</span>}
    </header>
  );
}

// ---------------------------------------------------------------- Where Settings is

/** The level Settings shows: the main page, one computer's page, or Notifications. */
export type SettingsPage =
  | { readonly kind: 'main' }
  | { readonly kind: 'computer'; readonly origin: string }
  | { readonly kind: 'notifications' };

export const MAIN_PAGE: SettingsPage = { kind: 'main' };

/** The key of Settings' own history entries (App's entries carry null or the preview tap). */
export const PAGE_KEY = 'termivoaSettings';

/** The page of a history entry: Back and Forward land on the page that entry was pushed for, else the main page. */
export function pageFromHistory(state: unknown): SettingsPage {
  const page = typeof state === 'object' && state !== null ? (state as Record<string, unknown>)[PAGE_KEY] : undefined;
  if (typeof page !== 'object' || page === null) return MAIN_PAGE;
  const { kind, origin } = page as Record<string, unknown>;
  if (kind === 'notifications') return { kind };
  if (kind === 'computer' && typeof origin === 'string') return { kind, origin };
  return MAIN_PAGE;
}

/** The page Settings opens at: Notifications when asked for, else the one of the history entry (Back from another screen). */
export function initialPage(section: string | undefined, state: unknown): SettingsPage {
  return section === 'notifications' ? { kind: 'notifications' } : section === undefined ? pageFromHistory(state) : MAIN_PAGE;
}

/** The page to draw: a computer that left the list (removed), or Notifications while home cannot be reached (it needs home), shows the main page. */
export function shownPage(page: SettingsPage, entries: ReadonlyArray<ComputerEntry>, homeAnswered = true): SettingsPage {
  if (page.kind === 'notifications') return homeAnswered ? page : MAIN_PAGE;
  return page.kind === 'computer' && !entries.some((entry) => entry.computer.origin === page.origin) ? MAIN_PAGE : page;
}
