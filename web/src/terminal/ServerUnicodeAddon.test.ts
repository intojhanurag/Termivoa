import { describe, expect, it } from 'vitest';
import { Terminal } from '@xterm/xterm';
import { ServerUnicodeAddon } from './ServerUnicodeAddon';

interface WidthFixture {
  readonly name: string;
  readonly text: string;
  readonly width: 1 | 2;
}

const fixtures: ReadonlyArray<WidthFixture> = [
  { name: 'ascii', text: 'A', width: 1 },
  { name: 'CJK', text: '界', width: 2 },
  { name: 'combining accent', text: 'e\u0301', width: 1 },
  { name: 'variation selector', text: '☕️', width: 2 },
  { name: 'rainbow flag', text: '🏳️‍🌈', width: 2 },
  { name: 'woman technologist', text: '👩‍💻', width: 2 },
  { name: 'family ZWJ sequence', text: '👨‍👩‍👦', width: 2 },
];

function write(terminal: Terminal, text: string): Promise<void> {
  return new Promise((resolve) => terminal.write(text, resolve));
}

describe('ServerUnicodeAddon', () => {
  for (const fixture of fixtures) {
    it(`uses the authoritative width for ${fixture.name}`, async () => {
      const terminal = new Terminal({
        allowProposedApi: true,
        cols: 20,
        rows: 5,
        disableStdin: true,
        scrollback: 0,
      });
      const addon = new ServerUnicodeAddon();
      terminal.loadAddon(addon);

      addon.prepareCell(fixture.width);
      try {
        await write(terminal, fixture.text);
      } finally {
        addon.clearCell();
      }

      expect(terminal.buffer.active.cursorX).toBe(fixture.width);
      expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getChars()).toBe(fixture.text);
      expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getWidth()).toBe(fixture.width);
      terminal.dispose();
    });
  }

  it('supports the wcwidth rainbow fixture supplied by the server', async () => {
    const terminal = new Terminal({
      allowProposedApi: true,
      cols: 20,
      rows: 5,
      disableStdin: true,
      scrollback: 0,
    });
    const addon = new ServerUnicodeAddon();
    terminal.loadAddon(addon);

    addon.prepareCell(1);
    try {
      await write(terminal, '🏳️‍🌈');
    } finally {
      addon.clearCell();
    }

    expect(terminal.buffer.active.cursorX).toBe(1);
    expect(terminal.buffer.active.getLine(0)?.getCell(0)?.getWidth()).toBe(1);
    terminal.dispose();
  });

  it('rejects widths xterm cannot represent', () => {
    const terminal = new Terminal({ allowProposedApi: true, cols: 20, rows: 5 });
    const addon = new ServerUnicodeAddon();
    terminal.loadAddon(addon);

    expect(() => addon.prepareCell(3)).toThrow('Unsupported terminal cell width 3');
    terminal.dispose();
  });
});
