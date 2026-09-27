import { describe, expect, it } from 'vitest';
import type { RunFirstRequest } from '../screens/presets';
import { INITIAL_STATUS, type ConnectionStatus } from './Connection';
import { runFirstCommand } from './runFirst';

/** A connection double whose status the test drives. */
function fake() {
  let status: ConnectionStatus = INITIAL_STATUS;
  const listeners = new Set<() => void>();
  const calls: string[] = [];
  const set = (change: Partial<ConnectionStatus>) => {
    status = { ...status, ...change };
    for (const listener of [...listeners]) listener();
  };
  return {
    calls,
    set,
    listeners,
    target: {
      subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
      getStatus: () => status,
      takeControl: () => { calls.push('control.acquire'); set({ control: 'acquiring' }); },
      sendText: (text: string) => { calls.push(`text:${text}`); set({}); return true; },
      sendKey: (key: string) => { calls.push(`key:${key}`); set({}); return true; },
    },
  };
}

const request = (): RunFirstRequest => ({ command: 'claude', settled: false });
const live = { link: 'live' as const, sessionState: 'running' as const };

describe('run first', () => {
  it('waits for the first snapshot, takes free control, sends once, and keeps control', () => {
    const { calls, set, target, listeners } = fake();
    const reports: string[] = [];
    const run = request();
    runFirstCommand(target, run, (message) => reports.push(message));
    set({ link: 'attaching' });
    expect(calls).toEqual([]);

    set(live);
    expect(calls).toEqual(['control.acquire']);
    set({ control: 'controller' });
    expect(calls).toEqual(['control.acquire', 'text:claude', 'key:enter']);
    expect(run.settled).toBe(true);
    expect(listeners.size).toBe(0);
    expect(reports).toEqual([]);
    expect(calls).not.toContain('control.release');
  });

  it('never sends again after a reconnect', () => {
    const { calls, set, target } = fake();
    const run = request();
    runFirstCommand(target, run, () => undefined);
    set(live);
    set({ control: 'controller' });
    set({ link: 'reconnecting', control: 'observer' });
    set({ link: 'live' });
    set({ control: 'controller' });
    expect(calls.filter((call) => call === 'text:claude')).toHaveLength(1);
    // A later Live mount sees the settled request and does nothing.
    const again = fake();
    runFirstCommand(again.target, run, () => undefined);
    again.set(live);
    expect(again.calls).toEqual([]);
  });

  it('does not ask for control or send when someone else has it', () => {
    const { calls, set, target } = fake();
    const reports: string[] = [];
    const run = request();
    runFirstCommand(target, run, (message) => reports.push(message));
    set({ ...live, otherController: true, controllerLabel: 'MacBook' });
    expect(calls).toEqual([]);
    expect(run.settled).toBe(true);
    expect(reports[0]).toMatch(/Did not run “claude”: another connection has control/u);
  });

  it('gives up without sending when control is taken or the link drops before the grant', () => {
    for (const change of [
      { control: 'observer' as const, otherController: true },
      { link: 'reconnecting' as const, control: 'observer' as const },
    ]) {
      const { calls, set, target } = fake();
      const reports: string[] = [];
      const run = request();
      runFirstCommand(target, run, (message) => reports.push(message));
      set(live);
      set(change);
      set({ link: 'live', control: 'controller', otherController: false });
      expect(calls).toEqual(['control.acquire']);
      expect(run.settled).toBe(true);
      expect(reports[0]).toMatch(/could not take control. Nothing was sent/u);
    }
  });

  it('does nothing when the session ended first, and stays pending if unmounted before the snapshot', () => {
    const ended = fake();
    const reports: string[] = [];
    runFirstCommand(ended.target, request(), (message) => reports.push(message));
    ended.set({ ...live, sessionState: 'exited' });
    expect(ended.calls).toEqual([]);
    expect(reports[0]).toMatch(/session ended/u);

    const first = fake();
    const run = request();
    const stop = runFirstCommand(first.target, run, () => undefined);
    stop();
    expect(run.settled).toBe(false);
    const second = fake();
    runFirstCommand(second.target, run, () => undefined);
    second.set(live);
    second.set({ control: 'controller' });
    expect(first.calls).toEqual([]);
    expect(second.calls).toEqual(['control.acquire', 'text:claude', 'key:enter']);
  });
});
