/** The small part of the Web Speech API that the composer uses. */
export interface Recognizer {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  onresult: ((event: RecognitionEvent) => void) | null;
  onerror: ((event: { readonly error: string }) => void) | null;
  onend: (() => void) | null;
  start(): void;
  abort(): void;
}

export type RecognitionEvent = {
  readonly results: ArrayLike<{ readonly isFinal: boolean; readonly 0?: { readonly transcript: string } }>;
};

export type RecognizerFactory = () => Recognizer;

type SpeechWindow = { SpeechRecognition?: new () => Recognizer; webkitSpeechRecognition?: new () => Recognizer };

/** Returns a way to make recognizers, or undefined where the browser has none. */
export function recognizerFactory(win: object = globalThis): RecognizerFactory | undefined {
  const { SpeechRecognition, webkitSpeechRecognition } = win as SpeechWindow;
  const Recognition = SpeechRecognition ?? webkitSpeechRecognition;
  return Recognition === undefined ? undefined : () => new Recognition();
}

export type SpeechSnapshot = {
  readonly state: 'idle' | 'listening' | 'error';
  readonly finalText: string;
  readonly interimText: string;
  readonly elapsed: number;
  readonly error?: string;
};

export const IDLE_SPEECH: SpeechSnapshot = { state: 'idle', finalText: '', interimText: '', elapsed: 0 };

export function speechError(code: string): string {
  switch (code) {
    case 'not-allowed':
    case 'service-not-allowed':
      return "Microphone access is off. Allow it in Settings, or use the keyboard's mic.";
    case 'no-speech':
      return 'No speech heard. Tap the mic and try again.';
    case 'network':
      return "The speech service is not reachable. Try again or use the keyboard's mic.";
    default:
      return 'Voice input stopped. Try again.';
  }
}

export function joinWords(...parts: string[]): string {
  return parts.map((part) => part.trim()).filter((part) => part !== '').join(' ');
}

/** Adds dictated words to the draft, with a space when the draft does not end in whitespace. */
export function appendDictation(text: string, spoken: string): string {
  if (spoken === '') return text;
  return text === '' || /\s$/u.test(text) ? `${text}${spoken}` : `${text} ${spoken}`;
}

/**
 * One dictation. Text from earlier listening runs is kept when the mic is
 * tapped again, and when the browser ends a run by itself (Safari does).
 */
export class SpeechController {
  private snapshot = IDLE_SPEECH;
  private readonly listeners = new Set<() => void>();
  private recognizer: Recognizer | undefined;
  private timer: ReturnType<typeof setInterval> | undefined;

  constructor(private readonly create: RecognizerFactory, private readonly lang: string) {}

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  readonly getSnapshot = (): SpeechSnapshot => this.snapshot;

  /** Call from the user's tap: browsers ask for the mic only in a user gesture. */
  start(): void {
    if (this.recognizer !== undefined) return;
    const recognizer = this.create();
    recognizer.continuous = true;
    recognizer.interimResults = true;
    recognizer.lang = this.lang;
    const base = this.snapshot.finalText;
    recognizer.onresult = (event) => {
      if (recognizer !== this.recognizer) return;
      const finals: string[] = [];
      const interims: string[] = [];
      for (let index = 0; index < event.results.length; index++) {
        const result = event.results[index];
        if (result !== undefined) (result.isFinal ? finals : interims).push(result[0]?.transcript ?? '');
      }
      this.set({ finalText: joinWords(base, ...finals), interimText: joinWords(...interims) });
    };
    recognizer.onerror = (event) => {
      if (recognizer !== this.recognizer) return;
      this.release();
      this.set({ state: 'error', error: speechError(event.error) });
    };
    recognizer.onend = () => {
      if (recognizer !== this.recognizer) return;
      this.release();
      this.set({ state: 'idle' });
    };
    this.recognizer = recognizer;
    const { error: _error, ...rest } = this.snapshot;
    this.snapshot = { ...rest, state: 'listening', elapsed: 0 };
    this.emit();
    const started = Date.now();
    this.timer = setInterval(() => { this.set({ elapsed: Math.floor((Date.now() - started) / 1000) }); }, 1000);
    try {
      recognizer.start();
    } catch {
      this.release();
      this.set({ state: 'error', error: speechError('') });
    }
  }

  /** Stops listening and keeps every word heard so far. */
  stop(): void {
    if (this.recognizer === undefined) return;
    this.release();
    this.set({ state: 'idle' });
  }

  /** Stops listening and throws the words away. */
  cancel(): void {
    this.release();
    this.snapshot = IDLE_SPEECH;
    this.emit();
  }

  /** Detaches and aborts the recognizer, folding unfinished words into the kept text. */
  private release(): void {
    const recognizer = this.recognizer;
    this.recognizer = undefined;
    clearInterval(this.timer);
    this.timer = undefined;
    if (recognizer !== undefined) {
      recognizer.onresult = null;
      recognizer.onerror = null;
      recognizer.onend = null;
      recognizer.abort();
    }
    this.snapshot = { ...this.snapshot, finalText: joinWords(this.snapshot.finalText, this.snapshot.interimText), interimText: '' };
  }

  private set(change: Partial<SpeechSnapshot>): void {
    this.snapshot = { ...this.snapshot, ...change };
    this.emit();
  }

  private emit(): void {
    for (const listener of this.listeners) listener();
  }
}
