import { Terminal } from '@xterm/xterm';
import { clampGrid } from './Connection';
import { FrameRenderer } from './FrameRenderer';
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
