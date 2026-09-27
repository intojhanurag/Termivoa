import type { Terminal } from '@xterm/xterm';
import type {
  Cell as ProtocolCell,
  CellRun as ProtocolCellRun,
  CellStyle as ProtocolCellStyle,
  ChangedRow as ProtocolChangedRow,
  Color as ProtocolColor,
  Cursor as ProtocolCursor,
  ScreenMetadata as ProtocolScreenMetadata,
  TerminalRow as ProtocolTerminalRow,
} from '../generated/protocol';
import { ServerUnicodeAddon } from './ServerUnicodeAddon';

const CONTROL_TEXT = /[\u0000-\u001f\u007f-\u009f]/u;
const DECIMAL_COUNTER = /^(0|[1-9][0-9]{0,19})$/u;
const MAX_CELL_BYTES = 1024;
const MAX_PENDING_PAINTS = 2;
const encoder = new TextEncoder();

export type Color = ProtocolColor;
export type CellStyle = ProtocolCellStyle;
export type Cell = ProtocolCell;
export type CellRun = ProtocolCellRun;
export type TerminalRow = ProtocolTerminalRow;
export type Cursor = ProtocolCursor;
export type ScreenMetadata = ProtocolScreenMetadata;
export type ChangedRow = ProtocolChangedRow;

export interface ScreenSnapshot {
  readonly generation: string;
  readonly revision: string;
  readonly screen: ScreenMetadata;
  readonly rows: ReadonlyArray<TerminalRow>;
}

export interface ScreenPatch {
  readonly generation: string;
  readonly baseRevision: string;
  readonly revision: string;
  readonly screen: ScreenMetadata;
  readonly rows: ReadonlyArray<ChangedRow>;
}

interface PaintCell {
  readonly column: number;
  readonly width: 1 | 2;
  readonly text: string;
}

interface PaintRun {
  readonly style: string;
  readonly cells: ReadonlyArray<PaintCell>;
}

interface PaintRow {
  readonly index: number;
  readonly runs: ReadonlyArray<PaintRun>;
}

export class ResyncRequiredError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ResyncRequiredError';
  }
}

/**
 * Owns all xterm writes. React should hold this controller by reference and
 * must not mirror screen rows into component state.
 */
export class FrameRenderer {
  private queue: Promise<void> = Promise.resolve();
  private pendingPaints = 0;
  private generation: string | undefined;
  private revision: string | undefined;
  private columns: number | undefined;
  private rows: number | undefined;

  constructor(
    private readonly terminal: Terminal,
    private readonly unicode: ServerUnicodeAddon,
  ) {}

  paintSnapshot(snapshot: ScreenSnapshot): Promise<void> {
    validateCounter(snapshot.revision, 'revision');
    validateMetadata(snapshot.screen);
    if (snapshot.rows.length !== snapshot.screen.rows) {
      throw new ResyncRequiredError('Snapshot row count does not match its dimensions');
    }
    const rows = snapshot.rows.map((row, index) => compileRow(row, index, snapshot.screen.columns));
    const generation = snapshot.generation;
    const revision = snapshot.revision;
    const screen = copyMetadata(snapshot.screen);

    return this.enqueue(async () => {
      if (this.terminal.cols !== screen.columns || this.terminal.rows !== screen.rows) {
        this.terminal.resize(screen.columns, screen.rows);
      }
      await this.paint(rows, screen);
      this.generation = generation;
      this.revision = revision;
      this.columns = screen.columns;
      this.rows = screen.rows;
    });
  }

  applyPatch(patch: ScreenPatch): Promise<void> {
    validateCounter(patch.baseRevision, 'baseRevision');
    validateCounter(patch.revision, 'revision');
    validateMetadata(patch.screen);
    if (BigInt(patch.revision) <= BigInt(patch.baseRevision)) {
      throw new ResyncRequiredError('Patch revision does not advance');
    }
    const seen = new Set<number>();
    const rows = patch.rows.map(({ index, row }) => {
      if (!Number.isInteger(index) || index < 0 || index >= patch.screen.rows || seen.has(index)) {
        throw new ResyncRequiredError(`Invalid or duplicate patch row ${index}`);
      }
      seen.add(index);
      return compileRow(row, index, patch.screen.columns);
    });
    const revision = patch.revision;
    const screen = copyMetadata(patch.screen);

    return this.enqueue(async () => {
      if (patch.generation !== this.generation) {
        throw new ResyncRequiredError('Patch generation does not match the displayed session');
      }
      if (patch.baseRevision !== this.revision) {
        throw new ResyncRequiredError('Patch base revision does not match the displayed revision');
      }
      if (patch.screen.columns !== this.columns || patch.screen.rows !== this.rows) {
        throw new ResyncRequiredError('A resize requires a full snapshot');
      }
      await this.paint(rows, screen);
      this.revision = revision;
    });
  }

