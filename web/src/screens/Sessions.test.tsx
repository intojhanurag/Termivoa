import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import type { Computer } from '../computers';
import { hosted, type ComputerEntry, type ComputerStatus } from './allSessions';
import { NewSession } from './NewSession';
import { waitingFirst } from './allSessions';
import { age, ComputerChips, controllerText, hostSummary, pairHint, problemText, PushCard, SessionRow, Sessions } from './Sessions';

const base: BrokerRPCSessionRecord = {
  id: '84444444-4444-4444-8444-444444444444',
  generation: '85555555-5555-4555-8555-555555555555',
  title: 'Shell',
  projectId: '81111111-1111-4111-8111-111111111111',
  shellProfileId: '82222222-2222-4222-8222-222222222222',
  state: 'running',
  columns: 80,
  rows: 24,
  createdAt: '2026-09-27T10:00:00Z',
  updatedAt: '2026-09-27T10:00:00Z',
};

const row = (session: BrokerRPCSessionRecord, fresh = false) => renderToStaticMarkup(
  <ul>
    <SessionRow session={session} project="api" shell="zsh" fresh={fresh} now={Date.parse('2026-09-27T12:00:00Z')} onOpen={() => undefined} />
  </ul>,
);

describe('controllerText', () => {
  it('names who controls the session: you, another device, or no one', () => {
    expect(controllerText({ ...base, controlled: true, controllerIsYou: true, viewerCount: 1 })).toEqual({ text: 'You', tone: 'accent' });
    expect(controllerText({ ...base, controlled: true, controllerIsYou: false, viewerCount: 3 }))
      .toEqual({ text: 'Other device', tone: 'amber' });
    expect(controllerText({ ...base, controlled: false, controllerIsYou: false, viewerCount: 0 })).toEqual({ text: 'No one', tone: 'muted' });
  });

  it('says nothing about control when the broker has no live presence, and shows exits', () => {
    expect(controllerText(base)).toBeUndefined();
    expect(controllerText({ ...base, state: 'exited', exitCode: 0, endedAt: '2026-09-27T10:01:00Z' }))
      .toEqual({ text: 'Exited · 0', tone: 'muted' });
  });
});

describe('age', () => {
  const now = Date.parse('2026-09-27T12:00:00Z');
  it('shortens elapsed time', () => {
    expect(age('2026-09-27T11:59:40Z', now)).toBe('now');
    expect(age('2026-09-27T11:25:00Z', now)).toBe('35 min');
    expect(age('2026-09-27T10:00:00Z', now)).toBe('2 h');
    expect(age('2026-09-26T10:00:00Z', now)).toBe('yesterday');
    expect(age('2026-09-24T10:00:00Z', now)).toBe('3 d');
    expect(age(undefined, now)).toBe('');
  });
});

