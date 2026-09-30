import type { KeyName, Modifiers } from '../generated/protocol';
import { NO_MODIFIERS } from './InputController';

export type AgentAction =
  | { readonly kind: 'key'; readonly key: KeyName; readonly modifiers: Modifiers; readonly character?: string }
  | { readonly kind: 'text'; readonly text: string };

export interface AgentKey {
  readonly id: string;
  /** Lowercase keycap text. */
  readonly glyph: string;
  readonly sub?: string;
  /** Accessible name; glyphs alone are not readable. */
  readonly name: string;
  readonly tone?: 'stop' | 'enter';
  /** Relative width in the row. */
  readonly weight: number;
  readonly action: AgentAction;
}

const key = (name: KeyName, modifiers: Partial<Modifiers> = {}): AgentAction => (
  { kind: 'key', key: name, modifiers: { ...NO_MODIFIERS, ...modifiers } }
);

/**
 * The agent keys send fixed messages. They ignore the armed Ctrl/Alt latch
 * (that belongs to the modifier row and the keyboard), so "⌃C" is always
 * exactly Ctrl+C.
 */
export const AGENT_KEYS: ReadonlyArray<AgentKey> = [
  { id: 'esc', glyph: 'esc', sub: 'stop', name: 'Escape', weight: 1.25, action: key('escape') },
  {
    id: 'ctrl-c', glyph: '⌃C', name: 'Control C', tone: 'stop', weight: 1,
    action: { kind: 'key', key: 'character', modifiers: { ...NO_MODIFIERS, ctrl: true }, character: 'c' },
  },
  { id: 'shift-tab', glyph: '⇧⇥', sub: 'mode', name: 'Shift Tab', weight: 1, action: key('tab', { shift: true }) },
  { id: 'up', glyph: '↑', name: 'Up arrow', weight: 1, action: key('arrow-up') },
  { id: '1', glyph: '1', name: '1', weight: 0.8, action: { kind: 'text', text: '1' } },
  { id: '2', glyph: '2', name: '2', weight: 0.8, action: { kind: 'text', text: '2' } },
  { id: 'y', glyph: 'y', name: 'y', weight: 0.8, action: { kind: 'text', text: 'y' } },
  { id: 'enter', glyph: '⏎', name: 'Enter', tone: 'enter', weight: 1.2, action: key('enter') },
];

/**
 * Down arrow as an agent key. Only the keyboard-up row shows it, next to ↑,
 * because that row scrolls and the arrow row's ↓ would be out of reach.
 */
export const DOWN_KEY: AgentKey = { id: 'down', glyph: '↓', name: 'Down arrow', weight: 1, action: key('arrow-down') };

export interface KeyTarget {
  sendKey(key: KeyName, modifiers: Modifiers, character?: string): boolean;
  sendText(text: string): boolean;
}

/** Sends one agent key. The target refuses when this phone lacks control. */
export function sendAgentKey(target: KeyTarget, agentKey: AgentKey): boolean {
  const { action } = agentKey;
  if (action.kind === 'text') {
    return target.sendText(action.text);
  }
  return action.character === undefined
    ? target.sendKey(action.key, action.modifiers)
    : target.sendKey(action.key, action.modifiers, action.character);
}
