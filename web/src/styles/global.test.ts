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


  it('lets a herdr project name keep its line: the small labels wrap under it, never cut it short', () => {
    expect(selectorsWith('flex-wrap: wrap')).toContain('.group-title');
    expect(selectorsWith('max-width: 100%')).toContain('.group-name');
    expect(selectorsWith('min-width: 0').filter((selector) => selector.includes('.group-name'))).toEqual([]);
  });

  it('stays flat: no gradients, and no shadow except none', () => {
    expect(css).not.toMatch(/gradient/u);
    expect(selectorsWith('box-shadow')).toEqual([]);
  });
});
