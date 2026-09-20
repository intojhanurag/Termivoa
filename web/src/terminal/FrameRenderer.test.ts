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

  it('requires resync for a wrong base revision without painting', async () => {
    const { terminal, renderer } = harness();
    const generation = '00000000-0000-4000-8000-000000000001';
    await renderer.paintSnapshot({
      generation,
      revision: '7',
      screen: metadata(),
      rows: [row('a'), row(), row(), row(), row()],
    });

    expect(() => renderer.applyPatch({
      generation,
      baseRevision: '6',
      revision: '8',
      screen: metadata(),
      rows: [{ index: 0, row: row('b') }],
    })).toThrow(ResyncRequiredError);
    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getChars()).toBe('a');
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
