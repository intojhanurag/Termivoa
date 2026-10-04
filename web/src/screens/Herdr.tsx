import { Fragment, useReducer, useState, type ReactNode } from 'react';
import { providerWord, type ProviderWord } from '../api/http';
import type { Computer } from '../computers';
import type { HTTPProviderGroup, HTTPProviderItem } from '../generated/protocol';
import { since } from './activity';
import { atLeast, cantReach, CHECKING_MIN_MS } from './allSessions';
import { countWords, groupsOf, providerState, rollUp, shown, type ProviderEntry } from './providers';
import { Button, Notice } from './ui';

/*
 * Provider projects in Sessions (Paper H1 and H3; docs/herdr-provider.md,
 * sections 5, 9 and 14). Names from a provider are text only, IDs are only
 * handed back to the same computer. No terminal title or question text is
 * shown: the host never sends them.
 */

/** Shown on the provider tab when a terminal left (docs/herdr-provider.md, rule 9). */
export const GONE = 'That terminal is gone.';

/** What a tap on a row opens: one terminal of one provider on one computer. */
export interface ProviderTap {
  readonly computer: Computer;
  readonly provider: string;
  readonly targetId: string;
}

/** Words with a colour and a dot; idle is plain muted; unknown and none show nothing. */
const TONES: Partial<Record<ProviderWord, string>> = { blocked: 'amber', working: 'accent', done: 'green', idle: 'muted' };

/** The state word on the right of a row, or undefined for unknown and none. "2 WORKING", "BLOCKED · 4m". */
export function wordText(word: ProviderWord, count: number, sinceText = ''): string | undefined {
  if (TONES[word] === undefined) return undefined;
  if (count > 1) return `${count} ${word}`;
  return sinceText === '' ? word : `${word} · ${sinceText}`;
}

export function StateWord({ word, text }: { readonly word: ProviderWord; readonly text: string | undefined }) {
  if (text === undefined) return null;
  return <span className={`state-word ${TONES[word] ?? 'muted'}`}>{text}</span>;
}

/** ["claude", "tab 1"], ["claude", "codex", "3 tabs"]: agents and tabs, never a terminal title (section 14). Shown joined by " · ". */
export function itemMeta(item: HTTPProviderItem): string[] {
  const agents = [...new Set(item.targets.flatMap((target) => (target.agent === undefined ? [] : [target.agent])))];
  const tabs = [...new Set(item.targets.map((target) => target.label))];
  const where = tabs.length > 1 ? `${tabs.length} tabs` : tabs[0] === undefined ? undefined : `tab ${tabs[0]}`;
  return [...agents, ...(where === undefined ? [] : [where])];
}

/** Names from a provider, each isolated (bdi), so a right-to-left or control mark in one cannot reorder the rest. */
export function Names({ parts }: { readonly parts: ReadonlyArray<string> }) {
  return <>{parts.map((part, index) => <Fragment key={index}>{index > 0 && ' · '}<bdi>{part}</bdi></Fragment>)}</>;
}

/** Row names of one computer's groups; a repeated name gets its place among the repeats: "notes", "notes · 2". */
export function numberedNames(groups: ReadonlyArray<Pick<HTTPProviderGroup, 'name'>>): string[] {
  const seen = new Map<string, number>();
  return groups.map((group) => {
    const count = (seen.get(group.name) ?? 0) + 1;
    seen.set(group.name, count);
    return count === 1 ? group.name : `${group.name} · ${count}`;
  });
}

/** A workspace outside git: one item named like its group. It shows as one row that opens at once. */
export const singleRow = (group: HTTPProviderGroup): HTTPProviderItem | undefined =>
  group.items.length === 1 && group.items[0]?.name === group.name ? group.items[0] : undefined;

