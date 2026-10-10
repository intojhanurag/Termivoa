import { describe, expect, it } from 'vitest';
import { Terminal } from '@xterm/xterm';
import {
  FrameRenderer,
  ResyncRequiredError,
  type CellStyle,
  type ScreenMetadata,
  type TerminalRow,
} from './FrameRenderer';
import { ServerUnicodeAddon } from './ServerUnicodeAddon';

const black = { r: 0, g: 0, b: 0, a: 255 } as const;
const white = { r: 255, g: 255, b: 255, a: 255 } as const;
const style: CellStyle = {
  foreground: white,
  background: black,
  bold: false,
  faint: false,
  italic: false,
  blink: false,
  reverse: false,
  conceal: false,
  strikethrough: false,
  underline: 'none',
};

function metadata(cursorColumn = 0): ScreenMetadata {
  return {
    columns: 20,
    rows: 5,
    buffer: 'normal',
    cursor: { column: cursorColumn, row: 0, style: 'block', visible: true, blink: false },
    foreground: white,
    background: black,
    cursorColor: white,
    widthMethod: 'unicode-graphemes',
    historyEpoch: '1',
    historyRevision: '0',
    contentClipped: false,
  };
}

function row(text = '', width: 1 | 2 = 1): TerminalRow {
  const cells: TerminalRow['runs'][number]['cells'][number][] = [];
  if (text !== '') {
    cells.push({ text, width, continuation: false, contentClipped: false });
    if (width === 2) {
      cells.push({ text: '', width: 0, continuation: true, contentClipped: false });
    }
  }
  while (cells.length < 20) {
    cells.push({ text: ' ', width: 1, continuation: false, contentClipped: false });
  }
  return { runs: [{ column: 0, style, cells: cells as TerminalRow['runs'][number]['cells'] }] };
}

function harness() {
  const terminal = new Terminal({
    allowProposedApi: true,
    cols: 20,
    rows: 5,
    disableStdin: true,
    scrollback: 0,
  });
  const unicode = new ServerUnicodeAddon();
  terminal.loadAddon(unicode);
  return { terminal, renderer: new FrameRenderer(terminal, unicode) };
}

