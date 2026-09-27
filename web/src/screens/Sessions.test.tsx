import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import type { SessionList } from './hooks';
import { NewSession } from './NewSession';
import { age, controllerText, SessionRow, Sessions } from './Sessions';

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

const settings: HTTPSettingsResponse = {
  device: { id: '00000000-0000-4000-8000-000000000020', label: 'iPhone' },
  brokerGeneration: '00000000-0000-4000-8000-000000000030',
  hostBuild: 'pterm/test',
  projects: [],
  shellProfiles: [{ id: base.shellProfileId, name: 'zsh' }],
};
const list: SessionList = { sessions: [], reachable: true, reload: async () => undefined };

describe('Sessions first use', () => {
  const renderSessions = (value: HTTPSettingsResponse) => renderToStaticMarkup(
    <Sessions
      settings={value}
      list={list}
      onOpen={() => undefined}
      onNew={() => undefined}
      onSettings={() => undefined}
      onRefreshSettings={async () => undefined}
      onFailure={() => ''}
    />,
  );

  it('explains how to add a project instead of offering a new session', () => {
    const html = renderSessions(settings);
    expect(html).toContain('Add a project on your computer first:');
    expect(html).toContain('<code>pterm project add &lt;folder&gt;</code>');
    expect(html).toContain('>Check again</button>');
    expect(html).not.toContain('new session</button>');
  });

  it('offers ❯ new session once a project and shell exist', () => {
    const html = renderSessions({ ...settings, projects: [{ id: base.projectId, name: 'site' }] });
    expect(html).toContain('new session</button>');
    expect(html).toContain('aria-label="Settings"');
    expect(html).not.toContain('Check again');
  });
});

describe('New session', () => {
  const renderNew = (value: HTTPSettingsResponse) => renderToStaticMarkup(
    <NewSession
      settings={value}
      onCancel={() => undefined}
      onStarted={() => undefined}
      onRefreshSettings={async () => undefined}
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
    expect(html).toContain('start session</button>');
    expect(html).toContain('save as preset');
    expect(html).not.toContain('Launch');
  });
});
