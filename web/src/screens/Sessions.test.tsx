import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { PresenceFacts } from './Sessions';

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
      .toBe('<dl><dt>Control</dt><dd>Controlled here</dd><dt>Viewers</dt><dd>1 viewer</dd></dl>');
    expect(render({ ...base, controlled: true, controllerIsYou: false, viewerCount: 3 }))
      .toContain('<dd>Controlled elsewhere</dd><dt>Viewers</dt><dd>3 viewers</dd>');
    expect(render({ ...base, controlled: false, controllerIsYou: false, viewerCount: 0 }))
      .toContain('<dd>No controller</dd><dt>Viewers</dt><dd>No viewers</dd>');
  });

  it('renders nothing when the broker has no live presence', () => {
    expect(render({ ...base, state: 'exited', endedAt: '2026-09-27T10:01:00Z' })).toBe('<dl></dl>');
  });
});
