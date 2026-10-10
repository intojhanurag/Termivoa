import { describe, expect, it } from 'vitest';
import {
  axisLock, HOLD_MS, isTwoFingerTap, MAX_GESTURE_KEYS, pinchFont, sessionSwipe, tapArrows, TouchGestures, TRACKPAD_PILL,
  type GestureHost, type GestureKey, type StyledCell, type TouchPoint,
} from './gestures';

function cell(chars: string, { width = 1, dim = false, fg = 0 } = {}): StyledCell {
  return {
    getChars: () => chars, getWidth: () => width,
    getFgColorMode: () => 0, getFgColor: () => fg, getBgColorMode: () => 0, getBgColor: () => 0,
    isBold: () => 0, isDim: () => (dim ? 1 : 0), isItalic: () => 0, isUnderline: () => 0, isInverse: () => 0, isInvisible: () => 0,
  };
}
const row = (text: string) => [...text].map((char) => cell(char === ' ' ? '' : char));
const screen = (column: number, { row: at = 3, visible = true, buffer = 'normal' as 'normal' | 'alternate' } = {}) => (
  { buffer, cursor: { row: at, column, visible } }
);

describe('tapArrows', () => {
  // "$ abc" with the cursor after "c" (column 5).
  const typed = row('$ abc          ');

  it('moves left along the cursor row to the tapped cell', () => {
    expect(tapArrows(screen(5), { row: 3, column: 2 }, typed)).toBe(-3);
    expect(tapArrows(screen(5), { row: 3, column: 5 }, typed)).toBe(0);
  });

  it('moves right over typed text, up to just past the last character', () => {
    expect(tapArrows(screen(2), { row: 3, column: 4 }, typed)).toBe(2);
    expect(tapArrows(screen(2), { row: 3, column: 5 }, typed)).toBe(3);
  });

  it('never moves right into the blank tail', () => {
    expect(tapArrows(screen(5), { row: 3, column: 9 }, typed)).toBe(0);
    expect(tapArrows(screen(2), { row: 3, column: 7 }, typed)).toBe(0);
  });

  it('never moves right across differently styled text (autosuggestion, right prompt)', () => {
    const suggested = [...row('$ ab'), cell('c', { dim: true }), cell('d', { dim: true }), ...row('    ')];
    expect(tapArrows(screen(4), { row: 3, column: 6 }, suggested)).toBe(0);
    const colored = [...row('$ ab'), cell('c', { fg: 2 }), ...row('   ')];
    expect(tapArrows(screen(3), { row: 3, column: 5 }, colored)).toBe(0);
    // Left over the same cells is fine: the text is there.
    expect(tapArrows(screen(5), { row: 3, column: 2 }, colored)).toBe(-3);
  });

  it('refuses wide characters, continuation cells and joined code points', () => {
    const wide = [...row('$ a'), cell('界', { width: 2 }), cell('', { width: 0 }), ...row('b   ')];
    expect(tapArrows(screen(6), { row: 3, column: 2 }, wide)).toBe(0);
    const zwj = [...row('$ a'), cell('👩‍💻'), ...row('b   ')];
    expect(tapArrows(screen(5), { row: 3, column: 2 }, zwj)).toBe(0);
  });

  it('refuses another row, a hidden cursor, the alternate screen, a cursor at the row start, and far taps', () => {
    expect(tapArrows(screen(5), { row: 2, column: 2 }, typed)).toBe(0);
    expect(tapArrows(screen(5, { visible: false }), { row: 3, column: 2 }, typed)).toBe(0);
    expect(tapArrows(screen(5, { buffer: 'alternate' }), { row: 3, column: 2 }, typed)).toBe(0);
    expect(tapArrows(screen(0), { row: 3, column: 2 }, typed)).toBe(0);
    const long = row(`$ ${'x'.repeat(100)} `);
    expect(tapArrows(screen(102), { row: 3, column: 21 }, long)).toBe(0);
    expect(tapArrows(screen(102), { row: 3, column: 22 }, long)).toBe(-80);
  });

  it('needs a shell prompt before the cursor: no arrows into a password or y/N prompt', () => {
    const sudo = row('[sudo] password for x:       ');
    expect(tapArrows(screen(23), { row: 3, column: 10 }, sudo)).toBe(0);
    const confirm = row('Continue? [y/N] yes   ');
    expect(tapArrows(screen(19), { row: 3, column: 16 }, confirm)).toBe(0);
    const readLine = row('name: abc   ');
    expect(tapArrows(screen(9), { row: 3, column: 6 }, readLine)).toBe(0);
  });

  it('moves only after the prompt: within the typed text, never into the prompt', () => {
    // "user@host:~/p$ git status" with the cursor between "sta" and "tus".
    const line = row('user@host:~/p$ git status    ');
    expect(tapArrows(screen(22), { row: 3, column: 15 }, line)).toBe(-7);
    expect(tapArrows(screen(22), { row: 3, column: 24 }, line)).toBe(2);
    expect(tapArrows(screen(22), { row: 3, column: 13 }, line)).toBe(0);
    expect(tapArrows(screen(22), { row: 3, column: 3 }, line)).toBe(0);
    for (const mark of ['%', '#', '>', '❯', '➜']) expect(tapArrows(screen(5), { row: 3, column: 2 }, row(`${mark} abc   `))).toBe(-3);
  });

  it('stops at a right prompt after two blank cells', () => {
    const rprompt = row('$ abc        main ');
    expect(tapArrows(screen(3), { row: 3, column: 5 }, rprompt)).toBe(2);
    expect(tapArrows(screen(3), { row: 3, column: 14 }, rprompt)).toBe(0);
  });
});

