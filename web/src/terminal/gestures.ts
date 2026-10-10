import { MAX_FONT_SIZE, MIN_FONT_SIZE } from '../screens/hooks';
import type { Cell } from './pathLinks';
import { wheelSteps } from './TerminalView';

/**
 * Touch gestures on the Live terminal (docs/gestures.md): pure rules plus one
 * state machine. Live feeds it touch points and gives it a host; nothing here
 * touches the DOM, and no terminal bytes pass through it.
 */

/** A finger held this long within HOLD_SLOP_PX arms the trackpad. */
export const HOLD_MS = 400;
export const HOLD_SLOP_PX = 8;
/** Trackpad travel before the axis locks. */
export const LOCK_PX = 12;
/** Bounds for one trackpad gesture: keys in all, and keys per second (excess is dropped, never buffered). */
export const MAX_GESTURE_KEYS = 120;
export const MAX_KEYS_PER_SECOND = 30;
/** A tap moves the cursor at most this many cells. */
export const MAX_TAP_ARROWS = 80;
/** Two-finger tap: the second finger lands within GAP, both lift within MS of the first touch. */
export const TWO_TAP_GAP_MS = 100;
export const TWO_TAP_MS = 300;
export const TWO_TAP_MOVE_PX = 10;
export const TWO_TAP_SCALE = 0.05;
/** Session swipe on the status line. */
export const SWIPE_PX = 60;
export const SWIPE_MS = 500;
export const SWIPE_EDGE_PX = 24;
/** The pill while the trackpad is armed. */
export const TRACKPAD_PILL = '← → ↑ ↓';

export type Axis = 'x' | 'y';
export type GestureKey = 'arrow-left' | 'arrow-right' | 'arrow-up' | 'arrow-down' | 'escape';

/** The axis the trackpad keeps until lift: the larger travel, once either reaches LOCK_PX. */
export function axisLock(dx: number, dy: number): Axis | undefined {
  const ax = Math.abs(dx);
  const ay = Math.abs(dy);
  if (Math.max(ax, ay) < LOCK_PX) return undefined;
  return ax >= ay ? 'x' : 'y';
}

/** Finger travel for one arrow: a cell across, one and a half cells down, never tiny. */
export function trackpadStep(axis: Axis, cell: { readonly width: number; readonly height: number }): number {
  return axis === 'x' ? Math.max(cell.width, 10) : Math.max(1.5 * cell.height, 24);
}

/** Text size for a pinch: six sizes per doubling of the finger distance, inside the A± bounds. */
export function pinchFont(start: number, scale: number): number {
  if (!(scale > 0)) return start;
  return Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, start + Math.round(Math.log2(scale) * 6)));
}

/** A quick, still two-finger touch: Esc. */
export function isTwoFingerTap(touch: {
  /** From the first finger down to the second. */
  readonly gap: number;
  /** From the first finger down to the last lift. */
  readonly duration: number;
  /** The most either finger moved. */
  readonly moved: number;
  /** The most the finger distance changed, as a ratio from 1. */
  readonly scaleChange: number;
}): boolean {
  return touch.gap <= TWO_TAP_GAP_MS && touch.duration <= TWO_TAP_MS
    && touch.moved < TWO_TAP_MOVE_PX && touch.scaleChange < TWO_TAP_SCALE;
}

/** -1 for the session before, 1 for the one after, 0 for no swipe. A swipe to the left shows the next one. */
export function sessionSwipe(dx: number, dy: number, ms: number, startX: number, width: number): -1 | 0 | 1 {
  if (Math.abs(dx) < SWIPE_PX || Math.abs(dx) <= 2 * Math.abs(dy) || ms >= SWIPE_MS) return 0;
  if (startX <= SWIPE_EDGE_PX || startX >= width - SWIPE_EDGE_PX) return 0;
  return dx < 0 ? 1 : -1;
}

/** One xterm cell with the style parts a tap compares. */
export interface StyledCell extends Cell {
  getFgColorMode(): number;
  getFgColor(): number;
  getBgColorMode(): number;
  getBgColor(): number;
  isBold(): number;
  isDim(): number;
  isItalic(): number;
  isUnderline(): number;
  isInverse(): number;
  isInvisible(): number;
}

