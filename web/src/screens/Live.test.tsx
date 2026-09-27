import { describe, expect, it } from 'vitest';
import { INITIAL_STATUS, type ConnectionStatus } from '../terminal/Connection';
import { readOnlyHint } from './Live';

const live: ConnectionStatus = { ...INITIAL_STATUS, link: 'live', control: 'observer', sessionState: 'running' };

describe('readOnlyHint', () => {
  it('tells a read-only viewer how to type', () => {
    expect(readOnlyHint(live)).toBe('Read-only. Tap Take control to type.');
    expect(readOnlyHint({ ...live, otherController: true })).toBe('Tap Take control to move control to this phone.');
  });

  it('stays quiet while controlling, connecting, or after the shell ended', () => {
    expect(readOnlyHint({ ...live, control: 'controller' })).toBeUndefined();
    expect(readOnlyHint({ ...live, control: 'acquiring' })).toBeUndefined();
    expect(readOnlyHint({ ...live, link: 'reconnecting' })).toBeUndefined();
    expect(readOnlyHint({ ...live, sessionState: 'exited' })).toBeUndefined();
  });
});
