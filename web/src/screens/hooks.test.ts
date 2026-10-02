import { describe, expect, it } from 'vitest';
import { keyboardTracker, latestOnly, websocketUrl } from './hooks';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

describe('latestOnly', () => {
  it('drops a slow older response that finishes after a newer one', async () => {
    const calls = [deferred<string>(), deferred<string>()];
    let next = 0;
    const applied: string[] = [];
    const loader = latestOnly(() => calls[next++]!.promise, (value) => applied.push(value), () => undefined);
    const first = loader.run();
    const second = loader.run();
    calls[1]!.resolve('new');
    await second;
    calls[0]!.resolve('old');
    await first;
    expect(applied).toEqual(['new']);
  });

  it('reports busy while a request is open, so the poll timer skips a tick', async () => {
    const call = deferred<string>();
    const errors: unknown[] = [];
    const loader = latestOnly(() => call.promise, () => undefined, (error) => errors.push(error));
    expect(loader.busy()).toBe(false);
    const run = loader.run();
    expect(loader.busy()).toBe(true);
    call.reject(new Error('down'));
    await run;
    expect(loader.busy()).toBe(false);
    expect(errors).toHaveLength(1);
  });
});

describe('keyboardTracker', () => {
  it('reports open only when the viewport is much shorter than the tallest seen at that width', () => {
    const track = keyboardTracker();
    expect(track(390, 760)).toBe(false);
    expect(track(390, 700)).toBe(false); // browser bars, not a keyboard
    expect(track(390, 420)).toBe(true);
    expect(track(390, 760)).toBe(false);
    expect(track(844, 350)).toBe(false); // rotation starts a new baseline
    expect(track(844, 180)).toBe(true);
    expect(track(390, 420)).toBe(true); // back in portrait, the old baseline still holds
  });
});

describe('websocketUrl', () => {
  it('builds the live socket URL of any computer', () => {
    expect(websocketUrl('https://vps.tailx.ts.net')).toBe('wss://vps.tailx.ts.net/api/v1/ws');
    expect(websocketUrl('http://127.0.0.1:5173')).toBe('ws://127.0.0.1:5173/api/v1/ws');
  });
});
