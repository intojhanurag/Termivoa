import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { InputController, mapKeyEvent, type InputSink } from './InputController';

type Call = [string, ...unknown[]];

function harness() {
  const calls: Call[] = [];
  const sink: InputSink = {
    key: (key, modifiers, character) => {
      calls.push(character === undefined ? ['key', key, modifiers] : ['key', key, modifiers, character]);
    },
    text: (text) => { calls.push(['text', text]); },
    paste: (text) => { calls.push(['paste', text]); },
  };
  const field = Object.assign(new EventTarget(), { value: '' });
  const controller = new InputController(sink);
  controller.attach(field);
  const fire = (type: string, fields: Record<string, unknown> = {}) => {
    const event = new Event(type, { cancelable: true });
    Object.assign(event, fields);
    field.dispatchEvent(event);
    return event;
  };
  return { calls, field, controller, fire };
}

const none = { shift: false, alt: false, ctrl: false, meta: false };
const key = (value: string, extra: Record<string, unknown> = {}) => ({
  key: value, shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, ...extra,
});

describe('mapKeyEvent', () => {
  it('maps control keys to the protocol enum', () => {
    expect(mapKeyEvent(key('c', { ctrlKey: true }))).toEqual({ key: 'character', modifiers: { ...none, ctrl: true }, character: 'c' });
    expect(mapKeyEvent(key('Tab', { shiftKey: true }))).toEqual({ key: 'tab', modifiers: { ...none, shift: true } });
    expect(mapKeyEvent(key('ArrowUp'))?.key).toBe('arrow-up');
    expect(mapKeyEvent(key('PageDown'))?.key).toBe('page-down');
    expect(mapKeyEvent(key('F5'))?.key).toBe('f5');
    expect(mapKeyEvent(key('Escape'))?.key).toBe('escape');
    expect(mapKeyEvent(key(' ', { ctrlKey: true }))?.key).toBe('space');
  });

  it('leaves plain text, IME processing, and browser shortcuts alone', () => {
    expect(mapKeyEvent(key('a'))).toBeUndefined();
    expect(mapKeyEvent(key('Process', { keyCode: 229 }))).toBeUndefined();
    expect(mapKeyEvent(key('Enter', { isComposing: true }))).toBeUndefined();
    expect(mapKeyEvent(key('v', { metaKey: true }))).toBeUndefined();
    expect(mapKeyEvent(key('é', { altKey: true }))).toBeUndefined();
  });
});

describe('InputController', () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it('sends Enter as a key and prevents the textarea default', () => {
    const { calls, fire } = harness();
    const event = fire('keydown', key('Enter'));
    expect(event.defaultPrevented).toBe(true);
    expect(calls).toEqual([['key', 'enter', none]]);
  });

  it('sends typed text once across beforeinput and input', () => {
    const { calls, fire } = harness();
    const before = fire('beforeinput', { inputType: 'insertText', data: 'a' });
    fire('input', { inputType: 'insertText', data: 'a' });
    expect(before.defaultPrevented).toBe(true);
    expect(calls).toEqual([['text', 'a']]);
  });

  it('sends a Chrome-style IME commit exactly once', () => {
    const { calls, fire } = harness();
    fire('compositionstart');
    fire('keydown', key('Process', { keyCode: 229, isComposing: true }));
    fire('beforeinput', { inputType: 'insertCompositionText', data: 'ni', isComposing: true });
    fire('input', { inputType: 'insertCompositionText', data: 'ni', isComposing: true });
    fire('beforeinput', { inputType: 'insertCompositionText', data: '你', isComposing: true });
    fire('input', { inputType: 'insertCompositionText', data: '你', isComposing: true });
    fire('compositionend', { data: '你' });
    expect(calls).toEqual([['text', '你']]);
  });

  it('swallows a Safari-style insertText echo after compositionend', () => {
    const { calls, fire } = harness();
    fire('compositionstart');
    fire('beforeinput', { inputType: 'insertFromComposition', data: 'こんにちは' });
    fire('compositionend', { data: 'こんにちは' });
    fire('beforeinput', { inputType: 'insertText', data: 'こんにちは' });
    fire('input', { inputType: 'insertText', data: 'こんにちは' });
    expect(calls).toEqual([['text', 'こんにちは']]);
  });

  it('still sends a repeated commit once the echo window has passed', async () => {
    const { calls, fire } = harness();
    fire('compositionstart');
    fire('compositionend', { data: 'ok' });
    await vi.advanceTimersByTimeAsync(0);
    fire('beforeinput', { inputType: 'insertText', data: 'ok' });
    expect(calls).toEqual([['text', 'ok'], ['text', 'ok']]);
  });

  it('maps soft-keyboard line breaks and backspace to keys', () => {
    const { calls, fire } = harness();
    fire('beforeinput', { inputType: 'insertLineBreak', data: null });
    fire('beforeinput', { inputType: 'deleteContentBackward', data: null });
    expect(calls).toEqual([['key', 'enter', none], ['key', 'backspace', none]]);
  });

  it('applies a latched Ctrl to the next typed character', () => {
    const { calls, fire, controller } = harness();
    controller.toggleLatch('ctrl');
    fire('beforeinput', { inputType: 'insertText', data: 'c' });
    fire('beforeinput', { inputType: 'insertText', data: 'c' });
    expect(calls).toEqual([
      ['key', 'character', { ...none, ctrl: true }, 'c'],
      ['text', 'c'],
    ]);
  });

  it('routes clipboard paste to the explicit composer path', () => {
    const { calls, fire } = harness();
    const event = fire('paste', { clipboardData: { getData: () => 'rm -rf build\nmake' } });
    expect(event.defaultPrevented).toBe(true);
    expect(calls).toEqual([['paste', 'rm -rf build\nmake']]);
  });

  it('clears the field after input so text is never re-read', () => {
    const { field, fire } = harness();
    field.value = 'stale';
    fire('input', { inputType: 'insertText', data: 'x' });
    expect(field.value).toBe('');
  });
});