/** Folded or not, per computer and group, for this visit only (memory). */
const folds = new Map<string, boolean>();
/** A group starts open when something in it needs you or runs. */
export const openByDefault = (group: HTTPProviderGroup): boolean => ['blocked', 'done', 'working'].includes(group.state);
export const foldKey = (computer: Pick<Computer, 'host'>, group: Pick<HTTPProviderGroup, 'id'>): string => `${computer.host}/${group.id}`;
export function isOpen(computer: Pick<Computer, 'host'>, group: HTTPProviderGroup): boolean {
  return folds.get(foldKey(computer, group)) ?? openByDefault(group);
}

/** DONE stays until someone sees it in herdr (the phone must not move the laptop's focus); rows opened here since are dimmed (section 14). */
const seenDone = new Set<string>();
const doneKey = (computer: Pick<Computer, 'host'>, item: HTTPProviderItem) => `${computer.host}/${item.id}/${item.stateSince ?? ''}`;
export function markOpened(computer: Pick<Computer, 'host'>, item: HTTPProviderItem): void {
  if (item.state === 'done') seenDone.add(doneKey(computer, item));
}
export const dimmed = (computer: Pick<Computer, 'host'>, item: HTTPProviderItem): boolean =>
  item.state === 'done' && seenDone.has(doneKey(computer, item));

/** The first blocked item that can open, across every computer, for "Open the blocked agent". */
export function firstBlocked(entries: ReadonlyArray<ProviderEntry>): ProviderTap | undefined {
  for (const entry of entries.filter(shown)) {
    for (const group of groupsOf(entry)) {
      const found = group.items.find((item) => item.state === 'blocked' && item.openTarget !== undefined);
      if (found?.openTarget !== undefined) return { computer: entry.computer, provider: entry.info.id, targetId: found.openTarget };
    }
  }
  return undefined;
}

function ItemRow({ entry, item, name, big, now, onOpen }: {
  readonly entry: ProviderEntry;
  readonly item: HTTPProviderItem;
  readonly name: string;
  /** All tab (H1): a 17 px name. herdr tab (H3): a mono name under its project. */
  readonly big: boolean;
  readonly now: number;
  readonly onOpen: (tap: ProviderTap) => void;
}) {
  const word = providerWord(item.state);
  const count = countWords(item.targets).get(word) ?? 0;
  const open = item.openTarget;
  return (
    <li>
      <button
        type="button"
        className={`session-row provider-item${big ? '' : ' nested'}${dimmed(entry.computer, item) ? ' seen' : ''}`}
        disabled={open === undefined}
        onClick={() => {
          if (open === undefined) return;
          markOpened(entry.computer, item);
          onOpen({ computer: entry.computer, provider: entry.info.id, targetId: open });
        }}
      >
        <span className="row-text">
          <span className="row-head">
            <span className="row-title"><bdi>{name}</bdi></span>
            <StateWord word={word} text={wordText(word, count, since(item.stateSince, now))} />
          </span>
          <span className="row-meta"><Names parts={itemMeta(item)} /></span>
        </span>
      </button>
    </li>
  );
}

/** A provider's trouble on one computer, or nothing while it is fine. */
export function ProviderProblem({ entry, onRetry }: { readonly entry: ProviderEntry; readonly onRetry: (key: string) => Promise<void> }) {
  const [retrying, setRetrying] = useState(false);
  const { computer, info } = entry;
  if (entry.status.kind === 'unreachable') {
    const retry = () => {
      setRetrying(true);
      void atLeast(onRetry(entry.key), CHECKING_MIN_MS).finally(() => setRetrying(false));
    };
    return (
      <Notice
        tone="warning"
        title={cantReach(`${info.name} on ${computer.label}`)}
        action={<Button className="link" busy={retrying} busyLabel="Retrying…" onClick={retry}>Retry</Button>}
      />
    );
  }
  switch (providerState(entry)) {
    case 'notRunning': return <Notice tone="neutral" title={`${info.name} is not running on ${computer.label}`}>Start it there; its projects show here.</Notice>;
    case 'unsupported': return <Notice tone="neutral" title={`Update ${info.name} on ${computer.label}`}>This version is too old for Termivoa.</Notice>;
    default: return null;
  }
}