describe('SessionRow', () => {
  it('shows controller, meta, activity dot and preview line', () => {
    const html = row({ ...base, controlled: true, controllerIsYou: true, viewerCount: 1, preview: '✻ Done.' }, true);
    expect(html).toContain('class="session-row you"');
    expect(html).toContain('<span class="controller accent">You</span>');
    expect(html).toContain('api · zsh · 2 h');
    expect(html).toContain('<span class="preview">✻ Done.</span>');
    expect(html).toContain(', new output');
  });

  it('shows the cwd basename as the title while the meta line keeps the project name', () => {
    const html = row({ ...base, title: 'Home', cwd: '/Users/me/src/app' });
    expect(html).toContain('<span class="row-title">app');
    expect(html).toContain('api · zsh');
    expect(html).not.toContain('>Home<');
  });

  it('shows a worktree badge before the meta line only for a worktree session', () => {
    const html = row({ ...base, cwd: '/Users/me/termivoa/worktrees/app/brave-otter', worktree: { name: 'brave-otter', repo: 'app' } });
    expect(html).toContain('<span class="worktree-badge" title="Worktree brave-otter of app"><span aria-hidden="true">⎇ </span>'
      + '<span class="visually-hidden">worktree </span>brave-otter</span> · api · zsh');
    expect(row(base)).not.toContain('worktree');
  });

  it('keeps a custom title over the cwd basename', () => {
    const html = row({ ...base, title: 'My shell', titleIsCustom: true, cwd: '/Users/me/src/app' });
    expect(html).toContain('<span class="row-title">My shell');
  });

  it('renders a hostile cwd as text only, never markup', () => {
    const html = row({ ...base, title: 'Home', cwd: '/Users/me/<img src=x onerror=alert(1)>' });
    expect(html).not.toContain('<img');
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;');
  });

  it('renders preview and title strictly as text, never markup or links', () => {
    const html = row({
      ...base,
      title: '<b>t</b>',
      preview: '<img src=x onerror=alert(1)> <a href="https://evil.invalid">x</a> https://evil.invalid',
    });
    expect(html).not.toContain('<img');
    expect(html).not.toContain('<a ');
    expect(html).not.toContain('<b>');
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt; &lt;a href=&quot;https://evil.invalid&quot;&gt;x&lt;/a&gt;');
  });

  it('fades exited sessions and does not open them', () => {
    const html = row({ ...base, state: 'exited', exitCode: 0, endedAt: '2026-09-26T10:00:00Z', preview: 'bye' });
    expect(html).toMatch(/<button type="button" class="session-row ended" disabled="">/u);
    expect(html).toContain('Exited · 0');
    expect(html).toContain('yesterday');
    expect(html).not.toContain('class="preview"');
  });
});

const waiting: BrokerRPCSessionRecord = {
  ...base,
  title: 'claude',
  agent: {
    state: 'waiting',
    stateSince: '2026-09-27T11:58:00Z',
    question: { id: 'q1', text: 'Do you want to <b>proceed</b>?', options: [{ label: 'Yes' }, { label: 'No' }] },
  },
  preview: 'hidden preview',
};
const working: BrokerRPCSessionRecord = { ...base, id: '84444444-4444-4444-8444-444444444445', agent: { state: 'working', stateSince: '2026-09-27T11:56:00Z' }, preview: 'Thinking…' };

describe('agent status badges', () => {
  it('highlights a waiting row with a pill, a ? tile, the question as text and an Answer label', () => {
    const html = row({ ...waiting, controlled: true, controllerIsYou: true, viewerCount: 1 });
    expect(html).toContain('class="session-row you asking"');
    expect(html).toContain('<span class="tile amber" aria-hidden="true">?</span>');
    expect(html).toContain('<span class="status-pill amber"><span class="dot amber" aria-hidden="true"></span>Waiting<span class="pill-time"> · 2m</span></span>');
    expect(html).toContain(', waiting for you');
    expect(html).toContain('<span class="question">Do you want to &lt;b&gt;proceed&lt;/b&gt;?</span>');
    expect(html).not.toContain('<b>');
    expect(html).toContain('Answer ›');
    expect(html).not.toContain('hidden preview');
    expect(html).toContain('api · zsh · 2 h · You');
  });

  it('shows a working pill with a spinner and keeps the preview line', () => {
    const html = row(working);
    expect(html).toContain('<span class="spinner"></span>');
    expect(html).toContain('Working<span class="pill-time"> · 4m</span>');
    expect(html).toContain('<span class="preview">Thinking…</span>');
    expect(html).not.toContain('waiting for you');
  });

  it('keeps an idle shell neutral, and says your turn for idle after work with unseen output', () => {
    const shell: BrokerRPCSessionRecord = { ...base, id: '84444444-4444-4444-8444-444444444446', agent: { state: 'idle', stateSince: '2026-09-27T11:48:00Z' } };
    expect(row(shell, true)).not.toContain('status-pill');
    const idle: BrokerRPCSessionRecord = { ...shell, agent: { state: 'idle', stateSince: '2026-09-27T11:48:00Z', afterWork: true } };
    const html = row(idle, true);
    expect(html).toContain('<span class="status-pill green">Your turn<span class="pill-time"> · 12m</span></span>');
    expect(html).toContain('<span class="tile green" aria-hidden="true"><svg');
    expect(row(idle, false)).not.toContain('status-pill');
  });

  it('shows ended sessions as muted text, not a tinted pill', () => {
    const html = row({ ...base, state: 'exited', exitCode: 2, endedAt: '2026-09-27T11:00:00Z' });
    expect(html).toContain('<span class="status-pill plain muted">Exited · 2</span>');
  });

  it('sorts waiting sessions first and keeps the rest in order', () => {
    const idle = { ...base, id: 'a' };
    const other = { ...waiting, id: 'b' };
    expect(waitingFirst([idle, working, waiting, other]).map((item) => item.id)).toEqual([waiting.id, 'b', 'a', working.id]);
  });

  it('summarizes waiting and working counts on the host line', () => {
    expect(hostSummary([waiting, working, { ...working, id: 'c' }, base])).toBe('Your computer · 1 waiting · 2 working');
    expect(hostSummary([working])).toBe('Your computer · 1 working');
    expect(hostSummary([base])).toBe('Your computer · connected');
    expect(hostSummary(undefined)).toBe('Your computer · connected');
  });
});

