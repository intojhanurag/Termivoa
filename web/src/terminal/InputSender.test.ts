import { describe, expect, it } from 'vitest';
import { DeliveryUncertainError, InputSender } from './InputSender';

const sessionId = '00000000-0000-4000-8000-000000000001';
const generation = '00000000-0000-4000-8000-000000000002';
const lease = 'a'.repeat(43);
const modifiers = { shift: false, alt: false, ctrl: false, meta: false };

function sender(send: (message: string) => void) {
  let request = 0;
  return new InputSender({ send }, sessionId, generation, lease, () => (
    `00000000-0000-4000-8000-${String(++request).padStart(12, '0')}`
  ));
}

describe('InputSender', () => {
  it('sends ordered structured input with generation and lease', () => {
    const messages: string[] = [];
    const input = sender((message) => messages.push(message));

    expect(input.sendText('hello')).toBe('1');
    expect(input.sendKey('enter', modifiers)).toBe('2');

    const first = JSON.parse(messages[0] ?? '{}') as Record<string, unknown>;
    expect(first).toMatchObject({
      version: 1,
      type: 'input.text',
      sessionId,
      generation,
      payload: { text: 'hello', lease, inputSequence: '1' },
    });
  });

  it('never queues or resumes input after disconnect', () => {
    const input = sender(() => undefined);
    input.sendText('sent');
    expect(input.disconnect()).toEqual([{ inputSequence: '1', type: 'input.text' }]);
    expect(() => input.sendText('offline')).toThrow('new connection');
  });

  it('reports uncertain delivery and stops after a transport failure', () => {
    const input = sender(() => { throw new Error('closed'); });
    expect(() => input.sendText('maybe')).toThrow(DeliveryUncertainError);
    expect(() => input.sendText('never queued')).toThrow('new connection');
  });

  it('removes acknowledged input from the uncertain set', () => {
    const input = sender(() => undefined);
    input.sendPaste('one\ntwo');
    input.accepted('1');
    expect(input.disconnect()).toEqual([]);
  });

  it('rejects controls, unpaired surrogates, and oversized input locally', () => {
    const input = sender(() => undefined);
    expect(() => input.sendText('line\n')).toThrow('invalid');
    expect(() => input.sendPaste('\u0000')).toThrow('invalid');
    expect(() => input.sendText('\uD800')).toThrow('invalid');
    expect(() => input.sendPaste('x'.repeat(64 * 1024 + 1))).toThrow('exceeds');
  });

  it('rejects malformed character keys and mouse coordinates locally', () => {
    const input = sender(() => undefined);
    expect(() => input.sendKey('character', modifiers, 'c')).toThrow('control modifier');
    expect(() => input.sendKey('enter', modifiers, 'x')).toThrow('Only a character');
    expect(() => input.sendMouse('press', 'left', 400, 0, modifiers)).toThrow('outside');
  });
});