function styleOf(cell: StyledCell): string {
  return [
    cell.getFgColorMode(), cell.getFgColor(), cell.getBgColorMode(), cell.getBgColor(),
    cell.isBold(), cell.isDim(), cell.isItalic(), cell.isUnderline(), cell.isInverse(), cell.isInvisible(),
  ].join(',');
}

/** The end of a shell prompt: one of these, then a blank cell. */
const PROMPT_ENDS = new Set(['$', '%', '#', '>', '❯', '➜']);

const blank = (cell: StyledCell | undefined) => cell === undefined || cell.getChars().trim() === '';

/**
 * The first column after the last shell prompt end ("$ ", "% ", "# ", "> ",
 * "❯ ", "➜ ") left of `column`, or undefined. A line with none (a password
 * prompt, "Continue? [y/N] ", `read`) may have no line editor, where arrows
 * would arrive as input.
 */
export function promptEnd(cells: ReadonlyArray<StyledCell>, column: number): number | undefined {
  for (let x = Math.min(column, cells.length) - 2; x >= 0; x--) {
    if (PROMPT_ENDS.has(cells[x]?.getChars() ?? '') && blank(cells[x + 1])) return x + 2;
  }
  return undefined;
}

/** One column, one code point: an arrow press moves over exactly this cell. */
function plain(cell: StyledCell | undefined): cell is StyledCell {
  return cell !== undefined && cell.getWidth() === 1 && [...cell.getChars()].length <= 1;
}

/**
 * Arrows for a tap at `tap` (0-based cell): negative is ←, positive is →, 0
 * sends nothing. Only on the cursor's own row of the normal screen with a
 * visible cursor, after a shell prompt (promptEnd: both the cursor and the
 * tap), only over single-column, single-code-point cells. A move to the right
 * only crosses text styled like the cell left of the cursor and stops at the
 * first run of two blank cells, so grey autosuggestions and a right prompt
 * stay put.
 */
export function tapArrows(
  screen: { readonly buffer: 'normal' | 'alternate'; readonly cursor: { readonly row: number; readonly column: number; readonly visible: boolean } },
  tap: { readonly row: number; readonly column: number },
  cells: ReadonlyArray<StyledCell>,
): number {
  const { cursor } = screen;
  if (screen.buffer !== 'normal' || !cursor.visible || tap.row !== cursor.row) return 0;
  const delta = tap.column - cursor.column;
  if (delta === 0 || Math.abs(delta) > MAX_TAP_ARROWS) return 0;
  const start = promptEnd(cells, cursor.column);
  if (start === undefined || tap.column < start || cursor.column < start) return 0;
  const from = Math.min(tap.column, cursor.column);
  const to = Math.max(tap.column, cursor.column);
  for (let x = from; x < to; x++) {
    if (!plain(cells[x])) return 0;
  }
  if (delta > 0) {
    const left = cells[cursor.column - 1];
    if (left === undefined) return 0;
    // The typed text ends before the first two blank cells after the prompt (a right prompt may follow).
    let lastText = start - 1;
    for (let x = start; x < cells.length && !(blank(cells[x]) && blank(cells[x + 1])); x++) {
      if (!blank(cells[x])) lastText = x;
    }
    if (to - 1 > lastText) return 0;
    const style = styleOf(left);
    for (let x = from; x < to; x++) {
      if (styleOf(cells[x] as StyledCell) !== style) return 0;
    }
  }
  return delta;
}

export interface TouchPoint {
  readonly id: number;
  readonly x: number;
  readonly y: number;
}

/** What the machine needs from Live. Read each time, so it always sees the latest state. */
export interface GestureHost {
  /** The Settings switch "Terminal gestures". Off: exactly the old touch behaviour. */
  enabled(): boolean;
  /** This phone may send input now: control of a live, running session, and no sheet, card or full panel. */
  canInput(): boolean;
  /** One terminal cell in CSS pixels. */
  cell(): { readonly width: number; readonly height: number } | undefined;
  /** One key, without modifiers. False when refused (no control, link down); nothing is queued. */
  send(key: GestureKey): boolean;
  /** One-finger travel that is not a gesture: app scroll (at the finger's cell) and the history pull, as before. */
  drag(phase: 'start' | 'move', point: TouchPoint): void;
  fontSize(): number;
  /** Once, on lift, so the PTY resizes once. */
  setFontSize(size: number): void;
  pill(text: string | undefined): void;
  /** The gesture took this touch: a click that follows does nothing. */
  tookTap(): void;
  now(): number;
  later(run: () => void, ms: number): () => void;
}