const settings: HTTPSettingsResponse = {
  device: { id: '00000000-0000-4000-8000-000000000020', label: 'iPhone' },
  brokerGeneration: '00000000-0000-4000-8000-000000000030',
  hostBuild: 'pterm/test',
  projects: [],
  shellProfiles: [{ id: base.shellProfileId, name: 'zsh' }],
  protocolRevision: 1,
  minProtocolRevision: 1,
};
const mac: Computer = { origin: 'https://mac.tailx.ts.net', host: 'mac.tailx.ts.net', label: 'mac', color: '#64D8E8', home: true };
const vps: Computer = { origin: 'https://vps.tailx.ts.net', host: 'vps.tailx.ts.net', label: 'vps', color: '#9B8CFF', home: false };
const NOW = Date.parse('2026-09-27T12:00:00Z');
const ok = (value: HTTPSettingsResponse, sessions: BrokerRPCSessionRecord[] = [], computer = mac): ComputerStatus =>
  ({ kind: 'ok', settings: value, sessions: sessions.map((item) => hosted(item, computer)), at: NOW });
const entry = (computer: Computer, status: ComputerStatus): ComputerEntry => ({ computer, status });

function renderSessions(entries: ComputerEntry[], extra: { offerPush?: boolean } = {}) {
  const all = entries.flatMap((item) => (item.status.kind === 'ok' ? item.status.sessions : []));
  return renderToStaticMarkup(
    <Sessions
      entries={entries}
      sessions={waitingFirst(all)}
      offerPush={extra.offerPush ?? false}
      onOpen={() => undefined}
      onNew={() => undefined}
      onWall={() => undefined}
      onSettings={() => undefined}
      onReload={async () => undefined}
      onRetry={() => undefined}
      onFailure={() => ''}
    />,
  );
}

describe('Sessions first use', () => {
  it('explains how to add a project instead of offering a new session', () => {
    const html = renderSessions([entry(mac, ok(settings))]);
    expect(html).toContain('Add a project on your computer first:');
    expect(html).toContain('<code>pterm project add &lt;folder&gt;</code>');
    expect(html).toContain('>Check again</button>');
    expect(html).not.toContain('new session</button>');
  });

  it('lists waiting sessions first under a summary host line', () => {
    const html = renderSessions([entry(mac, ok(settings, [working, waiting]))]);
    expect(html).toContain('Your computer · 1 waiting · 1 working');
    expect(html.indexOf('>Waiting<')).toBeLessThan(html.indexOf('>Working<'));
  });

  it('offers ❯ new session once a project and shell exist', () => {
    const html = renderSessions([entry(mac, ok({ ...settings, projects: [{ id: base.projectId, name: 'site' }] }))]);
    expect(html).toContain('new session</button>');
    expect(html).toContain('aria-label="Settings"');
    expect(html).toContain('<button type="button" class="round" aria-label="Wall">');
    expect(html).not.toContain('Check again');
  });

  it('shows no chips and no computer labels with one computer', () => {
    const html = renderSessions([entry(mac, ok(settings, [working]))]);
    expect(html).not.toContain('class="chips"');
    expect(html).not.toContain('computer-tag');
  });
});

