import { UnicodeGraphemesAddon } from '@xterm/addon-unicode-graphemes';
import type {
  ITerminalAddon,
  IUnicodeHandling,
  IUnicodeVersionProvider,
  Terminal,
} from '@xterm/xterm';

const WIDTH_MASK = 0b110;
const WIDTH_SHIFT = 1;
const KIND_MASK = 0x0f;
const KIND_SHIFT = 3;
const KIND_PREPEND = 1;
const KIND_ZWJ = 10;
const KIND_EXTENDED_PICTOGRAPHIC = 11;
const JOINED_STATE_OFFSET = 16;

const extendedPictographic = /\p{Extended_Pictographic}/u;

type CellWidth = 1 | 2;

function propertyWidth(properties: number): number {
  return (properties & WIDTH_MASK) >> WIDTH_SHIFT;
}

function propertyKind(properties: number): number {
  return (properties >> KIND_SHIFT) & KIND_MASK;
}

function withWidth(properties: number, width: CellWidth): number {
  return (properties & ~WIDTH_MASK) | (width << WIDTH_SHIFT);
}

function properties(kind: number, width: CellWidth, joinsPrevious: boolean): number {
  return ((kind & 0x00ff_ffff) << KIND_SHIFT)
    | (width << WIDTH_SHIFT)
    | (joinsPrevious ? 1 : 0);
}

/**
 * The pinned addon omits Extended_Pictographic metadata for several common
 * emoji in its published trie. Preserve its other Unicode behavior while
 * repairing GB11 ZWJ joining for server-supplied normalized cells.
 */
class ServerCellProvider implements IUnicodeVersionProvider {
  readonly version = 'phone-terminal-server-cells';

  private expectedWidth: CellWidth | undefined;

  constructor(private readonly delegate: IUnicodeVersionProvider) {}

  prepare(width: number): void {
    if (width !== 1 && width !== 2) {
      throw new Error(`Unsupported terminal cell width ${width}`);
    }
    this.expectedWidth = width;
  }

  clear(): void {
    this.expectedWidth = undefined;
  }

  wcwidth(codepoint: number): 0 | 1 | 2 {
    return this.expectedWidth ?? this.delegate.wcwidth(codepoint);
  }

  charProperties(codepoint: number, preceding: number): number {
    let result = this.delegate.charProperties(codepoint, preceding);

    if (extendedPictographic.test(String.fromCodePoint(codepoint))) {
      const precedingKind = propertyKind(preceding);
      const joinsPrevious = precedingKind === KIND_ZWJ || precedingKind === KIND_PREPEND;
      const width = this.expectedWidth
        ?? Math.max(1, propertyWidth(result), joinsPrevious ? propertyWidth(preceding) : 1);
      const boundedWidth: CellWidth = width >= 2 ? 2 : 1;
      const kind = joinsPrevious
        ? KIND_EXTENDED_PICTOGRAPHIC + JOINED_STATE_OFFSET
        : KIND_EXTENDED_PICTOGRAPHIC;
      result = properties(kind, boundedWidth, joinsPrevious);
    }

    if (this.expectedWidth !== undefined) {
      result = withWidth(result, this.expectedWidth);
    }
    return result;
  }
}

class CapturingUnicodeHandling implements IUnicodeHandling {
  readonly providers = new Map<string, IUnicodeVersionProvider>();

  constructor(private readonly delegate: IUnicodeHandling) {}

  get activeVersion(): string {
    return this.delegate.activeVersion;
  }

  set activeVersion(value: string) {
    this.delegate.activeVersion = value;
  }

  get versions(): ReadonlyArray<string> {
    return this.delegate.versions;
  }

  register(provider: IUnicodeVersionProvider): void {
    this.providers.set(provider.version, provider);
    this.delegate.register(provider);
  }
}

/**
 * Loads the pinned grapheme addon, then adds a narrow provider that makes each
 * normalized server cell occupy its authoritative width in xterm. The painter
 * must serialize writes and bracket each write of same-width cells with
 * prepare/clear.
 */
export class ServerUnicodeAddon implements ITerminalAddon {
  private readonly graphemes = new UnicodeGraphemesAddon();
  private provider: ServerCellProvider | undefined;
  private unicode: IUnicodeHandling | undefined;
  private previousVersion: string | undefined;

  activate(terminal: Terminal): void {
    const unicode = terminal.unicode;
    const capturing = new CapturingUnicodeHandling(unicode);
    const terminalProxy = { unicode: capturing } as unknown as Terminal;

    this.previousVersion = unicode.activeVersion;
    this.graphemes.activate(terminalProxy);

    const base = capturing.providers.get('15-graphemes');
    if (base === undefined) {
      this.graphemes.dispose();
      throw new Error('Pinned Unicode grapheme provider was not registered');
    }

    this.provider = new ServerCellProvider(base);
    this.unicode = unicode;
    unicode.register(this.provider);
    unicode.activeVersion = this.provider.version;
  }

  prepareCell(width: number): void {
    if (this.provider === undefined) {
      throw new Error('ServerUnicodeAddon is not active');
    }
    this.provider.prepare(width);
  }

  clearCell(): void {
    this.provider?.clear();
  }

  dispose(): void {
    this.provider?.clear();
    this.graphemes.dispose();
    if (
      this.unicode !== undefined
      && this.previousVersion !== undefined
      && this.unicode.versions.includes(this.previousVersion)
    ) {
      this.unicode.activeVersion = this.previousVersion;
    }
    this.provider = undefined;
    this.unicode = undefined;
    this.previousVersion = undefined;
  }
}
