import type { KeyName, Modifiers } from '../generated/protocol';

export interface InputSink {
  key(key: KeyName, modifiers: Modifiers, character?: string): void;
  text(text: string): void;
  /** Pasted text is never sent directly; the caller routes it to the composer. */
  paste(text: string): void;
}

export interface MappedKey {
  readonly key: KeyName;
  readonly modifiers: Modifiers;
  readonly character?: string;
}

interface KeyboardLike {
  readonly key: string;
  readonly shiftKey: boolean;
  readonly altKey: boolean;
  readonly ctrlKey: boolean;
  readonly metaKey: boolean;
  readonly isComposing?: boolean;
  readonly keyCode?: number;
  preventDefault(): void;
}

interface InputEventLike {
  readonly inputType: string;
  readonly data: string | null;
  readonly isComposing?: boolean;
  preventDefault(): void;
}

interface CompositionLike {
  readonly data: string;
}

interface ClipboardLike {
  readonly clipboardData: { getData(type: string): string } | null;
  preventDefault(): void;
}

export interface TextAreaLike extends EventTarget {
  value: string;
}

const NAMED_KEYS: Readonly<Record<string, KeyName>> = {
  Enter: 'enter',
  Backspace: 'backspace',
  Escape: 'escape',
  Tab: 'tab',
  ArrowUp: 'arrow-up',
  ArrowDown: 'arrow-down',
  ArrowLeft: 'arrow-left',
  ArrowRight: 'arrow-right',
  Home: 'home',
  End: 'end',
  Insert: 'insert',
  Delete: 'delete',
  PageUp: 'page-up',
  PageDown: 'page-down',
  F1: 'f1', F2: 'f2', F3: 'f3', F4: 'f4', F5: 'f5', F6: 'f6',
  F7: 'f7', F8: 'f8', F9: 'f9', F10: 'f10', F11: 'f11', F12: 'f12',
};

const PRINTABLE_ASCII = /^[!-~]$/u;
const LINE_BREAK = /\r\n|\r|\n/u;
const SINGLE_LINE_BREAK = /^(?:\r\n|\r|\n)$/u;
const DISALLOWED_TEXT = /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/gu;

export const NO_MODIFIERS: Modifiers = Object.freeze({ shift: false, alt: false, ctrl: false, meta: false });

/**
 * Maps a hardware key event to the protocol key enum. Plain printable keys
 * return undefined so they arrive once through the text path. Command/meta
 * shortcuts stay with the browser (copy, paste, select all).
 */
export function mapKeyEvent(event: Omit<KeyboardLike, 'preventDefault'>): MappedKey | undefined {
  if (event.isComposing === true || event.keyCode === 229 || event.metaKey) {
    return undefined;
  }
  const modifiers: Modifiers = {
    shift: event.shiftKey,
    alt: event.altKey,
    ctrl: event.ctrlKey,
    meta: false,
  };
  const named = NAMED_KEYS[event.key];
  if (named !== undefined) {
    return { key: named, modifiers };
  }
  if (!event.ctrlKey && !event.altKey) {
    return undefined;
  }
  if (event.key === ' ') {
    return { key: 'space', modifiers };
  }
  if (PRINTABLE_ASCII.test(event.key)) {
    return { key: 'character', modifiers, character: event.key };
  }
  return undefined;
}

/**
 * Owns the dedicated textarea used for mobile text, IME, and hardware keys.
 * Each IME commit is emitted exactly once: compositionend is authoritative,
 * composition-typed input events are ignored, and an immediately following
 * insertText echo of the same commit is swallowed.
 */
export class InputController {
  private composing = false;
  private commitEcho: string | undefined;
  private commitEchoTimer: ReturnType<typeof setTimeout> | undefined;
  private sawBeforeInput = false;
  private latched = { ctrl: false, alt: false };
  private element: TextAreaLike | undefined;

  constructor(
    private readonly sink: InputSink,
    private readonly onLatchChange: (latched: { ctrl: boolean; alt: boolean }) => void = () => undefined,
  ) {}

  attach(element: TextAreaLike): () => void {
    this.element = element;
    const listeners: Array<[string, (event: Event) => void]> = [
      ['keydown', (event) => this.handleKeyDown(event as unknown as KeyboardLike)],
      ['beforeinput', (event) => this.handleBeforeInput(event as unknown as InputEventLike)],
      ['input', (event) => this.handleInput(event as unknown as InputEventLike)],
      ['compositionstart', () => this.handleCompositionStart()],
      ['compositionend', (event) => this.handleCompositionEnd(event as unknown as CompositionLike)],
      ['paste', (event) => this.handlePaste(event as unknown as ClipboardLike)],
    ];
    for (const [type, listener] of listeners) {
      element.addEventListener(type, listener);
    }
    return () => {
      for (const [type, listener] of listeners) {
        element.removeEventListener(type, listener);
      }
      clearTimeout(this.commitEchoTimer);
      this.element = undefined;
    };
  }

