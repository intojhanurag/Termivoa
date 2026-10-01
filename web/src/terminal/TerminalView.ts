import { Terminal, type IBufferCell, type IDisposable } from '@xterm/xterm';
import { clampGrid } from './Connection';
import { FrameRenderer } from './FrameRenderer';
import { findPathLinks, rowCells, type PathLink } from './pathLinks';
import { ServerUnicodeAddon } from './ServerUnicodeAddon';

export const MONOSPACE = 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace';

/** Display-only xterm: no stdin, no local scrollback, no child byte path. */
export class TerminalView {
  readonly terminal: Terminal;
  readonly renderer: FrameRenderer;
  private readonly unicode = new ServerUnicodeAddon();

  constructor(fontSize: number) {
    this.terminal = new Terminal({
      allowProposedApi: true,
      disableStdin: true,
      scrollback: 0,
      cursorBlink: false,
      fontFamily: MONOSPACE,
      fontSize,
      screenReaderMode: false,
      theme: { background: '#0B0E14', foreground: '#D5DCE5', cursor: '#64D8E8' },
    });
    this.terminal.loadAddon(this.unicode);
    this.renderer = new FrameRenderer(this.terminal, this.unicode);
  }

  open(element: HTMLElement): void {
    this.terminal.open(element);
    // xterm keeps a helper textarea; keep it from raising a second keyboard.
    const helper = this.terminal.textarea;
    if (helper !== undefined) {
      helper.readOnly = true;
      helper.tabIndex = -1;
      helper.setAttribute('inputmode', 'none');
      helper.setAttribute('aria-hidden', 'true');
    }
  }

  setFontSize(fontSize: number): void {
    this.terminal.options.fontSize = fontSize;
  }

  /** Grid that fits the available CSS box, measured from xterm's own cells. */
  fit(width: number, height: number): { columns: number; rows: number } | undefined {
    const screen = this.terminal.element?.querySelector<HTMLElement>('.xterm-screen');
    if (screen === null || screen === undefined || screen.clientWidth === 0 || screen.clientHeight === 0) {
      return undefined;
    }
    const cellWidth = screen.clientWidth / this.terminal.cols;
    const cellHeight = screen.clientHeight / this.terminal.rows;
    return fitGrid(width, height, cellWidth, cellHeight);
  }

  /** Pixel top and bottom of the cursor row inside the terminal element. */
  cursorBand(): { top: number; bottom: number } | undefined {
    const screen = this.terminal.element?.querySelector<HTMLElement>('.xterm-screen');
    if (screen === null || screen === undefined || screen.clientHeight === 0) {
      return undefined;
    }
    const cellHeight = screen.clientHeight / this.terminal.rows;
    const row = this.terminal.buffer.active.cursorY;
    return { top: row * cellHeight, bottom: (row + 1) * cellHeight };
  }

  /**
   * Makes path-like text a link; a tap on one calls onOpen. xterm activates a
   * link only when the press and release land on it with no move between, so
   * a swipe that starts on a link scrolls as before. A selection wins too.
   */
  linkPaths(onOpen: (link: PathLink) => void): IDisposable {
    const { terminal } = this;
    return terminal.registerLinkProvider({
      provideLinks(y, callback) {
        const row = terminal.buffer.active.getLine(y - 1);
        if (row === undefined) {
          callback(undefined);
          return;
        }
        const cells: IBufferCell[] = [];
        for (let x = 0; x < row.length; x++) {
          const cell = row.getCell(x);
          if (cell !== undefined) cells.push(cell);
        }
        const { text, columns } = rowCells(cells);
        const links = findPathLinks(text).map((link) => ({
          // 1-based cells, end inclusive.
          range: { start: { x: (columns[link.start] ?? 0) + 1, y }, end: { x: columns[link.end] ?? terminal.cols, y } },
          text: text.slice(link.start, link.end),
          decorations: { pointerCursor: true, underline: true },
          activate: () => {
            if (!terminal.hasSelection()) onOpen(link);
          },
        }));
        callback(links.length > 0 ? links : undefined);
      },
    });
  }

  dispose(): void {
    this.terminal.dispose();
  }
}

export function fitGrid(
  width: number,
  height: number,
  cellWidth: number,
  cellHeight: number,
): { columns: number; rows: number } {
  if (!(cellWidth > 0) || !(cellHeight > 0)) {
    return clampGrid(0, 0);
  }
  return clampGrid(width / cellWidth, height / cellHeight);
}

/** The scroll offset that keeps the cursor row visible, or the same offset when it already is. */
export function followCursor(
  scrollTop: number,
  visibleHeight: number,
  band: { top: number; bottom: number },
): number {
  if (band.bottom > scrollTop + visibleHeight) {
    return Math.ceil(band.bottom - visibleHeight);
  }
  if (band.top < scrollTop) {
    return Math.floor(band.top);
  }
  return scrollTop;
}

/**
 * Finger travel, in CSS pixels, that sends one wheel step to a full-screen app.
 * ponytail: one fixed step; tune per font size if real phones feel too fast or slow.
 */
export const WHEEL_STEP_PX = 36;

/** At most this many wheel steps for one touch move, so a flick cannot flood input. */
export const MAX_WHEEL_STEPS = 4;

/**
 * Turns vertical finger travel into wheel steps. A downward swipe (positive
 * travel) shows earlier output, so it gives positive steps: wheel-up. The
 * remainder carries over to the next move so slow swipes still scroll.
 */
export function wheelSteps(carry: number, travel: number, step = WHEEL_STEP_PX): { steps: number; carry: number } {
  const total = carry + travel;
  const whole = Math.trunc(total / step);
  const steps = Math.max(-MAX_WHEEL_STEPS, Math.min(MAX_WHEEL_STEPS, whole));
  return { steps, carry: whole === steps ? total - whole * step : 0 };
}
