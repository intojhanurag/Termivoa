import { describe, expect, it } from 'vitest';
import type { CellStyle, ScreenMetadata, TerminalRow } from './FrameRenderer';
import { ResyncRequiredError } from './FrameRenderer';
import { SnapshotAssembler } from './SnapshotAssembler';

const black = { r: 0, g: 0, b: 0, a: 255 } as const;
const white = { r: 255, g: 255, b: 255, a: 255 } as const;
const style: CellStyle = {
  foreground: white, background: black, bold: false, faint: false,
  italic: false, blink: false, reverse: false, conceal: false,
  strikethrough: false, underline: 'none',
};

function metadata(): ScreenMetadata {
  return {
    columns: 20,
    rows: 5,
    buffer: 'normal',
    cursor: { column: 0, row: 0, style: 'block', visible: true, blink: false },
    foreground: white,
    background: black,
    cursorColor: white,
    widthMethod: 'unicode-graphemes',
    historyEpoch: '1',
    historyRevision: '0',
    contentClipped: false,
  };
}

function row(text: string): TerminalRow {
  const cells = Array.from({ length: 20 }, (_, index) => ({
    text: index === 0 ? text : ' ',
    width: 1 as const,
    continuation: false,
    contentClipped: false,
  })) as TerminalRow['runs'][number]['cells'];
  return {
    runs: [{
      column: 0,
      style,
      cells,
    }],
  };
}

const generation = '00000000-0000-4000-8000-000000000001';
const snapshotId = '00000000-0000-4000-8000-000000000002';

describe('SnapshotAssembler', () => {
  it('reveals rows only after a complete snapshot transaction', () => {
    const assembler = new SnapshotAssembler();
    assembler.begin({ generation, snapshotId, revision: '4', screen: metadata() });
    assembler.addRows({ generation, snapshotId, revision: '4', startRow: 2, rows: [row('c'), row('d'), row('e')] });
    expect(assembler.isAssembling()).toBe(true);
    assembler.addRows({ generation, snapshotId, revision: '4', startRow: 0, rows: [row('a'), row('b')] });

    const snapshot = assembler.end({ generation, snapshotId, revision: '4', rowCount: 5 });
    expect(snapshot.rows.map((value) => value.runs[0]?.cells[0]?.text)).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(assembler.isAssembling()).toBe(false);
  });

  it('rejects duplicate rows and abandons the transaction', () => {
    const assembler = new SnapshotAssembler();
    assembler.begin({ generation, snapshotId, revision: '4', screen: metadata() });
    assembler.addRows({ generation, snapshotId, revision: '4', startRow: 0, rows: [row('a')] });
    expect(() => assembler.addRows({ generation, snapshotId, revision: '4', startRow: 0, rows: [row('b')] }))
      .toThrow(ResyncRequiredError);
    expect(assembler.isAssembling()).toBe(false);
  });

  it('rejects wrong generation, revision, and snapshot identity', () => {
    for (const changed of [
      { generation: `${generation}x`, snapshotId, revision: '4' },
      { generation, snapshotId: `${snapshotId}x`, revision: '4' },
      { generation, snapshotId, revision: '5' },
    ]) {
      const assembler = new SnapshotAssembler();
      assembler.begin({ generation, snapshotId, revision: '4', screen: metadata() });
      expect(() => assembler.addRows({ ...changed, startRow: 0, rows: [row('a')] }))
        .toThrow(ResyncRequiredError);
      expect(assembler.isAssembling()).toBe(false);
    }
  });

  it('rejects an interrupted end with missing rows', () => {
    const assembler = new SnapshotAssembler();
    assembler.begin({ generation, snapshotId, revision: '4', screen: metadata() });
    assembler.addRows({ generation, snapshotId, revision: '4', startRow: 0, rows: [row('a')] });
    expect(() => assembler.end({ generation, snapshotId, revision: '4', rowCount: 5 }))
      .toThrow('before every row arrived');
    expect(assembler.isAssembling()).toBe(false);
  });
});
