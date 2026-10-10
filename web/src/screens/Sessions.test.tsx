import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import type { Computer } from '../computers';
import { hosted, type ComputerEntry, type ComputerStatus } from './allSessions';
import { NewSession } from './NewSession';
import { waitingFirst } from './allSessions';
import { age, ComputerChips, controllerText, hostSummary, pairHint, problemDetail, keptSettings, problemText, HomeScreenHint, PushCard, SessionRow, Sessions } from './Sessions';

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
  const day = 86_400_000;
  it.each([
    [60_000 - 1, 'now'], [60_000, '1 min'],
    [3_600_000 - 1, '59 min'], [3_600_000, '1 h'],
    [day - 1, '23 h'], [day, 'yesterday'],
    [2 * day - 1, 'yesterday'], [2 * day, '2 d'],
    [7 * day - 1, '6 d'], [7 * day, '1 wk'],
    [30 * day - 1, '4 wk'], [30 * day, '1 mo'],
    [365 * day - 1, '12 mo'], [365 * day, '1 yr'],
    [366 * day, '1 yr'], [1095 * day, '3 yr'], [3650 * day, '10 yr'],
  ])('formats an elapsed %i ms as %s, including unit boundaries', (elapsed, expected) => {
    expect(age(new Date(now - elapsed).toISOString(), now)).toBe(expected);
  });
  it('keeps future dates at now and unknown or invalid dates out of the label', () => {
    expect(age(new Date(now + 3 * 365 * day).toISOString(), now)).toBe('now');
    for (const iso of [undefined, '', 'not-a-date']) expect(age(iso, now)).toBe('');
    for (const clock of [NaN, Infinity, -Infinity]) expect(age('2026-09-27T12:00:00Z', clock)).toBe('');
    expect(age('2026-09-27T14:00:00+02:00', now)).toBe('now');
  });
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
  it('renders an old session age in years without changing its metadata', () => {
    const then = new Date(Date.parse('2026-09-27T12:00:00Z') - 1095 * 86_400_000).toISOString();
    const page = row({ ...base, createdAt: then });
    expect(page).toContain('api · zsh · 3 yr');
    expect(page).not.toContain('1095 d');
  });
  it('shows controller, meta, activity dot and preview line', () => {
    const html = row({ ...base, controlled: true, controllerIsYou: true, viewerCount: 1, preview: '✻ Done.' }, true);
    expect(html).toContain('class="session-row"');
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
  it('marks a waiting row with an amber word, the question as text and an Answer label', () => {
    const html = row({ ...waiting, controlled: true, controllerIsYou: true, viewerCount: 1 });
    expect(html).toContain('class="session-row"');
    expect(html).not.toContain('tile');
    expect(html).toContain('<span class="status-pill amber"><span class="dot amber" aria-hidden="true"></span>Waiting · 2m</span>');
    expect(html).toContain(', waiting for you');
    expect(html).toContain('<span class="question">Do you want to &lt;b&gt;proceed&lt;/b&gt;?</span>');
    expect(html).not.toContain('<b>');
    expect(html).toContain('Answer ›');
    expect(html).not.toContain('hidden preview');
    expect(html).toContain('api · zsh · 2 h · You');
  });

  it('shows a working word with a live dot and keeps the preview line', () => {
    const html = row(working);
    expect(html).toContain('<span class="status-pill accent"><span class="dot accent" aria-hidden="true"></span>Working · 4m</span>');
    expect(html).toContain('<span class="preview">Thinking…</span>');
    expect(html).not.toContain('waiting for you');
  });

  it('keeps an idle shell neutral, and says your turn for idle after work with unseen output', () => {
    const shell: BrokerRPCSessionRecord = { ...base, id: '84444444-4444-4444-8444-444444444446', agent: { state: 'idle', stateSince: '2026-09-27T11:48:00Z' } };
    expect(row(shell, true)).not.toContain('status-pill');
    const idle: BrokerRPCSessionRecord = { ...shell, agent: { state: 'idle', stateSince: '2026-09-27T11:48:00Z', afterWork: true } };
    const html = row(idle, true);
    expect(html).toContain('<span class="status-pill green">Your turn · 12m</span>');
    expect(row(idle, false)).not.toContain('status-pill');
  });

  it('shows ended sessions as a muted word', () => {
    const html = row({ ...base, state: 'exited', exitCode: 2, endedAt: '2026-09-27T11:00:00Z' });
    expect(html).toContain('<span class="controller muted">Exited · 2</span>');
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

function renderSessions(entries: ComputerEntry[], extra: { offerPush?: boolean; onHomeScreenSteps?: () => void } = {}) {
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
      onHomeScreenSteps={extra.onHomeScreenSteps}
      onReload={async () => undefined}
      onRetry={() => Promise.resolve()}
      onFailure={() => ''}
    />,
  );
}

describe('Sessions first use', () => {
  it('explains how to add a project instead of offering a new session', () => {
    const html = renderSessions([entry(mac, ok(settings))]);
    expect(html).toContain('Add a project on your computer first:');
    expect(html).toContain('<code>pterm project add &lt;folder&gt;</code>');
    expect(html).toContain('<span>Check again</span>');
    expect(html).not.toContain('new session</button>');
  });

  it('lists waiting sessions first under a summary host line', () => {
    const html = renderSessions([entry(mac, ok(settings, [working, waiting]))]);
    expect(html).toContain('Your computer · 1 waiting · 1 working');
    expect(html.indexOf('status-pill amber')).toBeLessThan(html.indexOf('status-pill accent'));
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
    expect(html).not.toContain('Computers and menu');
    expect(html).not.toContain('drawer');
    expect(html).not.toContain('computer-group');
  });
});

describe('Sessions across computers', () => {
  const ready = { ...settings, projects: [{ id: base.projectId, name: 'site' }] };

  it('in All computers, lists each computer under its own header (label and state), in the computers\' order', () => {
    const html = renderSessions([entry(mac, ok(ready, [working])), entry(vps, ok(ready, [waiting], vps))]);
    expect(html).toContain('2 computers · 1 waiting · 1 working');
    expect(html).toContain('<h2 class="computer-group is-ok"><span class="dot" style="background:#64D8E8" aria-hidden="true"></span><bdi>mac</bdi><span class="computer-group-state"><span class="visually-hidden"> · </span>1 session · 1 working</span></h2>');
    expect(html.indexOf('<bdi>mac</bdi><span class="computer-group-state">')).toBeLessThan(html.indexOf('status-pill accent'));
    expect(html.indexOf('status-pill accent')).toBeLessThan(html.indexOf('<bdi>vps</bdi><span class="computer-group-state">'));
    expect(html.indexOf('<bdi>vps</bdi><span class="computer-group-state">')).toBeLessThan(html.indexOf('status-pill amber'));
    expect(html).not.toContain('computer-tag');
    expect(html).toContain('new session on…</button>');
  });

  it('with a computer chosen, shows only its sessions, no headers, and its name on the pill and the status line', () => {
    const html = renderToStaticMarkup(
      <Sessions
        entries={[entry(mac, ok(ready, [working])), entry(vps, ok(ready, [waiting], vps))]} sessions={waitingFirst([hosted(working, mac), hosted(waiting, vps)])}
        computer={vps.host} offerPush={false} onOpen={() => undefined} onNew={() => undefined} onWall={() => undefined} onSettings={() => undefined}
        onReload={async () => undefined} onRetry={() => Promise.resolve()} onFailure={() => ''}
      />,
    );
    expect(html).toContain('vps · 1 waiting');
    expect(html).toContain('status-pill amber');
    expect(html).not.toContain('status-pill accent');
    expect(html).not.toContain('computer-group');
    expect(html).toContain('<button type="button" class="drawer-pill" aria-haspopup="dialog" aria-expanded="false"><span class="dot" style="background:#9B8CFF" aria-hidden="true"></span><bdi>vps</bdi>');
  });

  it('with a chosen computer that does not answer, says its state and an amber dot; another computer\'s trouble does not change the dot', () => {
    const page = (computer: string) => renderToStaticMarkup(
      <Sessions
        entries={[entry(mac, ok(ready, [working])), entry(vps, { kind: 'unreachable' })]} sessions={[hosted(working, mac)]}
        computer={computer} offerPush={false} onOpen={() => undefined} onNew={() => undefined} onWall={() => undefined} onSettings={() => undefined}
        onReload={async () => undefined} onRetry={() => Promise.resolve()} onFailure={() => ''}
      />,
    );
    expect(page(vps.host)).toContain('<span class="dot amber" aria-hidden="true"></span>vps · can&#x27;t reach</p>');
    expect(page(mac.host)).toContain('<span class="dot accent" aria-hidden="true"></span>mac · 1 working</p>');
  });

  it('has the drawer button and the All computers pill, no computer chips; Wall and Settings stay in the bar', () => {
    const html = renderSessions([entry(mac, ok(ready, [working])), entry(vps, ok(ready, [], vps))]);
    expect(html).toContain('aria-label="Computers and menu"');
    expect(html).toContain('All computers<span class="drawer-caret" aria-hidden="true">▾</span>');
    expect(html).not.toContain('aria-label="Computers"');
    expect(html).toContain('aria-label="Wall"');
    expect(html).toContain('aria-label="Settings"');
  });

  it('keeps sessions with the same id on two computers apart', () => {
    const html = renderSessions([entry(mac, ok(ready, [base])), entry(vps, ok(ready, [base], vps))]);
    expect(html.match(/class="session-row/gu)).toHaveLength(2);
  });

  it('offers one chip per computer to pick from (New session), none with one computer', () => {
    expect(renderToStaticMarkup(<ComputerChips computers={[mac]} chosen={undefined} onChoose={() => undefined} />)).toBe('');
    const pick = renderToStaticMarkup(
      <ComputerChips computers={[mac, vps]} chosen="mac.tailx.ts.net" disabled={(host) => host === vps.host} onChoose={() => undefined} />,
    );
    expect(pick).not.toContain('All');
    expect(pick).toContain('aria-pressed="true"><span class="dot" style="background:#64D8E8" aria-hidden="true"></span>mac</button>');
    expect(pick).toContain('aria-pressed="false" disabled=""><span class="dot" style="background:#9B8CFF" aria-hidden="true"></span>vps</button>');
  });

  it('says when a computer that did not answer was last seen, and asks (never claims) why only after a network error', () => {
    const lastSeen = NOW - 3 * 3_600_000;
    expect(problemDetail(entry(vps, { kind: 'unreachable', lastSeen, network: true }), NOW)).toBe('Last seen 3 h ago. Is it on, and in your tailnet?');
    expect(problemDetail(entry(vps, { kind: 'unreachable', network: true }), NOW)).toBe('Is it on, and in your tailnet?');
    // An HTTP error or an unreadable reply: something answered, so no question about power or the tailnet.
    expect(problemDetail(entry(vps, { kind: 'unreachable', lastSeen, network: false }), NOW)).toBe('Last seen 3 h ago.');
    expect(problemDetail(entry(vps, { kind: 'unreachable' }), NOW)).toBeUndefined();
    expect(problemDetail(entry(vps, { kind: 'pair' }), NOW)).toBeUndefined();
    expect(problemText(entry(vps, { kind: 'connecting' }), NOW)).toBe('Connecting to vps…');
    expect(problemDetail(entry(vps, { kind: 'connecting' }), NOW)).toBe('Just added. Its sessions show in a moment.');
    expect(problemText(entry(vps, { kind: 'blocked' }), NOW)).toBe("vps · can't reach");
    expect(problemDetail(entry(vps, { kind: 'blocked' }), NOW)).toBe('This app page is out of date. Close the app and open it again.');
    const html = renderSessions([entry(mac, ok(ready, [working])), entry(vps, { kind: 'unreachable', lastSeen, network: true })]);
    expect(html).toMatch(/<p class="notice-title">vps · can&#x27;t reach<\/p><div class="notice-body">Last seen [^<]+ ago\. Is it on, and in your tailnet\?<\/div>/u);
  });

  it('shows one line per computer that did not answer, and its sessions are gone', () => {
    expect(problemText(entry(vps, { kind: 'unreachable' }), NOW)).toBe("vps · can't reach");
    expect(problemText(entry(vps, { kind: 'unreachable', lastSeen: NOW - 3 * 3_600_000 }), NOW)).toBe("vps · can't reach");
    expect(problemText(entry(vps, { kind: 'pair' }), NOW)).toBe(`vps · pair again · ${pairHint('vps')}`);
    expect(pairHint('Build box', 'mac')).toBe('Run pterm pair --via mac on Build box');
    expect(problemText(entry(vps, { kind: 'outdated', message: 'Update Termivoa on vps' }), NOW)).toBe('Update Termivoa on vps');
    expect(problemText(entry(vps, { kind: 'loading' }), NOW)).toBeUndefined();
    expect(problemText(entry(vps, ok(settings)), NOW)).toBeUndefined();

    const html = renderSessions([entry(mac, ok(ready, [working])), entry(vps, { kind: 'unreachable' })]);
    expect(html).toContain('<p class="notice-title">vps · can&#x27;t reach</p>');
    // Retry has a busy label, so a tap shows that it works.
    expect(html).toMatch(/<div class="notice-action"><button type="button" class="link"><span class="btn-swap"><span>Retry<\/span><span class="off" aria-hidden="true">.*?Retrying…<\/span><\/span><\/button><\/div>/u);
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
    expect(html).toContain('Save as preset');
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
    expect(html).not.toContain('<svg');
  });

  it('is not in the first render of Sessions; it appears only after push support is checked', () => {
    expect(renderSessions([entry(mac, ok(settings))], { offerPush: true })).not.toContain('Get a buzz');
  });
});

describe('HomeScreenHint', () => {
  afterEach(() => vi.unstubAllGlobals());
  const TITLE = 'For notifications, add Termivoa to your Home Screen';
  const sessionsOn = (userAgent: string, standalone = false) => {
    vi.stubGlobal('navigator', { userAgent, maxTouchPoints: 0, standalone });
    return renderSessions([entry(mac, ok(settings))], { offerPush: true, onHomeScreenSteps: () => undefined });
  };

  it('is one line with See how and Not now', () => {
    const html = renderToStaticMarkup(<HomeScreenHint onSteps={() => undefined} onClose={() => undefined} />);
    expect(html).toContain(`>${TITLE}</h2><div class="push-card-actions"><button type="button" class="secondary">See how</button><button type="button" class="link">Not now</button>`);
  });

  it('shows on Sessions in an iPhone Safari tab, never in the Home Screen app or another browser', () => {
    expect(sessionsOn('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1')).toContain(TITLE);
    expect(sessionsOn('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1', true)).not.toContain(TITLE);
    expect(sessionsOn('Mozilla/5.0 (Linux; Android 15) Chrome/130.0 Mobile Safari/537.36')).not.toContain(TITLE);
  });
});

describe('New workspace while its computer does not answer (keptSettings)', () => {
  it('keeps the last good settings, so the open sheet stays with its own messages; a new answer replaces them', () => {
    const newer = { ...settings, hostBuild: 'pterm/newer' };
    expect(keptSettings(undefined, { kind: 'ok', settings, sessions: [], at: 0 })).toBe(settings);
    expect(keptSettings(settings, { kind: 'unreachable' })).toBe(settings);
    expect(keptSettings(settings, { kind: 'pair' })).toBe(settings);
    expect(keptSettings(settings, undefined)).toBe(settings);
    expect(keptSettings(settings, { kind: 'ok', settings: newer, sessions: [], at: 1 })).toBe(newer);
    expect(keptSettings(undefined, { kind: 'loading' })).toBeUndefined();
  });
});
