import { describe, expect, it } from 'vitest';
import { AGENT_KEYS, sendAgentKey } from './agentKeys';
import { InputSender } from './InputSender';

const sessionId = '00000000-0000-4000-8000-000000000001';
const generation = '00000000-0000-4000-8000-000000000002';
const lease = 'L'.repeat(43);

function harness(canSend = true) {
  const sent: unknown[] = [];
  const sender = new InputSender({ send: (message) => { sent.push(JSON.parse(message)); } }, sessionId, generation, lease, () => 'r');
  // Mirrors Connection: without control nothing reaches the sender.
  const target = {
    sendKey: (...args: Parameters<InputSender['sendKey']>) => canSend && sender.sendKey(...args) !== '',
    sendText: (text: string) => canSend && sender.sendText(text) !== '',
  };
  return { sent, target };
}

const none = { shift: false, alt: false, ctrl: false, meta: false };
const envelope = (type: string, payload: object, inputSequence: string) => ({
  version: 1, type, requestId: 'r', sessionId, generation, payload: { ...payload, lease, inputSequence },
});

describe('agent keys', () => {
  it('send the exact protocol envelopes', () => {
    const { sent, target } = harness();
    for (const key of AGENT_KEYS) {
      expect(sendAgentKey(target, key)).toBe(true);
    }
    expect(sent).toEqual([
      envelope('input.key', { key: 'escape', modifiers: none }, '1'),
      envelope('input.key', { key: 'character', modifiers: { ...none, ctrl: true }, character: 'c' }, '2'),
      envelope('input.key', { key: 'tab', modifiers: { ...none, shift: true } }, '3'),
      envelope('input.key', { key: 'arrow-up', modifiers: none }, '4'),
      envelope('input.text', { text: '1' }, '5'),
      envelope('input.text', { text: '2' }, '6'),
      envelope('input.text', { text: 'y' }, '7'),
      envelope('input.key', { key: 'enter', modifiers: none }, '8'),
    ]);
  });

  it('send nothing without control', () => {
    const { sent, target } = harness(false);
    expect(AGENT_KEYS.map((key) => sendAgentKey(target, key))).toEqual(AGENT_KEYS.map(() => false));
    expect(sent).toEqual([]);
  });
});