type State =
  | { readonly kind: 'idle' }
  /** Gestures off: today's behaviour. */
  | { readonly kind: 'plain' }
  | { readonly kind: 'pending'; readonly at: number; readonly start: TouchPoint; last: TouchPoint; readonly stop: () => void }
  | { readonly kind: 'scroll'; readonly at: number }
  | { readonly kind: 'trackpad'; readonly id: number; readonly origin: TouchPoint; last: TouchPoint; axis: Axis | undefined; carry: number; sent: number; readonly times: number[] }
  | {
    readonly kind: 'two'; readonly at: number; readonly gap: number; readonly starts: ReadonlyMap<number, TouchPoint>; readonly distance: number;
    moved: number; scaleChange: number; pinching: boolean; readonly font: number; size: number; shown: boolean;
  }
  /** A gesture ended or was cancelled: ignore touches until every finger lifts. */
  | { readonly kind: 'done' };

const distance = (a: TouchPoint, b: TouchPoint) => Math.hypot(a.x - b.x, a.y - b.y);

/** One touch state machine for the terminal area: idle → pending → scroll | trackpad | two (pinch or Esc tap). */
export class TouchGestures {
  private state: State = { kind: 'idle' };

  constructor(private readonly host: () => GestureHost) {}

  /** True while a gesture holds the touch; Live then prevents the default of touchmove and touchend. */
  get holding(): boolean {
    const { kind } = this.state;
    return kind === 'trackpad' || kind === 'two' || kind === 'done';
  }

  start(touches: ReadonlyArray<TouchPoint>): void {
    const host = this.host();
    const [first, second] = touches;
    if (first === undefined) return;
    // One finger down means every earlier finger lifted, even if an end was missed.
    if (touches.length === 1) this.reset();
    const { state } = this;
    if (state.kind === 'plain' || (state.kind === 'idle' && !host.enabled())) {
      this.state = { kind: 'plain' };
      host.drag('start', first);
      return;
    }
    if (state.kind === 'idle' && touches.length === 1) {
      const stop = host.later(() => this.arm(), HOLD_MS);
      this.state = { kind: 'pending', at: host.now(), start: first, last: first, stop };
      host.drag('start', first);
      return;
    }
    if (touches.length === 2 && second !== undefined && (state.kind === 'idle' || state.kind === 'pending' || state.kind === 'scroll')) {
      if (state.kind === 'pending') state.stop();
      const now = host.now();
      const at = state.kind === 'idle' ? now : state.at;
      this.state = {
        kind: 'two', at, gap: state.kind === 'scroll' ? Infinity : now - at, starts: new Map([[first.id, first], [second.id, second]]),
        distance: distance(first, second), moved: 0, scaleChange: 0, pinching: false, font: host.fontSize(), size: host.fontSize(), shown: false,
      };
      return;
    }
    // A second finger on the trackpad, or a third finger: cancel.
    this.cancel();
  }

  /** Returns true when the move belongs to a gesture (prevent the default scroll and zoom). */
  move(touches: ReadonlyArray<TouchPoint>): boolean {
    const host = this.host();
    const { state } = this;
    const [first] = touches;
    if (first === undefined) return this.holding;
    switch (state.kind) {
      case 'plain':
      case 'scroll':
        host.drag('move', first);
        return false;
      case 'pending':
        state.last = first;
        host.drag('move', first);
        if (distance(first, state.start) > HOLD_SLOP_PX) {
          state.stop();
          this.state = { kind: 'scroll', at: state.at };
        }
        return false;
      case 'trackpad':
        this.slide(state, touches.find((touch) => touch.id === state.id) ?? first);
        return true;
      case 'two':
        this.pinch(state, touches);
        return true;
      default:
        return this.holding;
    }
  }

