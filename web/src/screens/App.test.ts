import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api/http';
import type { BrokerRPCSessionRecord } from '../generated/protocol';
import { offlineRetry, pathFor, startLike, titleFor } from './App';

describe('offlineRetry', () => {
  it('keeps "connecting" for the first failure, then shows offline', () => {
    expect(offlineRetry(1).offline).toBe(false);
    expect(offlineRetry(2).offline).toBe(true);
    expect(offlineRetry(9).offline).toBe(true);
  });

  it('backs off 1, 2, 4, 8 seconds, then retries every 10', () => {
    expect([1, 2, 3, 4, 5, 6, 50].map((failures) => offlineRetry(failures).retryIn)).toEqual([1, 2, 4, 8, 10, 10, 10]);
  });
});

describe('wall route', () => {
  it('has its own address and tab title, so browser back works', () => {
    expect(pathFor({ name: 'wall' })).toBe('/wall');
    expect(titleFor({ name: 'wall' })).toBe('Wall · Termivoa');
  });
});

describe('run in new tab', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  const from = { id: 'old', projectId: 'project-1', shellProfileId: 'zsh' } as BrokerRPCSessionRecord;
  const created = { id: 'new', projectId: 'project-1', shellProfileId: 'zsh' } as BrokerRPCSessionRecord;

  it('creates a session in the same project and shell that runs the command first, once', async () => {
    const create = vi.spyOn(api, 'createSession').mockResolvedValue({ session: created } as Awaited<ReturnType<typeof api.createSession>>);
    expect(await startLike(from, 'pnpm test')).toEqual({
      name: 'live', session: created, runFirst: { command: 'pnpm test', settled: false },
    });
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ projectId: 'project-1', shellProfileId: 'zsh' }));
    expect(await startLike(from)).toEqual({ name: 'live', session: created });
  });
});
