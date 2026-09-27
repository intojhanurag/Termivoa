import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { BrokerRPCSessionRecord, HTTPSettingsResponse } from '../generated/protocol';
import { PresenceFacts, Sessions } from './Sessions';

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

const render = (session: BrokerRPCSessionRecord) => renderToStaticMarkup(<dl><PresenceFacts session={session} /></dl>);

describe('PresenceFacts', () => {
  it('names who controls the session and how many viewers are attached', () => {
    expect(render({ ...base, controlled: true, controllerIsYou: true, viewerCount: 1 }))
      .toBe('<dl><dt>Control</dt><dd>Controlled by this device</dd><dt>Viewers</dt><dd>1 viewer</dd></dl>');
    expect(render({ ...base, controlled: true, controllerIsYou: false, viewerCount: 3 }))
      .toContain('<dd>Controlled by another device</dd><dt>Viewers</dt><dd>3 viewers</dd>');
    expect(render({ ...base, controlled: false, controllerIsYou: false, viewerCount: 0 }))
      .toContain('<dd>No controller</dd><dt>Viewers</dt><dd>No viewers</dd>');
  });

  it('renders nothing when the broker has no live presence', () => {
    expect(render({ ...base, state: 'exited', endedAt: '2026-09-27T10:01:00Z' })).toBe('<dl></dl>');
  });
});

describe('Sessions first use', () => {
  const settings: HTTPSettingsResponse = {
    device: { id: '00000000-0000-4000-8000-000000000020', label: 'iPhone' },
    brokerGeneration: '00000000-0000-4000-8000-000000000030',
    hostBuild: 'pterm/test',
    projects: [],
    shellProfiles: [{ id: base.shellProfileId, name: 'zsh' }],
  };
  const renderSessions = (value: HTTPSettingsResponse) => renderToStaticMarkup(
    <Sessions
      settings={value}
      onOpen={() => undefined}
      onSettings={() => undefined}
      onRefreshSettings={async () => undefined}
      onFailure={() => ''}
    />,
  );

  it('explains how to add a project instead of showing an empty picker', () => {
    const html = renderSessions(settings);
    expect(html).toContain('Add a project on your computer first:');
    expect(html).toContain('<code>pterm project add &lt;folder&gt;</code>');
    expect(html).toContain('>Check again</button>');
    expect(html).not.toContain('<select');
    expect(html).not.toContain('Start session');
  });

  it('points to pterm doctor when no shell profile exists', () => {
    const html = renderSessions({ ...settings, projects: [{ id: base.projectId, name: 'site' }], shellProfiles: [] });
    expect(html).toContain('<code>pterm doctor</code>');
    expect(html).not.toContain('<select');
  });

  it('shows the pickers and Start session once a project and shell exist', () => {
    const html = renderSessions({ ...settings, projects: [{ id: base.projectId, name: 'site' }] });
    expect(html).toContain('<select id="project"');
    expect(html).toContain('>Start session</button>');
    expect(html).not.toContain('Check again');
  });
});
