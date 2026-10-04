import { describe, expect, it, vi } from 'vitest';
import { applyUpdate, keyboardTracker, websocketUrl } from './hooks';

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

describe('applyUpdate', () => {
  it('lets the waiting worker take over, then reloads once it controls the page', () => {
    const postMessage = vi.fn();
    const addEventListener = vi.fn();
    const reload = vi.fn();
    applyUpdate({ state: 'installed', postMessage }, { addEventListener }, reload);
    expect(postMessage).toHaveBeenCalledWith({ type: 'skip-waiting' });
    expect(addEventListener).toHaveBeenCalledWith('controllerchange', reload, { once: true });
    expect(reload).not.toHaveBeenCalled();
  });

  it('reloads at once when another tab already let it take over, so the tap is not a dead one', () => {
    const postMessage = vi.fn();
    const reload = vi.fn();
    applyUpdate({ state: 'activated', postMessage }, { addEventListener: vi.fn() }, reload);
    expect(reload).toHaveBeenCalledOnce();
    expect(postMessage).not.toHaveBeenCalled();
  });
});
