import { describe, expect, it } from 'vitest';
import { latestOnly } from './hooks';

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
