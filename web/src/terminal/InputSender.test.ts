import { describe, expect, it } from 'vitest';
import { DeliveryUncertainError, InputBackpressureError, InputSender } from './InputSender';

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
    let calls = 0;
    const input = sender(() => {
      calls++;
      if (calls === 2) throw new Error('closed');
    });
    input.sendText('first pending');
    let failure: DeliveryUncertainError | undefined;
    try {
      input.sendText('maybe');
    } catch (error) {
      failure = error as DeliveryUncertainError;
    }
    expect(failure).toBeInstanceOf(DeliveryUncertainError);
    expect(failure?.inputs).toEqual([
      { inputSequence: '1', type: 'input.text' },
      { inputSequence: '2', type: 'input.text' },
    ]);
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
    expect(() => input.sendKey('character', { ...modifiers, ctrl: true }, ' ')).toThrow('printable ASCII');
    expect(() => input.sendKey('enter', modifiers, 'x')).toThrow('Only a character');
    expect(() => input.sendMouse('press', 'left', 400, 0, modifiers)).toThrow('outside');
    expect(() => input.sendMouse('wheel', 'left', 0, 0, modifiers)).toThrow('incompatible');
    expect(() => input.sendMouse('press', 'none', 0, 0, modifiers)).toThrow('incompatible');
  });

  it('stops and closes a transport whose acknowledgements stall', () => {
    let sent = 0;
    const closes: string[] = [];
    let request = 0;
    const input = new InputSender(
      { send: () => { sent++; }, close: (reason) => closes.push(reason) },
      sessionId,
      generation,
      lease,
      () => `00000000-0000-4000-8000-${String(++request).padStart(12, '0')}`,
    );
    for (let index = 0; index < 256; index++) {
      input.sendText('x');
    }
    expect(() => input.sendText('not sent')).toThrow(InputBackpressureError);
    expect(sent).toBe(256);
    expect(closes).toEqual(['input acknowledgements stalled']);
    expect(() => input.sendText('still stopped')).toThrow('new connection');
  });
});