  /** Returns true when the gesture took the touch (prevent the default, so no click follows). */
  end(touches: ReadonlyArray<TouchPoint>): boolean {
    const host = this.host();
    const { state } = this;
    const took = this.holding;
    if (state.kind === 'trackpad') {
      this.finish();
    } else if (state.kind === 'two' && state.pinching) {
      if (state.size !== state.font) host.setFontSize(state.size);
      this.finish();
    } else if (state.kind === 'two' && touches.length === 0) {
      const tap = isTwoFingerTap({ gap: state.gap, duration: host.now() - state.at, moved: state.moved, scaleChange: state.scaleChange });
      if (tap && host.canInput()) host.send('escape');
      this.finish();
    } else if (state.kind === 'pending' || state.kind === 'scroll' || state.kind === 'plain') {
      if (touches.length === 0) this.reset();
    }
    if (touches.length === 0 && this.state.kind === 'done') this.state = { kind: 'idle' };
    if (took) host.tookTap();
    return took;
  }

  /** touchcancel, a hidden page, or the screen going away: stop now and send nothing more. */
  cancel(): void {
    if (this.holding) this.finish();
    else this.reset();
  }

  private reset(): void {
    if (this.state.kind === 'pending') this.state.stop();
    if (this.state.kind === 'trackpad' || (this.state.kind === 'two' && this.state.shown)) this.host().pill(undefined);
    this.state = { kind: 'idle' };
  }

  private finish(): void {
    if (this.state.kind === 'trackpad' || (this.state.kind === 'two' && this.state.shown)) this.host().pill(undefined);
    this.state = { kind: 'done' };
  }

  private arm(): void {
    const { state } = this;
    // Read-only, a sheet on top, or no live link: the hold stays a plain touch.
    if (state.kind !== 'pending' || !this.host().canInput()) return;
    this.state = { kind: 'trackpad', id: state.last.id, origin: state.last, last: state.last, axis: undefined, carry: 0, sent: 0, times: [] };
    this.host().pill(TRACKPAD_PILL);
  }

  private slide(state: Extract<State, { kind: 'trackpad' }>, point: TouchPoint): void {
    const host = this.host();
    let travel: number;
    if (state.axis === undefined) {
      state.axis = axisLock(point.x - state.origin.x, point.y - state.origin.y);
      if (state.axis === undefined) return;
      travel = state.axis === 'x' ? point.x - state.origin.x : point.y - state.origin.y;
    } else {
      travel = state.axis === 'x' ? point.x - state.last.x : point.y - state.last.y;
    }
    state.last = point;
    const cell = host.cell();
    if (cell === undefined) return;
    // At most MAX_WHEEL_STEPS per move; wheelSteps drops the rest.
    const { steps, carry } = wheelSteps(state.carry, travel, trackpadStep(state.axis, cell));
    state.carry = carry;
    const key: GestureKey = state.axis === 'x' ? (steps > 0 ? 'arrow-right' : 'arrow-left') : (steps > 0 ? 'arrow-down' : 'arrow-up');
    for (let step = 0; step < Math.abs(steps); step++) {
      if (state.sent >= MAX_GESTURE_KEYS) {
        this.finish();
        return;
      }
      const now = host.now();
      while (state.times.length > 0 && now - (state.times[0] ?? 0) >= 1000) state.times.shift();
      if (state.times.length >= MAX_KEYS_PER_SECOND) continue;
      // The first refusal ends the gesture: no retry, no queue.
      if (!host.canInput() || !host.send(key)) {
        this.finish();
        return;
      }
      state.times.push(now);
      state.sent++;
    }
  }

  private pinch(state: Extract<State, { kind: 'two' }>, touches: ReadonlyArray<TouchPoint>): void {
    const tracked = touches.filter((touch) => state.starts.has(touch.id));
    for (const touch of tracked) {
      const start = state.starts.get(touch.id);
      if (start !== undefined) state.moved = Math.max(state.moved, distance(start, touch));
    }
    const [a, b] = tracked;
    if (a === undefined || b === undefined || state.distance === 0) return;
    const scale = distance(a, b) / state.distance;
    state.scaleChange = Math.max(state.scaleChange, Math.abs(scale - 1));
    if (!state.pinching && (state.moved >= TWO_TAP_MOVE_PX || state.scaleChange >= TWO_TAP_SCALE)) state.pinching = true;
    if (!state.pinching) return;
    state.size = pinchFont(state.font, scale);
    // No pill until the size changes, so a two-finger scroll shows nothing.
    if (state.size !== state.font) state.shown = true;
    if (state.shown) this.host().pill(`${state.size} px`);
  }
}
