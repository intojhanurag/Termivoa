import { Terminal, type IBufferCell, type IDisposable } from '@xterm/xterm';
import { clampGrid } from './Connection';
import { FrameRenderer } from './FrameRenderer';
import { bufferRow, joinedRowLinks, rowLinks, overlaps, type Cell, type PathLink } from './pathLinks';
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
      theme: { background: '#07090C', foreground: '#ECE8E1', cursor: '#ECE8E1', cursorAccent: '#07090C' },
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
    const cell = this.cellSize();
    return cell === undefined ? undefined : fitGrid(width, height, cell.width, cell.height);
  }

  private screen(): HTMLElement | undefined {
    const screen = this.terminal.element?.querySelector<HTMLElement>('.xterm-screen');
    return screen === null || screen === undefined || screen.clientWidth === 0 || screen.clientHeight === 0 ? undefined : screen;
  }

  /** One cell in CSS pixels, measured from xterm's own screen. */
  cellSize(): { width: number; height: number } | undefined {
    const screen = this.screen();
    return screen === undefined ? undefined : { width: screen.clientWidth / this.terminal.cols, height: screen.clientHeight / this.terminal.rows };
  }

  /**
   * The cell under a viewport point as a 0-based frame row and column (the
   * frame cursor's terms), or undefined outside the grid. With `clamp`, a
   * point outside the grid gives the nearest cell.
   */
  cellAt(clientX: number, clientY: number, clamp = false): { row: number; column: number } | undefined {
    const screen = this.screen();
    const cell = this.cellSize();
    if (screen === undefined || cell === undefined) return undefined;
    const box = screen.getBoundingClientRect();
    const fit = (value: number, size: number) => (clamp ? Math.min(Math.max(value, 0), size - 1) : value);
    const column = fit(Math.floor((clientX - box.left) / cell.width), this.terminal.cols);
    const row = fit(Math.floor((clientY - box.top) / cell.height), this.terminal.rows);
    if (column < 0 || column >= this.terminal.cols || row < 0 || row >= this.terminal.rows) return undefined;
    return { row, column };
  }

  /** The cells of frame row `row` (as cellAt counts rows). */
  rowCells(row: number): IBufferCell[] {
    return bufferRow(this.terminal, this.terminal.buffer.active.baseY + row + 1) ?? [];
  }

  /** Pixel top and bottom of the cursor row inside the terminal element. */
  cursorBand(): { top: number; bottom: number } | undefined {
    const cell = this.cellSize();
    if (cell === undefined) {
      return undefined;
    }
    const row = this.terminal.buffer.active.cursorY;
    return { top: row * cell.height, bottom: (row + 1) * cell.height };
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
        const cells = bufferRow(terminal, y);
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

/** Rows a wrapped link through row y can span. ponytail: 7 rows each way covers a 140-character link at 20 columns. */
const LINK_ROWS = 7;

/** The rows around buffer row y and the 1-based row of the first, for joinedRowLinks. */
function rowsAround(terminal: Terminal, y: number): [Cell[][], number] {
  const first = Math.max(1, y - LINK_ROWS);
  const rows: Cell[][] = [];
  for (let at = first; at <= y + LINK_ROWS; at++) {
    const cells = bufferRow(terminal, at);
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

type WheelCell = { readonly row: number; readonly column: number };
type SendWheel = (button: 'wheel-up' | 'wheel-down', cell: WheelCell | undefined) => void;

/**
 * App scroll in a full-screen app: wheel steps from swipe or wheel travel,
 * each at one cell (#288). A swipe keeps the cell where it began, so tmux and
 * vim scroll that one pane even when the finger leaves it or the grid.
 */
export class AppScroll {
  private carry = 0;
  private cell: WheelCell | undefined;

  /** A swipe begins at `cell` (the grid cell under the finger). */
  start(cell: WheelCell | undefined): void {
    this.carry = 0;
    this.cell = cell;
  }

  /** Swipe travel: steps at the start cell. */
  move(travel: number, send: SendWheel): void {
    const { steps, carry } = wheelSteps(this.carry, travel);
    this.carry = carry;
    for (let step = 0; step < Math.abs(steps); step++) send(steps > 0 ? 'wheel-up' : 'wheel-down', this.cell);
  }

  /** A desktop wheel event at the pointer's cell. */
  wheel(travel: number, cell: WheelCell | undefined, send: SendWheel): void {
    this.cell = cell;
    this.move(travel, send);
  }
}