  toggleLatch(modifier: 'ctrl' | 'alt'): void {
    this.latched = { ...this.latched, [modifier]: !this.latched[modifier] };
    this.onLatchChange(this.latched);
  }

  /** On-screen key bar press; applies and clears latched modifiers. */
  pressKey(key: KeyName): void {
    this.sink.key(key, this.consumeLatch());
  }

  handleKeyDown(event: KeyboardLike): void {
    if (this.composing) {
      return;
    }
    const mapped = mapKeyEvent(event);
    if (mapped === undefined) {
      return;
    }
    event.preventDefault();
    const latch = this.consumeLatch();
    const modifiers = {
      ...mapped.modifiers,
      ctrl: mapped.modifiers.ctrl || latch.ctrl,
      alt: mapped.modifiers.alt || latch.alt,
    };
    if (mapped.character === undefined) {
      this.sink.key(mapped.key, modifiers);
    } else {
      this.sink.key(mapped.key, modifiers, mapped.character);
    }
  }

  handleBeforeInput(event: InputEventLike): void {
    this.sawBeforeInput = true;
    switch (event.inputType) {
      case 'insertCompositionText':
      case 'deleteCompositionText':
      case 'insertFromComposition':
        return;
      case 'insertText':
      case 'insertReplacementText':
        if (this.composing || event.isComposing === true) {
          return;
        }
        event.preventDefault();
        if (event.data !== null && event.data !== '' && !this.consumeCommitEcho(event.data)) {
          this.emitText(event.data);
        }
        return;
      case 'insertLineBreak':
      case 'insertParagraph':
        event.preventDefault();
        this.sink.key('enter', this.consumeLatch());
        return;
      case 'deleteContentBackward':
        if (this.composing) {
          return;
        }
        event.preventDefault();
        this.sink.key('backspace', this.consumeLatch());
        return;
      case 'deleteContentForward':
        if (this.composing) {
          return;
        }
        event.preventDefault();
        this.sink.key('delete', this.consumeLatch());
        return;
      default:
        if (!this.composing) {
          event.preventDefault();
        }
    }
  }

  /** Fallback only for engines that never deliver beforeinput. */
  handleInput(event: InputEventLike): void {
    if (this.composing || event.isComposing === true) {
      return;
    }
    if (!this.sawBeforeInput && event.inputType === 'insertText' && event.data !== null && event.data !== ''
      && !this.consumeCommitEcho(event.data)) {
      this.emitText(event.data);
    }
    this.clearField();
  }

  handleCompositionStart(): void {
    this.composing = true;
  }

  handleCompositionEnd(event: CompositionLike): void {
    this.composing = false;
    if (event.data !== '') {
      this.emitText(event.data);
      this.commitEcho = event.data;
      clearTimeout(this.commitEchoTimer);
      this.commitEchoTimer = setTimeout(() => {
        this.commitEcho = undefined;
      }, 0);
    }
    setTimeout(() => {
      if (!this.composing) {
        this.clearField();
      }
    }, 0);
  }

  handlePaste(event: ClipboardLike): void {
    event.preventDefault();
    const text = event.clipboardData?.getData('text/plain') ?? '';
    if (text !== '') {
      this.sink.paste(text);
    }
  }

  private consumeCommitEcho(data: string): boolean {
    if (this.commitEcho !== undefined && this.commitEcho === data) {
      this.commitEcho = undefined;
      return true;
    }
    return false;
  }

  private emitText(data: string): void {
    // Keyboard clipboard chips and text replacements insert text without a
    // paste event; multiline content must still get the composer's confirmation.
    if (LINE_BREAK.test(data) && !SINGLE_LINE_BREAK.test(data)) {
      this.sink.paste(data);
      return;
    }
    const latch = this.consumeLatch();
    if ((latch.ctrl || latch.alt) && [...data].length === 1) {
      const modifiers = { ...NO_MODIFIERS, ...latch };
      if (data === ' ') {
        this.sink.key('space', modifiers);
        return;
      }
      if (PRINTABLE_ASCII.test(data)) {
        this.sink.key('character', modifiers, data);
        return;
      }
    }
    const lines = data.split(LINE_BREAK);
    lines.forEach((line, index) => {
      if (index > 0) {
        this.sink.key('enter', NO_MODIFIERS);
      }
      const parts = line.split('\t');
      parts.forEach((part, partIndex) => {
        if (partIndex > 0) {
          this.sink.key('tab', NO_MODIFIERS);
        }
        const clean = part.replace(DISALLOWED_TEXT, '');
        if (clean !== '') {
          this.sink.text(clean);
        }
      });
    });
  }

  private consumeLatch(): Modifiers {
    const latch = { ...NO_MODIFIERS, ctrl: this.latched.ctrl, alt: this.latched.alt };
    if (this.latched.ctrl || this.latched.alt) {
      this.latched = { ctrl: false, alt: false };
      this.onLatchChange(this.latched);
    }
    return latch;
  }

  private clearField(): void {
    if (this.element !== undefined && !this.composing) {
      this.element.value = '';
    }
  }
}
