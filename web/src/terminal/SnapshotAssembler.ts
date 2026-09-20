import type { ScreenMetadata, ScreenSnapshot, TerminalRow } from './FrameRenderer';
import { ResyncRequiredError } from './FrameRenderer';

export interface SnapshotBegin {
  readonly generation: string;
  readonly snapshotId: string;
  readonly revision: string;
  readonly screen: ScreenMetadata;
}

export interface SnapshotRows {
  readonly generation: string;
  readonly snapshotId: string;
  readonly revision: string;
  readonly startRow: number;
  readonly rows: ReadonlyArray<TerminalRow>;
}

export interface SnapshotEnd {
  readonly generation: string;
  readonly snapshotId: string;
  readonly revision: string;
  readonly rowCount: number;
}

interface Assembly {
  readonly begin: SnapshotBegin;
  readonly rows: Array<TerminalRow | undefined>;
  readonly received: boolean[];
}

/** Buffers snapshot chunks until one complete, internally consistent frame exists. */
export class SnapshotAssembler {
  private assembly: Assembly | undefined;

  begin(message: SnapshotBegin): void {
    this.assembly = {
      begin: copyBegin(message),
      rows: Array.from<TerminalRow | undefined>({ length: message.screen.rows }),
      received: Array.from<boolean>({ length: message.screen.rows }).fill(false),
    };
  }

  addRows(message: SnapshotRows): void {
    const assembly = this.match(message);
    if (
      !Number.isInteger(message.startRow)
      || message.startRow < 0
      || message.rows.length === 0
      || message.startRow + message.rows.length > assembly.rows.length
    ) {
      this.abort();
      throw new ResyncRequiredError('Snapshot row chunk is outside the declared screen');
    }
    for (let offset = 0; offset < message.rows.length; offset++) {
      const index = message.startRow + offset;
      if (assembly.received[index]) {
        this.abort();
        throw new ResyncRequiredError(`Snapshot row ${index} was received more than once`);
      }
      const row = message.rows[offset];
      if (row === undefined) {
        this.abort();
        throw new ResyncRequiredError(`Snapshot row ${index} is missing`);
      }
      assembly.rows[index] = copyRow(row);
      assembly.received[index] = true;
    }
  }

  end(message: SnapshotEnd): ScreenSnapshot {
    const assembly = this.match(message);
    if (message.rowCount !== assembly.rows.length || assembly.received.some((value) => !value)) {
      this.abort();
      throw new ResyncRequiredError('Snapshot ended before every row arrived');
    }
    const rows = assembly.rows.map((row, index) => {
      if (row === undefined) {
        throw new ResyncRequiredError(`Snapshot row ${index} is missing`);
      }
      return row;
    });
    const snapshot: ScreenSnapshot = {
      generation: assembly.begin.generation,
      revision: assembly.begin.revision,
      screen: assembly.begin.screen,
      rows,
    };
    this.assembly = undefined;
    return snapshot;
  }

  abort(): void {
    this.assembly = undefined;
  }

  isAssembling(): boolean {
    return this.assembly !== undefined;
  }

  private match(message: {
    readonly generation: string;
    readonly snapshotId: string;
    readonly revision: string;
  }): Assembly {
    const assembly = this.assembly;
    if (
      assembly === undefined
      || message.generation !== assembly.begin.generation
      || message.snapshotId !== assembly.begin.snapshotId
      || message.revision !== assembly.begin.revision
    ) {
      this.abort();
      throw new ResyncRequiredError('Snapshot chunk does not match the active transaction');
    }
    return assembly;
  }
}

function copyBegin(message: SnapshotBegin): SnapshotBegin {
  return {
    ...message,
    screen: {
      ...message.screen,
      cursor: { ...message.screen.cursor },
      foreground: { ...message.screen.foreground },
      background: { ...message.screen.background },
      cursorColor: { ...message.screen.cursorColor },
    },
  };
}

function copyRow(row: TerminalRow): TerminalRow {
  const [firstRun, ...remainingRuns] = row.runs;
  return {
    runs: [firstRun, ...remainingRuns].map((run) => ({
      ...run,
      style: {
        ...run.style,
        foreground: { ...run.style.foreground },
        background: { ...run.style.background },
        ...(run.style.underlineColor === undefined
          ? {}
          : { underlineColor: { ...run.style.underlineColor } }),
      },
      cells: run.cells.map((cell) => ({ ...cell })),
    })) as TerminalRow['runs'],
  };
}
