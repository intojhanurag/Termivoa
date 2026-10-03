import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('global.css', import.meta.url), 'utf8').replace(/\/\*[\s\S]*?\*\//gu, '');
/** The selector of every rule that has `declaration`. */
const selectorsWith = (declaration: string) =>
  [...css.matchAll(/([^{}]+)\{([^{}]*)\}/gu)].filter(([, , body]) => body?.includes(declaration)).map(([, selector]) => selector?.trim() ?? '');

describe('global styles', () => {
  it('keeps only commands on one line, so long paths and branch names wrap in a 390 px sheet', () => {
    const code = selectorsWith('white-space: nowrap').filter((selector) => /\bcode\b/u.test(selector));
    expect(code).toEqual(['.notice code, .add-computer-hint code']);
  });

  it('gives "Check again" on a blocked card the same amber as on an unreachable one', () => {
    const amber = selectorsWith('--ink: var(--amber-text)');
    expect(amber.some((selector) => selector.includes('.computer-card:is(.is-unreachable, .is-blocked) button.secondary'))).toBe(true);
  });
});