/** The herdr tab (H3): each computer's projects, fold and unfold, worktrees with state words. */
export function ProviderTree({ entries, multi, now, onOpen, onCreate, onRetry }: {
  readonly entries: ReadonlyArray<ProviderEntry>;
  /** The phone uses more than one computer: "ON MAC" by each project. */
  readonly multi: boolean;
  readonly now: number;
  readonly onOpen: (tap: ProviderTap) => void;
  /** Absent until the + sheet is wired; shown only where both the provider and the group can create. */
  readonly onCreate?: ((entry: ProviderEntry, group: HTTPProviderGroup) => void) | undefined;
  readonly onRetry: (key: string) => Promise<void>;
}) {
  const [, redraw] = useReducer((value: number) => value + 1, 0);
  const list = entries.filter(shown);
  const loading = list.length > 0 && list.every((entry) => entry.status.kind === 'loading');
  return (
    <section aria-label={`${list[0]?.info.name ?? 'Provider'} projects`} className="provider-tree">
      {loading && <p role="status" className="muted">Loading…</p>}
      {list.map((entry) => {
        const groups = groupsOf(entry);
        const names = numberedNames(groups);
        const ready = entry.status.kind === 'ok' && entry.status.snapshot.state === 'ready';
        return (
          <div key={entry.key} className="stack provider-computer">
            <ProviderProblem entry={entry} onRetry={onRetry} />
            {ready && groups.length === 0 && <p className="muted">No {entry.info.name} projects on {entry.computer.label}.</p>}
            {groups.length > 0 && (
              <ul className="rows">
                {groups.map((group, index) => {
                  const name = names[index] ?? group.name;
                  const state = <StateWord word={providerWord(group.state)} text={groupWordText(group)} />;
                  const where = multi ? <span className="group-where">on {entry.computer.label}</span> : null;
                  // A git project with only its main checkout is one row too, and keeps its +.
                  const add = onCreate !== undefined && entry.info.canCreate && group.canCreate
                    ? <AddButton name={name} onClick={() => onCreate(entry, group)} />
                    : null;
                  const single = singleRow(group);
                  if (single !== undefined) {
                    return (
                      <ItemGroupRow key={group.id} entry={entry} item={single} name={name} where={where} state={state} add={add} onOpen={onOpen} />
                    );
                  }
                  const open = isOpen(entry.computer, group);
                  const id = `group-${foldKey(entry.computer, group).replace(/[^A-Za-z0-9_-]/gu, '-')}`;
                  return (
                    <li key={group.id} className="provider-group">
                      <div className="group-row">
                        <button
                          type="button"
                          className="group-toggle"
                          aria-expanded={open}
                          {...(open ? { 'aria-controls': id } : {})}
                          onClick={() => { folds.set(foldKey(entry.computer, group), !open); redraw(); }}
                        >
                          <span className="fold-glyph" aria-hidden="true">{open ? '▾' : '▸'}</span>
                          <GroupTitle name={name} where={where} count={!open && group.items.length > 1 ? group.items.length : undefined} />
                          <span className="group-state">{state}</span>
                        </button>
                        {add}
                      </div>
                      {open && (
                        <ul className="rows group-items" id={id}>
                          {group.items.map((item) => (
                            <ItemRow key={item.id} entry={entry} item={item} name={item.name} big={false} now={now} onOpen={onOpen} />
                          ))}
                        </ul>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
            {entry.status.kind === 'ok' && entry.status.snapshot.truncated === true && (
              <p className="hint-text">Some projects on {entry.computer.label} are not shown.</p>
            )}
          </div>
        );
      })}
    </section>
  );
}

/**
 * A project's name and its small labels ("ON MAC", "2 WORKTREES"). The name
 * has priority: the labels wrap under it when both do not fit on one line,
 * so the name is cut only when it alone is wider than the row.
 */
function GroupTitle({ name, where, count }: { readonly name: string; readonly where: ReactNode; readonly count?: number | undefined }) {
  return (
    <span className="group-title">
      <span className="group-name"><bdi>{name}</bdi></span>
      {where}
      {count !== undefined && <span className="group-where">{count} worktrees</span>}
    </span>
  );
}

function AddButton({ name, onClick }: { readonly name: string; readonly onClick: () => void }) {
  return (
    <button type="button" className="round group-add" aria-label={`New worktree in ${name}`} onClick={onClick}>
      <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">
        <path d="M7 1v12M1 7h12" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
      </svg>
    </button>
  );
}

/** A workspace outside git, or a git project with only its main checkout: one row like a project row, that opens its terminal. */
function ItemGroupRow({ entry, item, name, where, state, add, onOpen }: {
  readonly entry: ProviderEntry;
  readonly item: HTTPProviderItem;
  readonly name: string;
  readonly where: ReactNode;
  readonly state: ReactNode;
  /** The + of a git project; null outside git. */
  readonly add: ReactNode;
  readonly onOpen: (tap: ProviderTap) => void;
}) {
  const open = item.openTarget;
  return (
    <li className="provider-group">
      <div className="group-row">
        <button
          type="button"
          className={`group-toggle${dimmed(entry.computer, item) ? ' seen' : ''}`}
          disabled={open === undefined}
          onClick={() => {
            if (open === undefined) return;
            markOpened(entry.computer, item);
            onOpen({ computer: entry.computer, provider: entry.info.id, targetId: open });
          }}
        >
          <span className="fold-glyph" aria-hidden="true" />
          <GroupTitle name={name} where={where} />
          <span className="group-state">{state}</span>
        </button>
        {add}
      </div>
    </li>
  );
}

/** The All tab's provider part (H1): per project, the worktrees with an agent that has a state word. */
export function sectionParts(entries: ReadonlyArray<ProviderEntry>) {
  return entries.filter(shown).flatMap((entry) => {
    const groups = groupsOf(entry);
    const names = numberedNames(groups);
    return groups.flatMap((group, index) => {
      const items = group.items.filter((item) => item.targets.some((target) => target.agent !== undefined && TONES[providerWord(target.state)] !== undefined));
      const agents = group.items.flatMap((item) => item.targets).filter((target) => target.agent !== undefined).length;
      return items.length === 0 ? [] : [{ entry, group, name: names[index] ?? group.name, items, agents }];
    });
  });
}

export function ProviderSection({ entries, multi, now, onOpen }: {
  readonly entries: ReadonlyArray<ProviderEntry>;
  readonly multi: boolean;
  readonly now: number;
  readonly onOpen: (tap: ProviderTap) => void;
}) {
  const parts = sectionParts(entries);
  if (parts.length === 0) return null;
  return (
    <>
      {parts.map(({ entry, group, name, items, agents }) => (
        <section key={`${entry.key}/${group.id}`} aria-label={`${entry.info.name} · ${name}${multi ? ` on ${entry.computer.label}` : ''}`}>
          <div className="provider-head">
            <span className="home-badge"><bdi>{entry.info.name}</bdi></span>
            <span className="caps provider-head-name"><bdi>{name}</bdi>{multi ? ` · on ${entry.computer.label}` : ''}</span>
            <span className="caps faint">{agents} {agents === 1 ? 'agent' : 'agents'}</span>
          </div>
          <ul className="rows">
            {items.map((item) => (
              <ItemRow key={item.id} entry={entry} item={item} name={item.name} big now={now} onOpen={onOpen} />
            ))}
          </ul>
        </section>
      ))}
    </>
  );
}

/** The state word of a whole project: "1 BLOCKED", "2 WORKING", "IDLE". */
export function groupWordText(group: HTTPProviderGroup): string | undefined {
  const word = rollUp([group.state]);
  if (word === 'idle') return wordText(word, 1);
  const count = countWords(group.items.flatMap((item) => item.targets)).get(word) ?? 0;
  return TONES[word] === undefined ? undefined : `${count} ${word}`;
}
