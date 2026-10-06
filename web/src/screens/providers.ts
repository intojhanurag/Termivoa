import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { client, PROVIDER_WORDS, providerWord, type ProviderWord } from '../api/http';
import type { Computer } from '../computers';
import {
  FEATURE_REVISION,
  type HTTPProviderGroup, type HTTPProviderInfo, type HTTPProviderItem, type HTTPProviderSnapshotResponse, type HTTPProviderState, type HTTPProviderTarget,
  type HTTPSessionCreateRequest, type HTTPSessionMutationResponse, type HTTPSettingsResponse,
} from '../generated/protocol';
import { scheduler, type PollOutcome } from '../terminal/scheduler';
import { hosted, type ComputerEntry, type HostedSession } from './allSessions';

/**
 * Terminal workspace providers (herdr) across computers (docs/herdr-provider.md,
 * sections 5 and 9). Each computer that lists a provider gets one more read
 * through the phone-wide scheduler, under its own key: its own failure count,
 * its own queue, and its own status. A provider that fails or hangs never
 * changes a computer's sessions, its status or the Sessions status line.
 * Snapshots live in memory only.
 */

export interface ProviderSource {
  readonly computer: Computer;
  /** From the computer's settings; its `state` may be old, the snapshot's is the one to show. */
  readonly info: HTTPProviderInfo;
  /** The computer answered at FEATURE_REVISION.providerManage or later: the app shows the manage actions (docs/herdr-manage.md). */
  readonly manage: boolean;
  /** At FEATURE_REVISION.providerAgentTab or later: an agent in a new tab, and closed worktrees that open again (docs/herdr-manage.md, section 14). */
  readonly agentTab: boolean;
}

export type ProviderStatus =
  | { readonly kind: 'loading' }
  | { readonly kind: 'ok'; readonly snapshot: HTTPProviderSnapshotResponse; readonly at: number }
  /** "herdr on vps · can't reach". */
  | { readonly kind: 'unreachable' };

export interface ProviderEntry extends ProviderSource {
  /** The scheduler key of this read (providerKey). */
  readonly key: string;
  readonly status: ProviderStatus;
}

/** The scheduler key of one provider read: never a computer's origin, so its failures stay its own. */
export const providerKey = (origin: string, id: string): string => `${origin} provider:${id}`;

/** Every provider of every computer that answered at FEATURE_REVISION.providers or later, in the computers' order. */
export function providerSources(entries: ReadonlyArray<ComputerEntry>): ProviderSource[] {
  return entries.flatMap((entry) => {
    if (entry.status.kind !== 'ok' || entry.status.settings.protocolRevision < FEATURE_REVISION.providers) return [];
    const manage = entry.status.settings.protocolRevision >= FEATURE_REVISION.providerManage;
    const agentTab = entry.status.settings.protocolRevision >= FEATURE_REVISION.providerAgentTab;
    return (entry.status.settings.providers ?? []).map((info) => ({ computer: entry.computer, info, manage, agentTab }));
  });
}

/** The next status after one read: a failure shows at once when nothing was read yet or the user asked, else after the scheduler counts it offline. */
export function nextProviderStatus(previous: ProviderStatus, outcome: PollOutcome<HTTPProviderSnapshotResponse>, offline: boolean, now: number): ProviderStatus {
  if (outcome.ok) return { kind: 'ok', snapshot: outcome.value, at: now };
  return previous.kind === 'ok' && !offline ? previous : { kind: 'unreachable' };
}

/** The provider state to show: the snapshot's when read, else the settings' (docs/herdr-provider.md, 4.5). */
export function providerState(entry: Pick<ProviderEntry, 'info' | 'status'>): HTTPProviderState {
  return entry.status.kind === 'ok' ? entry.status.snapshot.state : entry.info.state;
}

/** Each provider whose own read says another state than its computer's settings: "<origin> <state>", so one change is one line. */
export function staleSettings(entries: ReadonlyArray<Pick<ProviderEntry, 'computer' | 'info' | 'status'>>): string[] {
  return entries.flatMap((entry) => (entry.status.kind === 'ok' && entry.status.snapshot.state !== entry.info.state
    ? [`${entry.computer.origin} ${entry.status.snapshot.state}`] : []));
}