  displayedRevision(): string | undefined {
    return this.revision;
  }

  private enqueue(work: () => Promise<void>): Promise<void> {
    if (this.pendingPaints >= MAX_PENDING_PAINTS) {
      return Promise.reject(new ResyncRequiredError('Display is lagging; request a fresh snapshot'));
    }
    this.pendingPaints++;
    const next = this.queue.then(async () => {
      try {
        await work();
      } finally {
        this.pendingPaints--;
      }
    });
    this.queue = next.catch(() => undefined);
    return next;
  }

  /**
   * Builds the whole frame as one drawing string per authoritative cell
   * width, so a frame costs at most two xterm writes. The width override is
   * read while xterm parses, so each width group is its own awaited write.
   */
  private async paint(rows: ReadonlyArray<PaintRow>, screen: ScreenMetadata): Promise<void> {
    this.terminal.options.theme = {
      foreground: colorHex(screen.foreground),
      background: colorHex(screen.background),
      cursor: colorHex(screen.cursorColor),
    };

    let narrow = '\u001b[?2026h\u001b[?25l\u001b[?7l';
    for (const row of rows) {
      narrow += `${cup(row.index, 0)}\u001b[2K`;
    }
    let wide = '';
    for (const row of rows) {
      for (const run of row.runs) {
        let narrowRun = '';
        let wideRun = '';
        for (const cell of run.cells) {
          const drawn = `${cup(row.index, cell.column)}${cell.text}`;
          if (cell.width === 1) {
            narrowRun += drawn;
          } else {
            wideRun += drawn;
          }
        }
        if (narrowRun !== '') {
          narrow += run.style + narrowRun;
        }
        if (wideRun !== '') {
          wide += run.style + wideRun;
        }
      }
    }
    const cursor = screen.cursor;
    const cursorVisibility = cursor.visible ? '\u001b[?25h' : '\u001b[?25l';
    const restore = `\u001b[0m${cup(cursor.row, cursor.column)}\u001b[${cursorStyle(cursor)} q${cursorVisibility}\u001b[?7h\u001b[?2026l`;

    if (wide === '') {
      await this.writeCells(1, narrow + restore);
      return;
    }
    await this.writeCells(1, narrow);
    await this.writeCells(2, wide + restore);
  }

  private async writeCells(width: 1 | 2, content: string): Promise<void> {
    this.unicode.prepareCell(width);
    try {
      await write(this.terminal, content);
    } finally {
      this.unicode.clearCell();
    }
  }
}

function compileRow(row: TerminalRow, index: number, columns: number): PaintRow {
  let expectedColumn = 0;
  const runs = row.runs.map((run) => {
    if (!Number.isInteger(run.column) || run.column !== expectedColumn || run.cells.length === 0) {
      throw new ResyncRequiredError(`Row ${index} has a non-contiguous run`);
    }
    const cells: PaintCell[] = [];
    for (let offset = 0; offset < run.cells.length; offset++) {
      const cell = run.cells[offset];
      if (cell === undefined) {
        throw new ResyncRequiredError(`Row ${index} has a missing cell`);
      }
      const column = run.column + offset;
      validateCell(cell, row, run, offset, index);
      if (!cell.continuation) {
        cells.push({ column, width: cell.width as 1 | 2, text: cell.text });
      }
      expectedColumn++;
    }
    return { style: styleSequence(run.style), cells };
  });
  if (expectedColumn !== columns) {
    throw new ResyncRequiredError(`Row ${index} covers ${expectedColumn} columns, expected ${columns}`);
  }
  return { index, runs };
}

function validateCell(
  cell: Cell,
  row: TerminalRow,
  run: CellRun,
  offset: number,
  rowIndex: number,
): void {
  if (cell.continuation) {
    if (cell.width !== 0 || cell.text !== '') {
      throw new ResyncRequiredError(`Row ${rowIndex} has an invalid continuation cell`);
    }
    const previous = offset > 0 ? run.cells[offset - 1] : previousRunCell(row, run);
    if (previous?.width !== 2 || previous.continuation) {
      throw new ResyncRequiredError(`Row ${rowIndex} has an orphan continuation cell`);
    }
    return;
  }
  if (cell.width !== 1 && cell.width !== 2) {
    throw new ResyncRequiredError(`Row ${rowIndex} has unsupported cell width ${cell.width}`);
  }
  if (cell.text === '' || CONTROL_TEXT.test(cell.text)) {
    throw new ResyncRequiredError(`Row ${rowIndex} contains unsafe printable content`);
  }
  if (encoder.encode(cell.text).byteLength > MAX_CELL_BYTES) {
    throw new ResyncRequiredError(`Row ${rowIndex} contains oversized cell content`);
  }
  if (cell.width === 2) {
    const next = run.cells[offset + 1] ?? nextRunCell(row, run);
    if (next === undefined || !next.continuation) {
      throw new ResyncRequiredError(`Row ${rowIndex} has a wide cell without a continuation`);
    }
  }
}

