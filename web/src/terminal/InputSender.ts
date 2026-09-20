import type {
  InputKeyPayload,
  InputMousePayload,
  Modifiers,
} from '../generated/protocol';

const MAX_INPUT_BYTES = 64 * 1024;
const MAX_PENDING_INPUTS = 256;
const MAX_PENDING_INPUT_BYTES = 256 * 1024;
const TEXT_CONTROLS = /[\u0000-\u001f\u007f-\u009f]/u;
const PASTE_CONTROLS = /[\u0000\u000b\u000c\u000e-\u001f\u007f-\u009f]/u;
const UNPAIRED_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u;

type InputKind = 'input.key' | 'input.text' | 'input.paste' | 'input.mouse';

export interface DeliveryUncertain {
  readonly inputSequence: string;
  readonly type: InputKind;
}

export interface InputTransport {
  readonly send: (message: string) => void;
  readonly close?: (reason: string) => void;
}

interface PendingInput extends DeliveryUncertain {
  readonly requestId: string;
  readonly bytes: number;
}

/**
 * Sends one lease's ordered input. It deliberately has no reconnect or retry
 * method: a new connection/controller lease must create a new sender.
 */
export class InputSender {
  private nextSequence = 1n;
  private readonly pending = new Map<string, PendingInput>();
  private pendingBytes = 0;
  private active = true;

  constructor(
    private readonly transport: InputTransport,
    private readonly sessionId: string,
    private readonly generation: string,
    private readonly lease: string,
    private readonly requestId: () => string = () => crypto.randomUUID(),
  ) {}

  sendKey(key: InputKeyPayload['key'], modifiers: Modifiers, character?: string): string {
    if (key === 'character') {
      if (
        character === undefined
        || !/^[\u0021-\u007e]$/u.test(character)
        || (!modifiers.ctrl && !modifiers.alt && !modifiers.meta)
      ) {
        throw new Error('Character keys require one printable ASCII character and a control modifier');
      }
    } else if (character !== undefined) {
      throw new Error('Only a character key may include character text');
    }
    const payload = { key, modifiers, ...(character === undefined ? {} : { character }) };
    return this.send('input.key', payload);
  }

  sendText(text: string): string {
    validateText(text, false);
    return this.send('input.text', { text });
  }

  sendPaste(text: string): string {
    validateText(text, true);
    return this.send('input.paste', { text });
  }

  sendMouse(
    kind: InputMousePayload['kind'],
    button: InputMousePayload['button'],
    column: number,
    row: number,
    modifiers: Modifiers,
  ): string {
    if (!Number.isInteger(column) || column < 0 || column >= 400 || !Number.isInteger(row) || row < 0 || row >= 200) {
      throw new Error('Mouse coordinates are outside terminal bounds');
    }
    const wheel = button === 'wheel-up' || button === 'wheel-down'
      || button === 'wheel-left' || button === 'wheel-right';
    const validPair = kind === 'wheel'
      ? wheel
      : kind === 'move'
        ? !wheel
        : kind === 'press'
          ? button !== 'none' && !wheel
          : !wheel;
    if (!validPair) {
      throw new Error('Mouse event kind and button are incompatible');
    }
    return this.send('input.mouse', { kind, button, column, row, modifiers });
  }

  accepted(sequence: string): void {
    this.removePending(sequence);
  }

  rejected(sequence: string | undefined): void {
    if (sequence !== undefined) {
      this.removePending(sequence);
    }
  }

  disconnect(): ReadonlyArray<DeliveryUncertain> {
    this.active = false;
    return [...this.pending.values()].map(({ inputSequence, type }) => ({ inputSequence, type }));
  }

  private send(type: InputKind, fields: object): string {
    if (!this.active) {
      throw new Error('Input is disabled; take control with a new connection before sending');
    }
    const inputSequence = this.nextSequence.toString(10);
    const requestId = this.requestId();
    const message = JSON.stringify({
      version: 1,
      type,
      requestId,
      sessionId: this.sessionId,
      generation: this.generation,
      payload: { ...fields, lease: this.lease, inputSequence },
    });
    const bytes = new TextEncoder().encode(message).byteLength;
    if (this.pending.size >= MAX_PENDING_INPUTS || this.pendingBytes + bytes > MAX_PENDING_INPUT_BYTES) {
      this.active = false;
      this.transport.close?.('input acknowledgements stalled');
      throw new InputBackpressureError(this.uncertainInputs());
    }

    this.nextSequence++;
    const pending: PendingInput = { inputSequence, type, requestId, bytes };
    this.pending.set(inputSequence, pending);
    this.pendingBytes += bytes;
    try {
      this.transport.send(message);
    } catch (error) {
      this.active = false;
      throw new DeliveryUncertainError(this.uncertainInputs(), error);
    }
    return inputSequence;
  }

  private removePending(sequence: string): void {
    const pending = this.pending.get(sequence);
    if (pending !== undefined) {
      this.pendingBytes -= pending.bytes;
      this.pending.delete(sequence);
    }
  }

  private uncertainInputs(): ReadonlyArray<DeliveryUncertain> {
    return [...this.pending.values()]
      .map(({ inputSequence, type }) => ({ inputSequence, type }));
  }
}

export class DeliveryUncertainError extends Error {
  readonly inputs: ReadonlyArray<DeliveryUncertain>;

  constructor(inputs: ReadonlyArray<DeliveryUncertain>, options?: unknown) {
    super('Delivery uncertain—check the terminal before sending again.', { cause: options });
    this.name = 'DeliveryUncertainError';
    this.inputs = inputs;
  }
}

export class InputBackpressureError extends DeliveryUncertainError {
  constructor(inputs: ReadonlyArray<DeliveryUncertain>) {
    super(inputs);
    this.name = 'InputBackpressureError';
    this.message = 'Input paused because acknowledgements stopped; check the terminal before reconnecting.';
  }
}

function validateText(text: string, paste: boolean): void {
  const bytes = new TextEncoder().encode(text).byteLength;
  const controls = paste ? PASTE_CONTROLS : TEXT_CONTROLS;
  if (bytes === 0 || bytes > MAX_INPUT_BYTES || UNPAIRED_SURROGATE.test(text) || controls.test(text)) {
    throw new Error(`${paste ? 'Paste' : 'Text'} input is invalid or exceeds ${MAX_INPUT_BYTES} bytes`);
  }
}