/** Turned off in Settings → Integrations, or not installed: the provider tab leaves that computer out. */
export const shown = (entry: Pick<ProviderEntry, 'info' | 'status'>): boolean => !['off', 'notInstalled'].includes(providerState(entry));

/** A provider that is not installed has no list to read: only its card in Settings → Integrations. */
export const polled = (source: Pick<ProviderSource, 'info'>): boolean => source.info.state !== 'notInstalled';

/** The computers with a provider that is not installed: nothing polls it, so Settings reads their settings when it opens. */
export const unpolledOrigins = (entries: ReadonlyArray<ComputerEntry>): string[] =>
  [...new Set(providerSources(entries).filter((source) => !polled(source)).map((source) => source.computer.origin))];

/**
 * What **Install** types into a new session, by provider ID (docs/herdr-manage.md, A7). Constants of the app:
 * nothing from a computer or from a provider is ever put into them. Typed, not run: the user presses Enter.
 */
/** Add a folder: typed into a new session, not run; the user types the folder and presses Enter. */
export const ADD_FOLDER_COMMAND = 'pterm project add ';

/**
 * A new session on that computer, as New session starts one (its first project and default shell), for a command the
 * app types and does not run (Install herdr, Add a folder; docs/herdr-manage.md, A7). Undefined when it has no project or no shell: New session's own help says what to do.
 */
export async function startInstall(
  computer: Computer, settings: Pick<HTTPSettingsResponse, 'projects' | 'shellProfiles'>,
  create: (body: HTTPSessionCreateRequest) => Promise<HTTPSessionMutationResponse> = (body) => client(computer.origin).createSession(body),
): Promise<HostedSession | undefined> {
  const [project] = settings.projects;
  const [shell] = settings.shellProfiles;
  if (project === undefined || shell === undefined) return undefined;
  return hosted((await create({ operationId: crypto.randomUUID(), projectId: project.id, shellProfileId: shell.id })).session, computer);
}

export const INSTALL_COMMANDS: Readonly<Partial<Record<string, string>>> = { herdr: 'curl -fsSL https://herdr.dev/install.sh | sh' };

const rank = (word: string): number => PROVIDER_WORDS.indexOf(providerWord(word));

/** The most urgent word: blocked, done, working, idle, unknown, none (docs/herdr-provider.md, section 5). */
export function rollUp(words: ReadonlyArray<string>): ProviderWord {
  return PROVIDER_WORDS[Math.min(PROVIDER_WORDS.length - 1, ...words.map(rank))] ?? 'none';
}

/** Words that need a look: the tab counts them. */
export const NEEDS_YOU: ReadonlySet<string> = new Set(['blocked', 'done']);

/** The groups of a snapshot that can be shown, or none while it is not ready. */
export function groupsOf(entry: Pick<ProviderEntry, 'status'>): ReadonlyArray<HTTPProviderGroup> {
  return entry.status.kind === 'ok' && entry.status.snapshot.state === 'ready' ? entry.status.snapshot.groups : [];
}

export const targetsOf = (groups: ReadonlyArray<HTTPProviderGroup>): HTTPProviderTarget[] =>
  groups.flatMap((group) => group.items.flatMap((item) => item.targets));

/** How many terminals are in each word. */
export function countWords(targets: ReadonlyArray<Pick<HTTPProviderTarget, 'state'>>): ReadonlyMap<ProviderWord, number> {
  const counts = new Map<ProviderWord, number>();
  for (const target of targets) counts.set(providerWord(target.state), (counts.get(providerWord(target.state)) ?? 0) + 1);
  return counts;
}

/** The tab's count: terminals that are blocked or done, over every computer shown. */
export function attentionCount(entries: ReadonlyArray<ProviderEntry>): number {
  return entries.filter(shown).flatMap((entry) => targetsOf(groupsOf(entry))).filter((target) => NEEDS_YOU.has(target.state)).length;
}

/** "1 blocked · 1 done · 2 working", or undefined when no agent is busy. */
export function wordSummary(entries: ReadonlyArray<ProviderEntry>): string | undefined {
  const counts = countWords(entries.filter(shown).flatMap((entry) => targetsOf(groupsOf(entry))));
  const parts = (['blocked', 'done', 'working'] as const).flatMap((word) => {
    const count = counts.get(word) ?? 0;
    return count === 0 ? [] : [`${count} ${word}`];
  });
  return parts.length === 0 ? undefined : parts.join(' · ');
}