describe('pure rules', () => {
  it('locks the axis by the larger travel after 12 px', () => {
    expect(axisLock(11, 0)).toBeUndefined();
    expect(axisLock(12, 5)).toBe('x');
    expect(axisLock(-3, -20)).toBe('y');
  });

  it('maps a pinch to text sizes inside 10 to 24', () => {
    expect(pinchFont(14, 1)).toBe(14);
    expect(pinchFont(14, 2)).toBe(20);
    expect(pinchFont(14, 0.5)).toBe(10);
    expect(pinchFont(14, 8)).toBe(24);
    expect(pinchFont(14, 1.13)).toBe(15);
    expect(pinchFont(14, 0)).toBe(14);
  });

  it('tells a two-finger tap from a slow, moving or pinching touch', () => {
    const tap = { gap: 20, duration: 150, moved: 3, scaleChange: 0.01 };
    expect(isTwoFingerTap(tap)).toBe(true);
    expect(isTwoFingerTap({ ...tap, gap: 100, duration: 300 })).toBe(true);
    expect(isTwoFingerTap({ ...tap, gap: 101 })).toBe(false);
    expect(isTwoFingerTap({ ...tap, duration: 301 })).toBe(false);
    expect(isTwoFingerTap({ ...tap, moved: 10 })).toBe(false);
    expect(isTwoFingerTap({ ...tap, scaleChange: 0.05 })).toBe(false);
  });

  it('switches session only on a fast, flat swipe away from the edges', () => {
    expect(sessionSwipe(-80, 10, 200, 200, 390)).toBe(1);
    expect(sessionSwipe(80, 10, 200, 200, 390)).toBe(-1);
    expect(sessionSwipe(-59, 0, 200, 200, 390)).toBe(0);
    expect(sessionSwipe(-80, 41, 200, 200, 390)).toBe(0);
    expect(sessionSwipe(-80, 0, 500, 200, 390)).toBe(0);
    expect(sessionSwipe(80, 0, 200, 20, 390)).toBe(0);
    expect(sessionSwipe(-80, 0, 200, 370, 390)).toBe(0);
  });
});

