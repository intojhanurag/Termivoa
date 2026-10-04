/* Demo provider data for tests (demo names only). Not imported by the app. */
import type { Computer } from '../computers';
import type {
  HTTPProviderGroup, HTTPProviderInfo, HTTPProviderItem, HTTPProviderSnapshotResponse, HTTPProviderTarget, HTTPSettingsResponse,
} from '../generated/protocol';
import type { ComputerEntry } from './allSessions';
import { rollUp, type ProviderEntry } from './providers';

export const mac: Computer = { origin: 'https://mac.tailx.ts.net', host: 'mac.tailx.ts.net', label: 'mac', color: '#64D8E8', home: true };
export const vps: Computer = { origin: 'https://vps.tailx.ts.net', host: 'vps.tailx.ts.net', label: 'vps', color: '#F2B544', home: false };

export const info = (extra: Partial<HTTPProviderInfo> = {}): HTTPProviderInfo => ({
  id: 'herdr', name: 'herdr', state: 'ready', version: '0.8.2', enabled: true, notify: ['blocked', 'done'], canCreate: true,
  agentKinds: ['claude', 'codex'], ...extra,
});

export const target = (id: string, state: string, extra: Partial<HTTPProviderTarget> = {}): HTTPProviderTarget => ({
  id, label: '1', state, canOpen: true, ...extra,
});

export const item = (id: string, name: string, targets: HTTPProviderTarget[], extra: Partial<HTTPProviderItem> = {}): HTTPProviderItem => ({
  id, name, state: rollUp(targets.map((one) => one.state)),
  targets, ...(targets[0] === undefined ? {} : { openTarget: targets[0].id }), ...extra,
});

export const group = (id: string, name: string, items: HTTPProviderItem[], extra: Partial<HTTPProviderGroup> = {}): HTTPProviderGroup => ({
  id, name, canCreate: true, items,
  state: rollUp(items.map((one) => one.state)),
  ...extra,
});

/** shop (a blocked worktree, a finished one, main with two agents working) and two workspaces outside git named notes. */
export const shopGroups = (): HTTPProviderGroup[] => [
  group('g3f9a0c2b17e4', 'shop', [
    item('w1', 'main', [target('w1:p1.term_a1', 'working', { agent: 'claude', label: '1' }), target('w1:p2.term_a2', 'working', { agent: 'codex', label: '2' }), target('w1:p3.term_a3', 'none', { label: 'logs' })]),
    item('w8', 'silver-meadow', [target('w8:p1.term_b1', 'blocked', { agent: 'claude' })], { stateSince: '2026-10-04T09:51:00Z' }),
    item('w9', 'lucky-valley', [target('w9:p1.term_c1', 'done', { agent: 'claude' })]),
  ]),
  group('ws-w5', 'notes', [item('w5', 'notes', [target('w5:p1.term_d1', 'idle', { agent: 'claude' })])], { canCreate: false }),
  group('ws-w6', 'notes', [item('w6', 'notes', [target('w6:p1.term_e1', 'none', { label: 'zsh' })])], { canCreate: false }),
];

export const snapshot = (groups: HTTPProviderGroup[] = shopGroups(), extra: Partial<HTTPProviderSnapshotResponse> = {}): HTTPProviderSnapshotResponse => ({
  provider: 'herdr', state: 'ready', groups, ...extra,
});

export const providerEntry = (computer: Computer, status: ProviderEntry['status'], extra: Partial<HTTPProviderInfo> = {}): ProviderEntry => ({
  computer, info: info(extra), manage: true, key: `${computer.origin} provider:herdr`, status,
});

export const ready = (computer: Computer = mac, groups: HTTPProviderGroup[] = shopGroups()): ProviderEntry =>
  providerEntry(computer, { kind: 'ok', snapshot: snapshot(groups), at: 0 });

export const settingsWith = (extra: Partial<HTTPSettingsResponse> = {}): HTTPSettingsResponse => ({
  device: { id: '00000000-0000-4000-8000-000000000020', label: 'phone' }, projects: [], shellProfiles: [], hostBuild: '0.1.0',
  brokerGeneration: '00000000-0000-4000-8000-000000000002', protocolRevision: 4, minProtocolRevision: 1, providers: [info()], ...extra,
});

export const okEntry = (computer: Computer, extra: Partial<HTTPSettingsResponse> = {}): ComputerEntry => ({
  computer, status: { kind: 'ok', settings: settingsWith(extra), sessions: [], at: 0 },
});
