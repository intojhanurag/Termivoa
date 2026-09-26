import { afterEach, describe, expect, it, vi } from 'vitest';

describe('runtime protocol validation', () => {
  const RealFunction = globalThis.Function;

  afterEach(() => {
    globalThis.Function = RealFunction;
  });

  // The gateway CSP has no 'unsafe-eval'; any runtime code compilation blanks the page.
  it('loads and validates without compiling code (CSP without unsafe-eval)', async () => {
    const compiled: string[] = [];
    globalThis.Function = new Proxy(RealFunction, {
      apply(_target, _this, args: string[]) { compiled.push(args.join()); throw new EvalError('blocked by CSP'); },
      construct(_target, args: string[]) { compiled.push(args.join()); throw new EvalError('blocked by CSP'); },
    });
    vi.resetModules();
    const { parseHostMessage } = await import('./validate');
    expect(() => parseHostMessage(JSON.stringify({
      version: 1, type: 'access.revoked', requestId: '00000000-0000-4000-8000-000000000001',
      payload: { message: 'Revoked' },
    }))).not.toThrow();
    expect(() => parseHostMessage(JSON.stringify({
      version: 1, type: 'access.revoked', requestId: '00000000-0000-4000-8000-000000000001',
      payload: { message: 'x'.repeat(513) },
    }))).toThrow();
    expect(compiled).toEqual([]);
  });
});