describe('FrameRenderer', () => {
  it('paints a complete snapshot with authoritative Unicode widths', async () => {
    const { terminal, renderer } = harness();
    const input: string[] = [];
    terminal.onData((data) => input.push(data));

    await renderer.paintSnapshot({
      generation: '00000000-0000-4000-8000-000000000001',
      revision: '1',
      screen: metadata(3),
      rows: [row('🏳️‍🌈', 2), row('e\u0301'), row('界', 2), row(), row()],
    });

    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getChars()).toBe('🏳️‍🌈');
    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getWidth()).toBe(2);
    expect(terminal.buffer.active.getLine(1)?.getCell(0)?.getChars()).toBe('e\u0301');
    expect(terminal.buffer.active.cursorX).toBe(3);
    expect(renderer.displayedRevision()).toBe('1');
    expect(input).toEqual([]);
    terminal.dispose();
  });

  it('draws the default black background as the app ink, and other colours as sent', async () => {
    const { terminal, renderer } = harness();
    const red = { r: 200, g: 0, b: 0, a: 255 } as const;
    const tinted: TerminalRow = { runs: [{ ...row('a').runs[0]!, style: { ...style, background: red } }] };
    await renderer.paintSnapshot({
      generation: '00000000-0000-4000-8000-000000000001',
      revision: '1',
      screen: metadata(),
      rows: [row('a'), tinted, row(), row(), row()],
    });

    expect(terminal.options.theme?.background).toBe('#07090c');
    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getBgColor()).toBe(0x07090c);
    expect(terminal.buffer.active.getLine(1)?.getCell(0)?.getBgColor()).toBe(0xc80000);
    terminal.dispose();
  });

  it('shows the new frame from its top row after a resize that narrows full rows (#287)', async () => {
    const { terminal, renderer } = harness();
    const full = (text: string, columns: number): TerminalRow => ({
      runs: [{ column: 0, style, cells: [...text.padEnd(columns, 'x')].map((cell) => ({ text: cell, width: 1, continuation: false, contentClipped: false })) as TerminalRow['runs'][number]['cells'] }],
    });
    const frame = (revision: string, columns: number, rows: number) => ({
      generation: '00000000-0000-4000-8000-000000000001',
      revision,
      screen: { ...metadata(), columns, rows, cursor: { ...metadata().cursor, row: rows - 1 } },
      rows: Array.from({ length: rows }, (_, index) => full(`r${index}`, columns)),
    });
    // A bigger font: fewer columns. xterm would rewrap the old full rows and push one past its base.
    await renderer.paintSnapshot(frame('1', 24, 5));
    await renderer.paintSnapshot(frame('2', 20, 7));

    const { active } = terminal.buffer;
    expect([active.baseY, active.viewportY]).toEqual([0, 0]);
    const shown = Array.from({ length: 7 }, (_, index) => active.getLine(active.viewportY + index)?.translateToString(true).slice(0, 2));
    expect(shown).toEqual(['r0', 'r1', 'r2', 'r3', 'r4', 'r5', 'r6']);
    terminal.dispose();
  });

  it('applies a matching whole-row patch', async () => {
    const { terminal, renderer } = harness();
    const generation = '00000000-0000-4000-8000-000000000001';
    await renderer.paintSnapshot({
      generation,
      revision: '9007199254740993',
      screen: metadata(),
      rows: [row('a'), row(), row(), row(), row()],
    });

    await renderer.applyPatch({
      generation,
      baseRevision: '9007199254740993',
      revision: '9007199254740994',
      screen: metadata(1),
      rows: [{ index: 0, row: row('b') }],
    });

    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getChars()).toBe('b');
    expect(renderer.displayedRevision()).toBe('9007199254740994');
    terminal.dispose();
  });

  it('serializes a snapshot and consecutive patches without caller-side awaiting', async () => {
    const { terminal, renderer } = harness();
    const generation = '00000000-0000-4000-8000-000000000001';
    await renderer.paintSnapshot({
      generation,
      revision: '1',
      screen: metadata(),
      rows: [row('a'), row(), row(), row(), row()],
    });
    const first = renderer.applyPatch({
      generation,
      baseRevision: '1',
      revision: '2',
      screen: metadata(),
      rows: [{ index: 0, row: row('b') }],
    });
    const second = renderer.applyPatch({
      generation,
      baseRevision: '2',
      revision: '3',
      screen: metadata(),
      rows: [{ index: 0, row: row('c') }],
    });

    await Promise.all([first, second]);
    expect(renderer.displayedRevision()).toBe('3');
    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getChars()).toBe('c');
    terminal.dispose();
  });

  it('rechecks a queued patch base after prior work advances the display', async () => {
    const { terminal, renderer } = harness();
    const generation = '00000000-0000-4000-8000-000000000001';
    await renderer.paintSnapshot({
      generation,
      revision: '1',
      screen: metadata(),
      rows: [row('a'), row(), row(), row(), row()],
    });
    const first = renderer.applyPatch({
      generation,
      baseRevision: '1',
      revision: '2',
      screen: metadata(),
      rows: [{ index: 0, row: row('b') }],
    });
    const stale = renderer.applyPatch({
      generation,
      baseRevision: '1',
      revision: '3',
      screen: metadata(),
      rows: [{ index: 0, row: row('c') }],
    });

    await first;
    await expect(stale).rejects.toThrow(ResyncRequiredError);
    expect(renderer.displayedRevision()).toBe('2');
    terminal.dispose();
  });

  it('bounds retained paint work when xterm stalls', async () => {
    const { terminal, renderer } = harness();
    const generation = '00000000-0000-4000-8000-000000000001';
    const originalWrite = terminal.write.bind(terminal);
    let release: (() => void) | undefined;
    terminal.write = ((_data: string | Uint8Array, callback?: () => void) => {
      release = callback;
    }) as typeof terminal.write;

    const snapshot = renderer.paintSnapshot({
      generation,
      revision: '1',
      screen: metadata(),
      rows: [row('a'), row(), row(), row(), row()],
    });
    await Promise.resolve();
    await Promise.resolve();
    expect(release).toBeTypeOf('function');

    const queued = renderer.applyPatch({
      generation,
      baseRevision: '1',
      revision: '2',
      screen: metadata(),
      rows: [{ index: 0, row: row('b') }],
    });
    const overflow = renderer.applyPatch({
      generation,
      baseRevision: '2',
      revision: '3',
      screen: metadata(),
      rows: [{ index: 0, row: row('c') }],
    });
    await expect(overflow).rejects.toThrow('Display is lagging');

    terminal.write = originalWrite;
    release?.();
    await Promise.all([snapshot, queued]);
    expect(renderer.displayedRevision()).toBe('2');
    terminal.dispose();
  });

  it('requires resync for a wrong base revision without painting', async () => {
    const { terminal, renderer } = harness();
    const generation = '00000000-0000-4000-8000-000000000001';
    await renderer.paintSnapshot({
      generation,
      revision: '7',
      screen: metadata(),
      rows: [row('a'), row(), row(), row(), row()],
    });

    await expect(renderer.applyPatch({
      generation,
      baseRevision: '6',
      revision: '8',
      screen: metadata(),
      rows: [{ index: 0, row: row('b') }],
    })).rejects.toThrow(ResyncRequiredError);
    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getChars()).toBe('a');
    terminal.dispose();
  });

  it('paints a large colorful snapshot in a bounded number of xterm writes', async () => {
    const columns = 200;
    const height = 60;
    const terminal = new Terminal({
      allowProposedApi: true,
      cols: columns,
      rows: height,
      disableStdin: true,
      scrollback: 0,
    });
    const unicode = new ServerUnicodeAddon();
    terminal.loadAddon(unicode);
    const renderer = new FrameRenderer(terminal, unicode);
    let writes = 0;
    const originalWrite = terminal.write.bind(terminal);
    terminal.write = ((data: string | Uint8Array, callback?: () => void) => {
      writes++;
      originalWrite(data, callback);
    }) as typeof terminal.write;

    const runsPerRow = 20;
    const runWidth = columns / runsPerRow;
    const rows: TerminalRow[] = Array.from({ length: height }, (_, rowIndex) => ({
      runs: Array.from({ length: runsPerRow }, (_, runIndex) => ({
        column: runIndex * runWidth,
        style: { ...style, foreground: { r: runIndex * 12, g: rowIndex, b: 200, a: 255 }, bold: runIndex % 2 === 0 },
        cells: Array.from({ length: runWidth }, (_, offset) => ({
          text: String.fromCharCode(97 + ((runIndex + offset) % 26)),
          width: 1,
          continuation: false,
          contentClipped: false,
        })),
      })),
    })) as unknown as TerminalRow[];

    const started = performance.now();
    await renderer.paintSnapshot({
      generation: '00000000-0000-4000-8000-000000000001',
      revision: '1',
      screen: { ...metadata(), columns, rows: height },
      rows,
    });
    const elapsed = performance.now() - started;

    expect(writes).toBeLessThanOrEqual(2);
    expect(elapsed).toBeLessThan(1000);
    expect(terminal.buffer.active.getLine(59)?.getCell(199)?.getChars()).toBe('c');
    expect(terminal.buffer.active.getLine(0)?.getCell(10)?.isBold()).toBe(0);
    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.isBold()).not.toBe(0);
    terminal.dispose();
  });

  it('paints mixed narrow and wide cells and patches with at most two writes each', async () => {
    const { terminal, renderer } = harness();
    let writes = 0;
    const originalWrite = terminal.write.bind(terminal);
    terminal.write = ((data: string | Uint8Array, callback?: () => void) => {
      writes++;
      originalWrite(data, callback);
    }) as typeof terminal.write;
    const generation = '00000000-0000-4000-8000-000000000001';

    await renderer.paintSnapshot({
      generation,
      revision: '1',
      screen: metadata(2),
      rows: [row('界', 2), row('e\u0301'), row('👩‍💻', 2), row('x'), row()],
    });
    expect(writes).toBeLessThanOrEqual(2);
    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getWidth()).toBe(2);
    expect(terminal.buffer.active.getLine(0)?.getCell(2)?.getChars()).toBe(' ');
    expect(terminal.buffer.active.getLine(1)?.getCell(0)?.getChars()).toBe('e\u0301');
    expect(terminal.buffer.active.getLine(2)?.getCell(0)?.getChars()).toBe('👩‍💻');
    expect(terminal.buffer.active.getLine(2)?.getCell(0)?.getWidth()).toBe(2);
    expect(terminal.buffer.active.cursorX).toBe(2);

    writes = 0;
    await renderer.applyPatch({
      generation,
      baseRevision: '1',
      revision: '2',
      screen: metadata(),
      rows: [{ index: 0, row: row('a') }, { index: 3, row: row('界', 2) }],
    });
    expect(writes).toBeLessThanOrEqual(2);
    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getChars()).toBe('a');
    expect(terminal.buffer.active.getLine(0)?.getCell(1)?.getChars()).toBe(' ');
    expect(terminal.buffer.active.getLine(3)?.getCell(0)?.getWidth()).toBe(2);
    terminal.dispose();
  });

  it('rejects escape and control content before painting', () => {
    const { terminal, renderer } = harness();
    expect(() => renderer.paintSnapshot({
      generation: '00000000-0000-4000-8000-000000000001',
      revision: '1',
      screen: metadata(),
      rows: [row('\u001b]52;bad'), row(), row(), row(), row()],
    })).toThrow('unsafe printable content');
    expect(renderer.displayedRevision()).toBeUndefined();
    terminal.dispose();
  });

  it('rejects an interrupted or incomplete snapshot before painting', () => {
    const { terminal, renderer } = harness();
    expect(() => renderer.paintSnapshot({
      generation: '00000000-0000-4000-8000-000000000001',
      revision: '1',
      screen: metadata(),
      rows: [row()],
    })).toThrow('Snapshot row count');
    terminal.dispose();
  });
});
