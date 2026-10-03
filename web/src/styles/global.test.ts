import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('global.css', import.meta.url), 'utf8').replace(/\/\*[\s\S]*?\*\//gu, '');
/** The selector of every rule that has `declaration`. */
const selectorsWith = (declaration: string) =>
  [...css.matchAll(/([^{}]+)\{([^{}]*)\}/gu)].filter(([, , body]) => body?.includes(declaration)).map(([, selector]) => selector?.trim() ?? '');

describe('global styles', () => {
  it('wraps code inside its box, so a long command or host name never makes a 375 px page wider', () => {
    expect(selectorsWith('white-space: nowrap').filter((selector) => /\bcode\b/u.test(selector))).toEqual([]);
    expect(selectorsWith('overflow-wrap: anywhere')).toContain('code');
    // Only the "--via" flag stays whole (PairVia).
    expect(selectorsWith('white-space: nowrap')).toContain('.nowrap');
    const command = [...css.matchAll(/\.notice-command \{([^}]*)\}/gu)].map(([, body]) => body).join('');
    expect(command).not.toMatch(/overflow-x|white-space|inline-flex/u);
  });

  it('gives "Check again" on a blocked card the same amber as on an unreachable one', () => {
    const amber = selectorsWith('--ink: var(--amber-text)');
    expect(amber.some((selector) => selector.includes('.computer-card:is(.is-unreachable, .is-blocked) button.secondary'))).toBe(true);
  });
});
