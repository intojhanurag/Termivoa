import { Terminal, type IBufferCell, type IDisposable } from '@xterm/xterm';
import { clampGrid } from './Connection';
import { FrameRenderer } from './FrameRenderer';
import { joinedRowLinks, rowLinks, overlaps, type Cell, type PathLink } from './pathLinks';
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
   * Makes path-like text a link; a tap on one calls onOpen.
   * xterm activates a link on mouseup when the mousedown was on the same link
   * and the mouseup is still inside it. A touch that scrolls or swipes sends
   * no mouse events, so it never activates one; a mouse drag inside a link
   * would, so a selection wins. On a touch screen there is no hover
   * decoration: iOS would spend the first tap on the hover change.
   */
  linkPaths(onOpen: (link: PathLink) => void): IDisposable {
    const { terminal } = this;
    return terminal.registerLinkProvider({
      provideLinks(y, callback) {
        const cells = rowCellsAt(terminal, y);
        if (cells === undefined) {
          callback(undefined);
          return;
        }
        const hover = globalThis.matchMedia?.('(pointer: coarse)').matches !== true;
        // A wrapped add-computer link can look like a path on one row ("/add-computer#…"); it is not one.
        const urls = joinedRowLinks(...rowsAround(terminal, y)).map((url) => url.range);
        const links = rowLinks(cells, y).filter(({ range }) => !urls.some((url) => overlaps(url, range))).map(({ link, text, range }) => ({
          range,
          text,
          decorations: { pointerCursor: hover, underline: hover },
          activate: () => {
            if (!terminal.hasSelection()) onOpen(link);
          },
        }));
        callback(links.length > 0 ? links : undefined);
      },
    });
  }

  /**
   * Makes an add-computer link a link, also when it wraps over several rows;
   * a tap on one calls onOpen with the whole link. Only links that accept
   * takes are links; terminal output is untrusted, so accept checks each one
   * (parseAddComputerLink). Taps, hover and selection work as in linkPaths.
   */
  linkAddComputer(accept: (url: string) => boolean, onOpen: (url: string) => void): IDisposable {
    const { terminal } = this;
    return terminal.registerLinkProvider({
      provideLinks(y, callback) {
        const hover = globalThis.matchMedia?.('(pointer: coarse)').matches !== true;
        const links = joinedRowLinks(...rowsAround(terminal, y))
          .filter(({ url, range }) => range.start.y <= y && y <= range.end.y && accept(url))
          .map(({ url, range }) => ({
            range,
            text: url,
            decorations: { pointerCursor: hover, underline: hover },
            activate: () => {
              if (!terminal.hasSelection()) onOpen(url);
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

/** The cells of buffer row y (1-based), or undefined past the buffer. */
function rowCellsAt(terminal: Terminal, y: number): IBufferCell[] | undefined {
  const row = terminal.buffer.active.getLine(y - 1);
  if (row === undefined) return undefined;
  const cells: IBufferCell[] = [];
  for (let x = 0; x < row.length; x++) {
    const cell = row.getCell(x);
    if (cell !== undefined) cells.push(cell);
  }
  return cells;
}

/** Rows a wrapped link through row y can span. ponytail: 7 rows each way covers a 140-character link at 20 columns. */
const LINK_ROWS = 7;

/** The rows around buffer row y and the 1-based row of the first, for joinedRowLinks. */
function rowsAround(terminal: Terminal, y: number): [Cell[][], number] {
  const first = Math.max(1, y - LINK_ROWS);
  const rows: Cell[][] = [];
  for (let at = first; at <= y + LINK_ROWS; at++) {
    const cells = rowCellsAt(terminal, at);
    if (cells === undefined) break;
    rows.push(cells);
  }
  return [rows, first];
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