/** A host with a manual clock and timers; records what the machine did. */
function harness({ enabled = true, canInput = true, refuseAfter = Infinity } = {}) {
  let now = 0;
  let timers: Array<{ at: number; run: () => void }> = [];
  const sent: GestureKey[] = [];
  const pills: Array<string | undefined> = [];
  const drags: Array<[string, number]> = [];
  const fonts: number[] = [];
  let took = 0;
  const state = { enabled, canInput };
  const host: GestureHost = {
    enabled: () => state.enabled,
    canInput: () => state.canInput,
    cell: () => ({ width: 8, height: 16 }),
    send: (key) => {
      if (sent.length >= refuseAfter) return false;
      sent.push(key);
      return true;
    },
    drag: (phase, point) => { drags.push([phase, point.y]); },
    fontSize: () => 14,
    setFontSize: (size) => { fonts.push(size); },
    pill: (text) => { pills.push(text); },
    tookTap: () => { took++; },
    now: () => now,
    later: (run, ms) => {
      const timer = { at: now + ms, run };
      timers.push(timer);
      return () => { timers = timers.filter((item) => item !== timer); };
    },
  };
  const machine = new TouchGestures(() => host);
  const tick = (ms: number) => {
    now += ms;
    const due = timers.filter((timer) => timer.at <= now);
    timers = timers.filter((timer) => timer.at > now);
    for (const timer of due) timer.run();
  };
  return { machine, sent, pills, drags, fonts, state, tick, took: () => took };
}
const at = (x: number, y: number, id = 0): TouchPoint => ({ id, x, y });

describe('hold and slide', () => {
  it('arms after a still hold and sends one arrow per cell along the locked axis', () => {
    const h = harness();
    h.machine.start([at(100, 100)]);
    h.tick(HOLD_MS);
    expect(h.pills).toEqual([TRACKPAD_PILL]);
    expect(h.machine.move([at(105, 102)])).toBe(true);
    expect(h.sent).toEqual([]);
    // Locks on x at 12 px: 12 px is one 10 px step (cells are 8 px, a step is at least 10).
    h.machine.move([at(112, 104)]);
    expect(h.sent).toEqual(['arrow-right']);
    // Vertical travel after the lock is ignored.
    h.machine.move([at(112, 200)]);
    expect(h.sent).toHaveLength(1);
    h.machine.move([at(90, 200)]);
    expect(h.sent).toEqual(['arrow-right', 'arrow-left', 'arrow-left']);
    expect(h.machine.end([])).toBe(true);
    expect(h.took()).toBe(1);
    expect(h.pills.at(-1)).toBeUndefined();
  });

  it('sends at most 4 per move, 30 per second and 120 per gesture', () => {
    const h = harness();
    h.machine.start([at(0, 0)]);
    h.tick(HOLD_MS);
    let x = 0;
    h.machine.move([at((x += 500), 0)]);
    expect(h.sent).toHaveLength(4);
    for (let move = 0; move < 20; move++) h.machine.move([at((x += 40), 0)]);
    expect(h.sent).toHaveLength(30);
    for (let second = 0; second < 10; second++) {
      h.tick(1000);
      for (let move = 0; move < 10; move++) h.machine.move([at((x += 40), 0)]);
    }
    expect(h.sent).toHaveLength(MAX_GESTURE_KEYS);
    h.machine.move([at((x += 40), 0)]);
    expect(h.sent).toHaveLength(MAX_GESTURE_KEYS);
  });

  it('a refused key ends the gesture: no retry, nothing queued', () => {
    const h = harness({ refuseAfter: 1 });
    h.machine.start([at(0, 0)]);
    h.tick(HOLD_MS);
    h.machine.move([at(40, 0)]);
    expect(h.sent).toEqual(['arrow-right']);
    h.machine.move([at(80, 0)]);
    expect(h.sent).toEqual(['arrow-right']);
    expect(h.pills.at(-1)).toBeUndefined();
  });

  it('read-only never arms: the hold stays a plain touch and its click goes through', () => {
    const h = harness({ canInput: false });
    h.machine.start([at(0, 0)]);
    h.tick(HOLD_MS);
    expect(h.pills).toEqual([]);
    h.machine.move([at(5, 60)]);
    expect(h.sent).toEqual([]);
    expect(h.machine.end([])).toBe(false);
    expect(h.took()).toBe(0);
  });

  it('a move before the hold is a scroll: it never arms and keeps feeding the drag', () => {
    const h = harness();
    h.machine.start([at(0, 0)]);
    expect(h.machine.move([at(0, 20)])).toBe(false);
    h.tick(HOLD_MS);
    expect(h.pills).toEqual([]);
    h.machine.move([at(0, 60)]);
    expect(h.drags).toEqual([['start', 0], ['move', 20], ['move', 60]]);
  });

  it('a second finger or a cancel stops the trackpad until every finger lifts', () => {
    const h = harness();
    h.machine.start([at(0, 0)]);
    h.tick(HOLD_MS);
    h.machine.start([at(0, 0), at(50, 50, 1)]);
    h.machine.move([at(100, 0), at(50, 50, 1)]);
    expect(h.sent).toEqual([]);
    h.machine.end([]);
    h.machine.start([at(0, 0)]);
    h.tick(HOLD_MS);
    h.machine.cancel();
    expect(h.machine.move([at(100, 0)])).toBe(true);
    expect(h.sent).toEqual([]);
  });
});

