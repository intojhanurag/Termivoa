import { httpValidators } from 'virtual:http-validators';
import { describe, expect, it, vi } from 'vitest';
import { InvalidResponseError, parseHttpBody } from './httpBody';

const brokerGeneration = '00000000-0000-4000-8000-000000000002';

describe('parseHttpBody', () => {
  const session = (id: string, extra: object = {}) => ({
    id, generation: brokerGeneration, title: 'app', projectId: brokerGeneration, shellProfileId: brokerGeneration,
    state: 'running', columns: 80, rows: 24, createdAt: '2026-09-27T10:00:00.000Z', updatedAt: '2026-09-27T10:00:00.000Z',
    ...extra,
  });
  const id = (n: number) => `00000000-0000-4000-8000-00000000000${n}`;

  it('keeps known fields and drops unknown ones', () => {
    const body = parseHttpBody('sessionListResponse', { sessions: [session(id(1), { glow: true })], page: 2 });
    expect(body).toEqual({ sessions: [session(id(1))] });
  });

  it('skips one bad list item, including one with a new enum value, and keeps the rest', () => {
    const body = parseHttpBody('sessionListResponse', {
      sessions: [session(id(1)), session(id(2), { state: 'paused' }), session(id(3), { columns: 'wide' }), session(id(4))],
    });
    expect(body.sessions.map((item) => item.id)).toEqual([id(1), id(4)]);
    const computers = parseHttpBody('computerListResponse', {
      computers: [{ origin: 'https://vps.tailx.ts.net', label: 'vps' }, { origin: 'http://evil.tailx.ts.net', label: 'x' }],
    });
    expect(computers.computers).toEqual([{ origin: 'https://vps.tailx.ts.net', label: 'vps' }]);
  });

  it('validates each session once when the list is valid, and item by item only after a failure', () => {
    const perItem = vi.spyOn(httpValidators, 'sessionRecord');
    parseHttpBody('sessionListResponse', { sessions: [session(id(1)), session(id(2)), session(id(3))] });
    expect(perItem).not.toHaveBeenCalled();
    const body = parseHttpBody('sessionListResponse', { sessions: [session(id(1)), session(id(2), { state: 'teleporting' })] });
    expect(body.sessions.map((item) => item.id)).toEqual([id(1)]);
    expect(perItem).toHaveBeenCalledTimes(2);
    perItem.mockRestore();
  });

  it('rejects wrong types outside lists', () => {
    expect(() => parseHttpBody('sessionListResponse', { sessions: 'all' })).toThrow(InvalidResponseError);
    expect(() => parseHttpBody('pairPollResponse', { status: 'maybe' })).toThrow(InvalidResponseError);
    expect(() => parseHttpBody('uploadResponse', null)).toThrow(InvalidResponseError);
  });
});