describe('Sessions across computers', () => {
  const ready = { ...settings, projects: [{ id: base.projectId, name: 'site' }] };

  it('merges every computer, waiting first, each row with its colour dot and label', () => {
    const html = renderSessions([entry(mac, ok(ready, [working])), entry(vps, ok(ready, [waiting], vps))]);
    expect(html).toContain('2 computers · 1 waiting · 1 working');
    expect(html.indexOf('>Waiting<')).toBeLessThan(html.indexOf('>Working<'));
    expect(html).toContain('<span class="computer-tag"><span class="dot" style="background:#9B8CFF" aria-hidden="true"></span>vps</span>');
    expect(html).toContain('<span class="computer-tag"><span class="dot" style="background:#64D8E8" aria-hidden="true"></span>mac</span>');
    expect(html).toContain('new session on…</button>');
  });

  it('keeps sessions with the same id on two computers apart', () => {
    const html = renderSessions([entry(mac, ok(ready, [base])), entry(vps, ok(ready, [base], vps))]);
    expect(html.match(/class="session-row/gu)).toHaveLength(2);
  });

  it('offers All plus one chip per computer', () => {
    const html = renderToStaticMarkup(<ComputerChips computers={[mac, vps]} chosen={undefined} onChoose={() => undefined} />);
    expect(html).toContain('<button type="button" class="chip" aria-pressed="true">All</button>');
    expect(html).toContain('aria-pressed="false"><span class="dot" style="background:#9B8CFF" aria-hidden="true"></span>vps</button>');
    expect(renderToStaticMarkup(<ComputerChips computers={[mac]} chosen={undefined} onChoose={() => undefined} />)).toBe('');
    const pick = renderToStaticMarkup(
      <ComputerChips computers={[mac, vps]} chosen="mac.tailx.ts.net" all={false} disabled={(host) => host === vps.host} onChoose={() => undefined} />,
    );
    expect(pick).not.toContain('All');
    expect(pick).toContain('aria-pressed="false" disabled="">');
  });

  it('shows one line per computer that did not answer, and its sessions are gone', () => {
    expect(problemText(entry(vps, { kind: 'unreachable' }), NOW)).toBe("vps · can't reach");
    expect(problemText(entry(vps, { kind: 'unreachable', lastSeen: NOW - 3 * 3_600_000 }), NOW)).toBe("vps · can't reach · last seen 3 h ago");
    expect(problemText(entry(vps, { kind: 'pair' }), NOW)).toBe(`vps · pair again · ${pairHint('vps')}`);
    expect(pairHint('Build box', 'mac')).toBe('Run pterm pair --via mac on Build box');
    expect(problemText(entry(vps, { kind: 'outdated', message: 'Update Termivoa on vps' }), NOW)).toBe('Update Termivoa on vps');
    expect(problemText(entry(vps, { kind: 'loading' }), NOW)).toBeUndefined();
    expect(problemText(entry(vps, ok(settings)), NOW)).toBeUndefined();

    const html = renderSessions([entry(mac, ok(ready, [working])), entry(vps, { kind: 'unreachable' })]);
    expect(html).toContain('<p class="notice-title">vps · can&#x27;t reach</p>');
    expect(html).toMatch(/<button type="button" class="secondary compact"><svg[^]*?<\/svg>Retry<\/button>/u);
    const pair = renderSessions([entry(mac, ok(ready, [working])), entry(vps, { kind: 'pair' })]);
    expect(pair).toContain('vps · pair again');
    expect(pair).not.toContain('Retry');
  });

  it('keeps the one-computer screen as before: no computer rows, "not reachable" in the header', () => {
    const html = renderSessions([entry(mac, { kind: 'unreachable' })]);
    expect(html).toContain('Your computer · not reachable, retrying');
    expect(html).not.toContain('computer-problem');
    expect(html).not.toContain('can&#x27;t reach');
  });

  it('shows the other computers while one is still loading', () => {
    const html = renderSessions([entry(mac, { kind: 'loading' }), entry(vps, ok(ready, [working], vps))]);
    expect(html).not.toContain('Loading…');
    expect(html).toContain('class="session-row');
    expect(renderSessions([entry(mac, { kind: 'loading' })])).toContain('Loading…');
  });
});

describe('New session', () => {
  const renderNew = (value: HTTPSettingsResponse, more: ComputerEntry[] = []) => renderToStaticMarkup(
    <NewSession
      entries={[entry(mac, ok(value)), ...more]}
      onCancel={() => undefined}
      onStarted={() => undefined}
      onReload={async () => undefined}
      onFailure={() => ''}
    />,
  );

  it('points to pterm doctor when no shell profile exists', () => {
    const html = renderNew({ ...settings, projects: [{ id: base.projectId, name: 'site' }], shellProfiles: [] });
    expect(html).toContain('<code>pterm doctor</code>');
    expect(html).not.toContain('<select');
  });

  it('shows project, shell (login shell first) and run first, then ❯ start session', () => {
    const html = renderNew({
      ...settings,
      projects: [{ id: base.projectId, name: 'site' }],
      shellProfiles: [{ id: 'zsh', name: 'zsh' }, { id: 'bash', name: 'bash' }],
    });
    expect(html).toContain('<select id="project"');
    expect(html).toMatch(/<select id="shell"><option value="zsh" selected="">zsh<\/option>/u);
    expect(html).toContain('<input id="run-first"');
    expect(html).toContain('<span>start session</span><span class="off" aria-hidden="true">starting…</span>');
    expect(html).toContain('save as preset');
    expect(html).not.toContain('Launch');
    expect(html).not.toContain('computer-heading');
  });

  it('picks the computer first; one that cannot be reached is disabled', () => {
    const html = renderNew(
      { ...settings, projects: [{ id: base.projectId, name: 'site' }] },
      [entry(vps, { kind: 'unreachable' })],
    );
    expect(html).toContain('<h2 id="computer-heading" class="caps">Computer</h2>');
    expect(html).toContain('Runs on mac');
    expect(html).toMatch(/aria-pressed="false" disabled=""><span class="dot" style="background:#9B8CFF" aria-hidden="true"><\/span>vps<\/button>/u);
    expect(html).toContain("vps · can&#x27;t reach");
    expect(html.indexOf('computer-heading')).toBeLessThan(html.indexOf('<select id="project"'));
  });
});

describe('PushCard', () => {
  it('offers push with a short privacy note, Turn on and Not now', () => {
    const html = renderToStaticMarkup(<PushCard onClose={() => undefined} onFailure={() => ''} />);
    expect(html).toContain('Get a buzz when an agent needs you');
    expect(html).toContain('Only the session name and status. Never terminal text.');
    expect(html).toContain('<button type="button" class="secondary">Turn on</button>');
    expect(html).toContain('<button type="button" class="link">Not now</button>');
    expect(html).toContain('<svg');
  });

  it('is not in the first render of Sessions; it appears only after push support is checked', () => {
    expect(renderSessions([entry(mac, ok(settings))], { offerPush: true })).not.toContain('Get a buzz');
  });
});