describe('pinch and two-finger tap', () => {
  it('shows the size while pinching and sets it once, on lift', () => {
    const h = harness();
    h.machine.start([at(100, 100)]);
    h.machine.start([at(100, 100), at(200, 100, 1)]);
    expect(h.machine.move([at(100, 100), at(250, 100, 1)])).toBe(true);
    expect(h.pills.at(-1)).toBe('18 px');
    h.machine.move([at(100, 100), at(300, 100, 1)]);
    expect(h.pills.at(-1)).toBe('20 px');
    expect(h.fonts).toEqual([]);
    expect(h.machine.end([at(100, 100)])).toBe(true);
    expect(h.fonts).toEqual([20]);
    h.machine.end([]);
    expect(h.fonts).toEqual([20]);
    expect(h.pills.at(-1)).toBeUndefined();
  });

  it('a two-finger scroll that keeps the size shows no pill', () => {
    const h = harness();
    h.machine.start([at(100, 100), at(200, 100, 1)]);
    h.machine.move([at(100, 160), at(200, 160, 1)]);
    h.machine.end([]);
    expect(h.pills).toEqual([]);
    expect(h.fonts).toEqual([]);
  });

  it('a pinch works without control', () => {
    const h = harness({ canInput: false });
    h.machine.start([at(100, 100), at(200, 100, 1)]);
    h.machine.move([at(100, 100), at(150, 100, 1)]);
    h.machine.end([]);
    expect(h.fonts).toEqual([pinchFont(14, 0.5)]);
  });

  it('a quick still two-finger tap sends one Esc', () => {
    const h = harness();
    h.machine.start([at(100, 100)]);
    h.tick(20);
    h.machine.start([at(100, 100), at(200, 100, 1)]);
    h.tick(100);
    h.machine.end([at(200, 100, 1)]);
    expect(h.machine.end([])).toBe(true);
    expect(h.sent).toEqual(['escape']);
    expect(h.fonts).toEqual([]);
  });

  it('no Esc when slow, late, read-only or under a sheet', () => {
    const slow = harness();
    slow.machine.start([at(100, 100), at(200, 100, 1)]);
    slow.tick(301);
    slow.machine.end([]);
    const late = harness();
    late.machine.start([at(100, 100)]);
    late.tick(101);
    late.machine.start([at(100, 100), at(200, 100, 1)]);
    late.machine.end([]);
    const readOnly = harness({ canInput: false });
    readOnly.machine.start([at(100, 100), at(200, 100, 1)]);
    readOnly.machine.end([]);
    expect([...slow.sent, ...late.sent, ...readOnly.sent]).toEqual([]);
  });
});

describe('the switch', () => {
  it('off: no trackpad, pinch or Esc; one-finger drags work as before and clicks go through', () => {
    const h = harness({ enabled: false });
    h.machine.start([at(0, 0)]);
    h.tick(HOLD_MS);
    expect(h.machine.move([at(60, 60)])).toBe(false);
    expect(h.machine.end([])).toBe(false);
    h.machine.start([at(100, 100)]);
    h.machine.start([at(100, 100), at(200, 100, 1)]);
    expect(h.machine.move([at(100, 100), at(300, 100, 1)])).toBe(false);
    h.machine.end([]);
    expect(h.sent).toEqual([]);
    expect(h.fonts).toEqual([]);
    expect(h.pills).toEqual([]);
    expect(h.took()).toBe(0);
    expect(h.drags).toEqual([['start', 0], ['move', 60], ['start', 100], ['start', 100], ['move', 100]]);
  });
});