function previousRunCell(row: TerminalRow, run: CellRun): Cell | undefined {
  const index = row.runs.indexOf(run);
  return index > 0 ? row.runs[index - 1]?.cells.at(-1) : undefined;
}

function nextRunCell(row: TerminalRow, run: CellRun): Cell | undefined {
  const index = row.runs.indexOf(run);
  return index >= 0 ? row.runs[index + 1]?.cells[0] : undefined;
}

function validateCounter(value: string, name: string): void {
  if (!DECIMAL_COUNTER.test(value)) {
    throw new ResyncRequiredError(`${name} is not a canonical decimal counter`);
  }
}

function validateMetadata(screen: ScreenMetadata): void {
  if (!Number.isInteger(screen.columns) || screen.columns < 20 || screen.columns > 400) {
    throw new ResyncRequiredError('Screen columns are out of bounds');
  }
  if (!Number.isInteger(screen.rows) || screen.rows < 5 || screen.rows > 200) {
    throw new ResyncRequiredError('Screen rows are out of bounds');
  }
  if (
    !Number.isInteger(screen.cursor.column)
    || !Number.isInteger(screen.cursor.row)
    || screen.cursor.column < 0
    || screen.cursor.column >= screen.columns
    || screen.cursor.row < 0
    || screen.cursor.row >= screen.rows
  ) {
    throw new ResyncRequiredError('Cursor is outside the screen');
  }
  validateCounter(screen.historyEpoch, 'historyEpoch');
  validateCounter(screen.historyRevision, 'historyRevision');
  for (const color of [screen.foreground, screen.background, screen.cursorColor]) {
    validateColor(color);
  }
}

function validateColor(color: Color): void {
  for (const component of [color.r, color.g, color.b, color.a]) {
    if (!Number.isInteger(component) || component < 0 || component > 255) {
      throw new ResyncRequiredError('Color component is out of bounds');
    }
  }
}

function copyMetadata(screen: ScreenMetadata): ScreenMetadata {
  return {
    ...screen,
    cursor: { ...screen.cursor },
    foreground: { ...screen.foreground },
    background: { ...screen.background },
    cursorColor: { ...screen.cursorColor },
  };
}

function styleSequence(style: CellStyle): string {
  validateColor(style.foreground);
  validateColor(style.background);
  if (style.underlineColor !== undefined) {
    validateColor(style.underlineColor);
  }
  const values = [
    0,
    style.bold ? 1 : undefined,
    style.faint ? 2 : undefined,
    style.italic ? 3 : undefined,
    underlineCode(style.underline),
    style.blink ? 5 : undefined,
    style.reverse ? 7 : undefined,
    style.conceal ? 8 : undefined,
    style.strikethrough ? 9 : undefined,
    `38;2;${style.foreground.r};${style.foreground.g};${style.foreground.b}`,
    `48;2;${style.background.r};${style.background.g};${style.background.b}`,
    style.underlineColor === undefined
      ? undefined
      : `58;2;${style.underlineColor.r};${style.underlineColor.g};${style.underlineColor.b}`,
  ].filter((value): value is number | string => value !== undefined);
  return `\u001b[${values.join(';')}m`;
}

function underlineCode(value: CellStyle['underline']): string | undefined {
  switch (value) {
    case 'none': return undefined;
    case 'single': return '4';
    case 'double': return '4:2';
    case 'curly': return '4:3';
    case 'dotted': return '4:4';
    case 'dashed': return '4:5';
  }
}

function cursorStyle(cursor: Cursor): number {
  if (cursor.style === 'underline') {
    return cursor.blink ? 3 : 4;
  }
  if (cursor.style === 'bar') {
    return cursor.blink ? 5 : 6;
  }
  return cursor.blink ? 1 : 2;
}

function colorHex(color: Color): string {
  validateColor(color);
  return `#${hex(color.r)}${hex(color.g)}${hex(color.b)}`;
}

function hex(value: number): string {
  return value.toString(16).padStart(2, '0');
}

function cup(row: number, column: number): string {
  return `\u001b[${row + 1};${column + 1}H`;
}

function write(terminal: Terminal, content: string): Promise<void> {
  return new Promise((resolve) => terminal.write(content, resolve));
}
