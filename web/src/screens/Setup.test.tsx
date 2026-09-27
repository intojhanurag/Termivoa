import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/http';
import { pairingFailure, Setup } from './Setup';

afterEach(() => vi.unstubAllGlobals());

const render = (path: string, reason?: string) => {
  vi.stubGlobal('window', { location: { pathname: path } });
  return renderToStaticMarkup(
    <Setup grant={{ value: undefined }} {...(reason === undefined ? {} : { reason })} onPaired={() => undefined} />,
  );
};

describe('pairingFailure', () => {
  it('names the likely cause in plain words', () => {
    expect(pairingFailure(new ApiError(410, 'pairing_expired', 'grant expired')))
      .toBe('This pairing link expired or was already used.');
    expect(pairingFailure(new ApiError(401, 'unauthorized', 'bad grant')))
      .toBe('This pairing link expired or was already used.');
    expect(pairingFailure(new ApiError(429, 'limit_exceeded', 'slow down'))).toMatch(/Too many pairing attempts/u);
    expect(pairingFailure(new ApiError(0, 'network', 'Connection unavailable'))).toMatch(/cannot reach your computer/u);
    expect(pairingFailure(new Error('boom'))).toBe('Pairing did not work.');
  });
});

describe('Setup', () => {
  it('shows pairing steps with the reason after logout or revocation', () => {
    const html = render('/', 'You logged out. This phone no longer has access.');
    expect(html).toContain('You logged out. This phone no longer has access.');
    expect(html).toContain('On your computer, run <code>pterm pair</code>.');
    expect(html).not.toContain('role="alert"');
  });

  it('says a /pair link without a usable grant must be made again', () => {
    const html = render('/pair');
    expect(html).toContain('role="alert"');
    expect(html).toContain('Run <code>pterm pair</code> again on your computer');
  });
});
