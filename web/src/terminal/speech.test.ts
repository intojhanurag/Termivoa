import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  appendDictation, recognizerFactory, SpeechController, speechSupported, type RecognitionEvent, type Recognizer,
} from './speech';

class FakeRecognizer implements Recognizer {
  continuous = false;
  interimResults = false;
  lang = '';
  onresult: ((event: RecognitionEvent) => void) | null = null;
  onerror: ((event: { readonly error: string }) => void) | null = null;
  onend: (() => void) | null = null;
  started = 0;
  aborted = 0;
  start() { this.started++; }
  abort() { this.aborted++; }

  /** Sends the whole result list, as the browser does: [text, isFinal] pairs. */
  hear(...results: Array<[string, boolean]>) {
    this.onresult?.({ results: results.map(([transcript, isFinal]) => ({ isFinal, 0: { transcript } })) });
  }
}

function setup() {
  const made: FakeRecognizer[] = [];
  const speech = new SpeechController(() => { const fake = new FakeRecognizer(); made.push(fake); return fake; }, 'en-GB');
  const last = () => made.at(-1) as FakeRecognizer;
  return { speech, made, last };
}

afterEach(() => { vi.useRealTimers(); });

describe('speech support', () => {
  it('finds the standard or the webkit recognizer', () => {
    expect(speechSupported({})).toBe(false);
    expect(speechSupported({ SpeechRecognition: FakeRecognizer })).toBe(true);
    expect(speechSupported({ webkitSpeechRecognition: FakeRecognizer })).toBe(true);
    expect(recognizerFactory({ webkitSpeechRecognition: FakeRecognizer })?.()).toBeInstanceOf(FakeRecognizer);
  });
});

describe('speech controller', () => {
  it('listens continuously with interim results in the given language', () => {
    const { speech, last } = setup();
    speech.start();
    expect(last()).toMatchObject({ continuous: true, interimResults: true, lang: 'en-GB', started: 1 });
    expect(speech.getSnapshot().state).toBe('listening');
  });

  it('shows interim words, then accumulates final ones', () => {
    const { speech, last } = setup();
    const seen: string[] = [];
    speech.subscribe(() => { seen.push(speech.getSnapshot().state); });
    speech.start();
    last().hear(['fix the', false]);
    expect(speech.getSnapshot()).toMatchObject({ finalText: '', interimText: 'fix the' });
    last().hear(['fix the login bug', true], [' in', false]);
    expect(speech.getSnapshot()).toMatchObject({ finalText: 'fix the login bug', interimText: 'in' });
    last().hear(['fix the login bug', true], [' in the header', true]);
    expect(speech.getSnapshot()).toMatchObject({ finalText: 'fix the login bug in the header', interimText: '' });
    expect(seen.length).toBeGreaterThan(0);
  });

  it('counts elapsed seconds while listening', () => {
    vi.useFakeTimers();
    const { speech } = setup();
    speech.start();
    vi.advanceTimersByTime(7_000);
    expect(speech.getSnapshot().elapsed).toBe(7);
  });

  it('stop keeps the words, including unfinished ones, and a new run adds to them', () => {
    const { speech, last, made } = setup();
    speech.start();
    last().hear(['hello', true], [' world', false]);
    speech.stop();
    expect(made[0]?.aborted).toBe(1);
    expect(speech.getSnapshot()).toMatchObject({ state: 'idle', finalText: 'hello world', interimText: '' });
    speech.start();
    last().hear(['again', true]);
    expect(speech.getSnapshot().finalText).toBe('hello world again');
  });

  it('returns to idle with the text kept when the browser ends the run', () => {
    const { speech, last } = setup();
    speech.start();
    last().hear(['run the tests', true]);
    last().onend?.();
    expect(speech.getSnapshot()).toMatchObject({ state: 'idle', finalText: 'run the tests' });
  });

  it('cancel throws the words away', () => {
    const { speech, last } = setup();
    speech.start();
    last().hear(['secret', true]);
    speech.cancel();
    expect(speech.getSnapshot()).toMatchObject({ state: 'idle', finalText: '', interimText: '' });
    expect(last().aborted).toBe(1);
  });

  it.each([
    ['not-allowed', "Microphone access is off. Allow it in Settings, or use the keyboard's mic."],
    ['service-not-allowed', "Microphone access is off. Allow it in Settings, or use the keyboard's mic."],
    ['no-speech', 'No speech heard. Tap the mic and try again.'],
    ['network', "The speech service is not reachable. Try again or use the keyboard's mic."],
    ['audio-capture', 'Voice input stopped. Try again.'],
  ])('maps the %s error to a short message', (code, message) => {
    const { speech, last } = setup();
    speech.start();
    last().hear(['kept', true]);
    last().onerror?.({ error: code });
    expect(speech.getSnapshot()).toMatchObject({ state: 'error', error: message, finalText: 'kept' });
    speech.start();
    expect(speech.getSnapshot().state).toBe('listening');
    expect(speech.getSnapshot().error).toBeUndefined();
  });

  it('ignores events from a recognizer it has let go', () => {
    const { speech, made } = setup();
    speech.start();
    speech.cancel();
    made[0]?.hear(['late', true]);
    expect(speech.getSnapshot().finalText).toBe('');
  });
});

describe('appendDictation', () => {
  it('adds a space only when the draft does not end in whitespace', () => {
    expect(appendDictation('', 'hello')).toBe('hello');
    expect(appendDictation('@a.png ', 'fix this')).toBe('@a.png fix this');
    expect(appendDictation('fix', 'this')).toBe('fix this');
    expect(appendDictation('fix\n', 'this')).toBe('fix\nthis');
    expect(appendDictation('fix', '')).toBe('fix');
  });
});