/** One tab per provider (herdr) while a computer shows it; none without any. */
export function providerTabs(entries: ReadonlyArray<ProviderEntry>): ReadonlyArray<{ readonly id: string; readonly name: string; readonly count: number }> {
  const tabs = new Map<string, { id: string; name: string; count: number }>();
  for (const entry of entries.filter(shown)) {
    const tab = tabs.get(entry.info.id) ?? { id: entry.info.id, name: entry.info.name, count: 0 };
    tab.count += attentionCount([entry]);
    tabs.set(entry.info.id, tab);
  }
  return [...tabs.values()];
}

/** Where a target is: its group and item in a snapshot, or undefined when it left. */
export function findTarget(snapshot: { readonly groups: ReadonlyArray<HTTPProviderGroup> }, targetId: string): {
  readonly group: HTTPProviderGroup;
  readonly item: HTTPProviderItem;
  readonly target: HTTPProviderTarget;
} | undefined {
  for (const group of snapshot.groups) {
    for (const item of group.items) {
      const target = item.targets.find((candidate) => candidate.id === targetId);
      if (target !== undefined) return { group, item, target };
    }
  }
  return undefined;
}

export interface Providers {
  readonly entries: ReadonlyArray<ProviderEntry>;
  /** Reads one provider now (Retry, after a create); settles when it answered or failed. */
  readonly reload: (key: string) => Promise<void>;
}

/** Polls every listed provider while `active`, beside (never inside) the sessions read of useAllSessions. */
export function useProviders(active: boolean, computers: ReadonlyArray<ComputerEntry>): Providers {
  const sources = providerSources(computers).filter(polled);
  const byKey = useRef(new Map<string, ProviderSource>());
  byKey.current = new Map(sources.map((source) => [providerKey(source.computer.origin, source.info.id), source]));
  const keys = [...byKey.current.keys()].join('\n');
  const [statuses, setStatuses] = useState<ReadonlyMap<string, ProviderStatus>>(new Map());

  const load = useCallback((key: string) => {
    const source = byKey.current.get(key);
    return source === undefined ? Promise.reject(new Error('gone')) : client(source.computer.origin).provider(source.info.id);
  }, []);

  const publish = useCallback((key: string, outcome: PollOutcome<HTTPProviderSnapshotResponse>, asked = false) => {
    if (!byKey.current.has(key)) return;
    setStatuses((current) => new Map(current).set(key, nextProviderStatus(current.get(key) ?? { kind: 'loading' }, outcome, asked || scheduler.offline(key), Date.now())));
  }, []);

  // A provider that left (computer removed, uninstalled, revision lower) keeps nothing.
  useEffect(() => {
    const now = new Set(keys === '' ? [] : keys.split('\n'));
    setStatuses((current) => ([...current.keys()].every((key) => now.has(key)) ? current : new Map([...current].filter(([key]) => now.has(key)))));
  }, [keys]);

  useEffect(() => {
    if (!active || keys === '') return undefined;
    // ponytail: the scheduler polls at most maxComputers keys; one provider per computer fits, more would be cut.
    return scheduler.poll(keys.split('\n'), load, publish, 'providers');
  }, [active, keys, load, publish]);

  const reload = useCallback((key: string) => {
    scheduler.retry(key);
    return scheduler.serial(key, () => load(key), 'providers').then(
      (value) => { scheduler.reported(key, true); publish(key, { ok: true, value }, true); },
      (error: unknown) => { scheduler.reported(key, false); publish(key, { ok: false, error }, true); },
    );
  }, [load, publish]);

  const entries = useMemo(
    () => (keys === '' ? [] : keys.split('\n')).flatMap((key) => {
      const source = byKey.current.get(key);
      return source === undefined ? [] : [{ ...source, key, status: statuses.get(key) ?? { kind: 'loading' as const } }];
    }),
    // byKey follows `computers`; settings (info) can change without the keys changing.
    [keys, statuses, computers],
  );
  return { entries, reload };
}
