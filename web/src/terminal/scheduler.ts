/**
 * The phone-wide terminal-work budget (docs/multi-computer.md, "Performance
 * budget" and "Home offline, and one computer offline"). The phone, not the
 * computers, is the bottleneck: every message is parsed and validated on
 * arrival and browser WebSockets have no receive backpressure, so the only
 * lever is how many streams are open and how fast they run.
 *
 * - The active terminal (Live) comes first: it is never queued or throttled
 *   and keeps today's limits. While one is active, and while the app is in the
 *   background, every Wall tile is paused.
 * - Wall tiles ask for a live slot with requestLiveTile. At most `liveTiles`
 *   thumbnail tiles are live. When live tiles together decode more than `tileBytesPerSecond`, the noisiest one
 *   pauses for `bytePauseMs`. A paused tile keeps its last frame, shows
 *   Paused and closes its connection; it goes live again when a slot opens.
 * - A visible tile that was never live (the live budget was full) gets one
 *   short turn, state 'peek': it connects like a live tile, paints its first
 *   snapshot, then the Wall calls peekDone and the tile is Paused with that
 *   picture. One turn at a time, one try per tile for as long as the Wall is
 *   open (a failed turn is not repeated; opening a terminal closes the Wall and
 *   forgets which tiles had a turn), only while the Wall is open and in the foreground, only when
 *   no other connection attempt is running or waiting, never for a computer
 *   that is backing off, and at most `peekMs` and `peekBytes` long. The turn
 *   uses the spare slot of `tileConnections`; live tiles keep their 4.
 * - Connection attempts across all computers pass connectGate: at most
 *   `concurrentConnects` at once (the active terminal never waits), and a
 *   computer that keeps failing waits a jittered backoff, then an offline
 *   cooldown, before background attempts.
 * - Reads across all computers pass read: at most `concurrentReads` at once.
 *   Provider reads use their own lane of `concurrentProviderReads`, so a
 *   provider read that hangs never holds a slot a session read needs.
 *   poll polls up to `maxComputers` computers with one outstanding poll per
 *   computer and publishes each computer's result as soon as it arrives.
 *
 * Pure apart from timers (global setTimeout, so fake timers work) and an
 * injectable clock and random source. Tile changes are announced in a
 * microtask, never inside the caller's own call stack.
 */

export type TileState = 'live' | 'peek' | 'paused';

export const DEFAULT_LIMITS = {
  /** Live Wall tiles; tuned on real phones. */
  liveTiles: 4,
  /** Open Wall tile connections: the live tiles and one snapshot turn. */
  tileConnections: 5,
  /** The longest a never-live tile's snapshot turn lasts. */
  peekMs: 8_000,
  /** The most decoded bytes one snapshot turn may receive. */
  peekBytes: 1024 * 1024,
  /** A turn starts this long after the connection gate went quiet, so live tiles connect first. */
  peekDelayMs: 1_000,
  /** Decoded bytes per second across all live tiles. */
  tileBytesPerSecond: 1024 * 1024,
  /** How long a tile paused for bytes stays paused. */
  bytePauseMs: 10_000,
  /** Computers polled at most: home and up to 8 peers. */
  maxComputers: 9,
  concurrentReads: 3,
  /** Provider reads (lane 'providers'), apart from the session reads above. */
  concurrentProviderReads: 2,
  concurrentConnects: 2,
  pollIntervalMs: 5_000,
  retryBaseMs: 1_000,
  retryMaxMs: 15_000,
  /** After this many failures in a row, a computer counts as offline. */
  offlineAfterFailures: 3,
  offlineCooldownMs: 30_000,
};

export type SchedulerLimits = Readonly<typeof DEFAULT_LIMITS>;

/** The rate a thumbnail viewer asks the broker for (`session.attach` `thumbnail.maxUpdatesPerSecond`). */
export const THUMBNAIL_UPDATES_PER_SECOND = 5;

export interface TileHandle {
  /** The current state; changes are also sent to onChange. */
  state(): TileState;
  /** Offscreen tiles are paused and free their slot. */
  setVisible(visible: boolean): void;
  /** Reports decoded bytes this tile's connection received (ConnectionOptions.onMessageBytes). */
  bytes(count: number): void;
  /** The 'peek' turn is over (picture painted, or the attempt failed). One try per tile while the Wall is open. */
  peekDone(): void;
  release(): void;
}

export interface TileOptions {
  /** The computer this tile's session runs on; a computer that is backing off gets no snapshot turn. */
  readonly origin: string;
  /** Called (in a microtask) when the state changes; not for the initial state. */
  readonly onChange: (state: TileState) => void;
}

/** Admits one connection attempt at a time; see Scheduler.connectGate. */
export interface ConnectGate {
  /** Calls `go` once an attempt may start (maybe at once); returns a cancel function for a waiting attempt. */
  wait(go: () => void): () => void;
  /**
   * The attempt `go` started ended. `live` and `failed` count for the
   * computer's health; `cancelled` (closed by the app) only frees the slot.
   * Call once per `go`.
   */
  done(result: 'live' | 'failed' | 'cancelled'): void;
}

/** The read budget a read uses: session reads, or provider reads with their own smaller budget. */
export type ReadLane = 'sessions' | 'providers';

export type PollOutcome<T> = { readonly ok: true; readonly value: T } | { readonly ok: false; readonly error: unknown };

interface Tile {
  readonly key: string;
  readonly origin: string;
  readonly onChange: (state: TileState) => void;
  visible: boolean;
  state: TileState;
  notified: TileState;
  pausedUntil: number;
  /** Bytes in the current one-second bucket. */
  bytes: number;
  bucket: number;
  /** Was live, or had its snapshot turn: no more turns. */
  seen: boolean;
  /** Bytes of the current snapshot turn. */
  turnBytes: number;
  turnTimer: ReturnType<typeof setTimeout> | undefined;
}

interface Waiter {
  readonly origin: string;
  readonly go: () => void;
}

interface Health {
  failures: number;
  retryAt: number;
}

export class Scheduler {
  readonly limits: SchedulerLimits;
  private readonly now: () => number;
  private readonly random: () => number;
  private readonly tiles = new Map<string, Tile>();
  private active: string | undefined;
  private foreground = true;
  private wakeTimer: ReturnType<typeof setTimeout> | undefined;
  private peekTimer: ReturnType<typeof setTimeout> | undefined;
  private connecting = 0;
  private readonly connectQueue: Waiter[] = [];
  private connectTimer: ReturnType<typeof setTimeout> | undefined;
  private readonly lanes: Record<ReadLane, { reading: number; readonly queue: Array<() => void> }> = {
    sessions: { reading: 0, queue: [] },
    providers: { reading: 0, queue: [] },
  };
  private readonly health = new Map<string, Health>();
  /** The last read queued per computer; the next one starts after it. */
  private readonly flights = new Map<string, Promise<unknown>>();
  /** Each running poll's kick: polls one computer now, or all when undefined. */
  private readonly polls = new Set<(origin?: string) => void>();

  constructor(limits: Partial<SchedulerLimits> = {}, deps: { now?: () => number; random?: () => number } = {}) {
    this.limits = { ...DEFAULT_LIMITS, ...limits };
    this.now = deps.now ?? (() => Date.now());
    this.random = deps.random ?? Math.random;
  }

  /**
   * Asks for a live Wall tile slot. A new tile counts as offscreen (paused)
   * until setVisible(true). Read `state()` at once; later changes go to onChange.
   */
  requestLiveTile(key: string, options: TileOptions): TileHandle {
    // Asking again for a key replaces the old request (its handle stops working).
    clearTimeout(this.tiles.get(key)?.turnTimer);
    this.tiles.delete(key);
    const tile: Tile = {
      key,
      origin: options.origin,
      onChange: options.onChange,
      // Offscreen until the IntersectionObserver says otherwise, so tiles
      // below the fold never take the first live slots.
      visible: false,
      state: 'paused',
      notified: 'paused',
      pausedUntil: 0,
      bytes: 0,
      bucket: 0,
      seen: false,
      turnBytes: 0,
      turnTimer: undefined,
    };
    this.tiles.set(key, tile);
    this.plan();
    tile.notified = tile.state;
    const current = () => this.tiles.get(key) === tile;
    return {
      state: () => tile.state,
      setVisible: (visible) => {
        if (current() && tile.visible !== visible) {
          tile.visible = visible;
          this.plan();
        }
      },
      bytes: (count) => {
        if (current()) this.addBytes(tile, count);
      },
      peekDone: () => {
        if (current() && tile.state === 'peek') this.endPeek(tile);
      },
      release: () => {
        if (current()) {
          clearTimeout(tile.turnTimer);
          this.tiles.delete(key);
          this.plan();
        }
      },
    };
  }

  /** The session open in Live, or undefined. While set, every Wall tile is paused. */
  setActive(key: string | undefined): void {
    this.active = key;
    this.plan();
  }

  /** False while the app is in the background: tiles pause and polls wait. */
  setForeground(foreground: boolean): void {
    this.foreground = foreground;
    this.plan();
    if (foreground) {
      for (const kick of this.polls) kick();
    }
  }

  /**
   * The gate for one Connection to `origin` (ConnectionOptions.gate). The
   * active terminal passes `active` and never waits, though it holds a slot.
   */
  connectGate(origin: string, active = false): ConnectGate {
    let held = false;
    return {
      wait: (go) => {
        const start = () => {
          held = true;
          this.connecting++;
          go();
        };
        if (active) {
          start();
          return () => undefined;
        }
        const waiter: Waiter = { origin, go: start };
        this.connectQueue.push(waiter);
        this.pumpConnects();
        return () => {
          const index = this.connectQueue.indexOf(waiter);
          if (index >= 0) this.connectQueue.splice(index, 1);
          this.planPeek();
        };
      },
      done: (result) => {
        if (!held) return;
        held = false;
        this.connecting--;
        if (result !== 'cancelled') this.record(origin, result === 'live');
        this.pumpConnects();
      },
    };
  }

  /** Runs `load` when the lane has a free slot: `concurrentReads` for sessions, `concurrentProviderReads` for providers, across all computers. */
  read<T>(load: () => Promise<T>, lane: ReadLane = 'sessions'): Promise<T> {
    const slots = this.lanes[lane];
    return new Promise<T>((resolve, reject) => {
      slots.queue.push(() => {
        slots.reading++;
        // Promise.resolve().then: a load that throws at once still frees its slot.
        Promise.resolve().then(load).then(resolve, reject).finally(() => {
          slots.reading--;
          this.pumpReads(lane);
        });
      });
      this.pumpReads(lane);
    });
  }

  /**
   * Like read, but one at a time per computer: `load` starts only after every
   * earlier serial read of `origin` has settled. Polls, reloads and route
   * reads all pass here, so at most one request per computer is in flight.
   */
  serial<T>(origin: string, load: () => Promise<T>, lane: ReadLane = 'sessions'): Promise<T> {
    const after = this.flights.get(origin) ?? Promise.resolve();
    const next = after.then(() => this.read(load, lane));
    const settled = next.then(() => undefined, () => undefined);
    this.flights.set(origin, settled);
    void settled.then(() => {
      if (this.flights.get(origin) === settled) this.flights.delete(origin);
    });
    return next;
  }

  /**
   * Polls each computer (the first `maxComputers`) with `load` every
   * `pollIntervalMs`: one outstanding poll per computer, reads limited by
   * read, and each result published as soon as it arrives. A failing
   * computer is retried after a jittered backoff, then an offline cooldown;
   * retry() polls it at once. Polls wait while the app is in the background.
   * Returns stop.
   */
  poll<T>(
    origins: ReadonlyArray<string>,
    load: (origin: string) => Promise<T>,
    publish: (origin: string, outcome: PollOutcome<T>) => void,
    lane: ReadLane = 'sessions',
  ): () => void {
    const timers = new Map<string, ReturnType<typeof setTimeout>>();
    const open = new Set<string>();
    let stopped = false;
    const run = (origin: string) => {
      clearTimeout(timers.get(origin));
      timers.delete(origin);
      if (stopped || open.has(origin) || !this.foreground) return;
      open.add(origin);
      this.serial(origin, () => load(origin), lane).then(
        (value) => {
          this.record(origin, true);
          if (!stopped) publish(origin, { ok: true, value });
        },
        (error: unknown) => {
          this.record(origin, false);
          if (!stopped) publish(origin, { ok: false, error });
        },
      ).finally(() => {
        open.delete(origin);
        if (stopped) return;
        const wait = Math.max(this.limits.pollIntervalMs, (this.health.get(origin)?.retryAt ?? 0) - this.now());
        timers.set(origin, setTimeout(() => run(origin), wait));
      });
    };
    const list = origins.slice(0, this.limits.maxComputers);
    const kick = (only?: string) => {
      for (const origin of list) if (only === undefined || only === origin) run(origin);
    };
    this.polls.add(kick);
    kick();
    return () => {
      stopped = true;
      this.polls.delete(kick);
      for (const timer of timers.values()) clearTimeout(timer);
    };
  }

  /** The user pressed Retry: forget the computer's failures and poll and connect now. */
  retry(origin: string): void {
    this.health.delete(origin);
    this.pumpConnects();
    for (const kick of this.polls) kick(origin);
  }

  /** A computer was removed: drop its failure count and backoff. */
  forget(origin: string): void {
    this.health.delete(origin);
  }

  /** A read outside the poll (Check again, a reload) ended: it counts for the computer's health, and an answer wakes waiting connects. */
  reported(origin: string, ok: boolean): void {
    this.record(origin, ok);
    if (ok) this.pumpConnects();
  }

  /** True after `offlineAfterFailures` failures in a row: show "<label> · can't reach". */
  offline(origin: string): boolean {
    return (this.health.get(origin)?.failures ?? 0) >= this.limits.offlineAfterFailures;
  }

  private record(origin: string, ok: boolean): void {
    if (ok) {
      this.health.delete(origin);
      this.planPeek();
      return;
    }
    const failures = (this.health.get(origin)?.failures ?? 0) + 1;
    const { retryBaseMs, retryMaxMs, offlineAfterFailures, offlineCooldownMs } = this.limits;
    const base = failures >= offlineAfterFailures ? offlineCooldownMs : Math.min(retryMaxMs, retryBaseMs * 2 ** (failures - 1));
    // Jitter (0.5x to 1.5x) so computers and tiles do not retry in step.
    this.health.set(origin, { failures, retryAt: this.now() + base * (0.5 + this.random()) });
  }

  private pumpReads(lane: ReadLane): void {
    const slots = this.lanes[lane];
    const limit = lane === 'providers' ? this.limits.concurrentProviderReads : this.limits.concurrentReads;
    while (slots.reading < limit) {
      const next = slots.queue.shift();
      if (next === undefined) return;
      next();
    }
  }

  private pumpConnects(): void {
    clearTimeout(this.connectTimer);
    this.connectTimer = undefined;
    const now = this.now();
    let wake = Infinity;
    for (let index = 0; index < this.connectQueue.length && this.connecting < this.limits.concurrentConnects;) {
      const waiter = this.connectQueue[index] as Waiter;
      const retryAt = this.health.get(waiter.origin)?.retryAt ?? 0;
      if (retryAt > now) {
        wake = Math.min(wake, retryAt);
        index++;
        continue;
      }
      this.connectQueue.splice(index, 1);
      waiter.go();
    }
    if (wake !== Infinity) {
      this.connectTimer = setTimeout(() => this.pumpConnects(), wake - now);
    }
    this.planPeek();
  }

  private addBytes(tile: Tile, count: number): void {
    if (tile.state === 'peek') {
      tile.turnBytes += count;
      if (tile.turnBytes > this.limits.peekBytes) this.endPeek(tile);
      return;
    }
    if (tile.state !== 'live') return;
    // ponytail: fixed one-second buckets, not a sliding window; enough to catch a flood.
    const bucket = Math.floor(this.now() / 1000);
    let total = 0;
    let noisiest = tile;
    for (const other of this.tiles.values()) {
      if (other.state !== 'live') continue;
      if (other.bucket !== bucket) {
        other.bucket = bucket;
        other.bytes = 0;
      }
      if (other === tile) other.bytes += count;
      total += other.bytes;
      if (other.bytes > noisiest.bytes) noisiest = other;
    }
    if (total > this.limits.tileBytesPerSecond) {
      noisiest.pausedUntil = this.now() + this.limits.bytePauseMs;
      noisiest.bytes = 0;
      this.plan();
    }
  }

  /** Decides which tiles are live: live ones keep their slot, then waiting ones in request order. */
  private plan(): void {
    const now = this.now();
    const open = this.foreground && this.active === undefined;
    let live = this.limits.liveTiles;
    let connections = this.limits.tileConnections;
    const next = new Map<Tile, TileState>();
    const consider = (tile: Tile) => {
      if (open && tile.visible && now >= tile.pausedUntil && live > 0 && connections > 0) {
        next.set(tile, 'live');
        live--;
        connections--;
      } else {
        // A turn goes on only while the Wall is open and the tile visible.
        next.set(tile, tile.state === 'peek' && !tile.seen && open && tile.visible ? 'peek' : 'paused');
      }
    };
    for (const tile of this.tiles.values()) if (tile.state === 'live') consider(tile);
    for (const tile of this.tiles.values()) if (!next.has(tile)) consider(tile);
    let wake = Infinity;
    for (const [tile, state] of next) {
      if (tile.pausedUntil > now && tile.visible) wake = Math.min(wake, tile.pausedUntil);
      if (tile.state !== state) {
        if (tile.state === 'peek') clearTimeout(tile.turnTimer);
        // A turn cut short (tile scrolled away) is not a try; only live and a finished turn count.
        if (state === 'live') tile.seen = true;
        tile.state = state;
        this.announce(tile);
      }
    }
    clearTimeout(this.wakeTimer);
    this.wakeTimer = wake === Infinity ? undefined : setTimeout(() => this.plan(), wake - now);
    this.planPeek();
  }

  private endPeek(tile: Tile): void {
    tile.seen = true;
    this.plan();
  }

  /** The tile that may have a snapshot turn now, or undefined. */
  private peekCandidate(): Tile | undefined {
    if (!this.foreground || this.active !== undefined || this.connecting > 0) return undefined;
    const now = this.now();
    // A waiter for a computer that is backing off cannot run yet; it must not hold every other computer's turn.
    if (this.connectQueue.some((waiter) => (this.health.get(waiter.origin)?.retryAt ?? 0) <= now)) return undefined;
    let live = 0;
    let candidate: Tile | undefined;
    for (const tile of this.tiles.values()) {
      // One turn at a time.
      if (tile.state === 'peek') return undefined;
      if (tile.state === 'live') live++;
      else if (candidate === undefined && tile.visible && !tile.seen && (this.health.get(tile.origin)?.retryAt ?? 0) <= now) candidate = tile;
    }
    return live < this.limits.tileConnections ? candidate : undefined;
  }

  /** Arms the start of the next snapshot turn; every change that could allow one calls this. */
  private planPeek(): void {
    if (this.peekCandidate() === undefined) {
      clearTimeout(this.peekTimer);
      this.peekTimer = undefined;
      return;
    }
    if (this.peekTimer !== undefined) return;
    this.peekTimer = setTimeout(() => {
      this.peekTimer = undefined;
      const tile = this.peekCandidate();
      if (tile === undefined) return;
      tile.state = 'peek';
      tile.turnBytes = 0;
      tile.turnTimer = setTimeout(() => this.endPeek(tile), this.limits.peekMs);
      this.announce(tile);
    }, this.limits.peekDelayMs);
  }

  private announce(tile: Tile): void {
    queueMicrotask(() => {
      if (this.tiles.get(tile.key) === tile && tile.state !== tile.notified) {
        tile.notified = tile.state;
        tile.onChange(tile.state);
      }
    });
  }
}

/** The one scheduler for the whole phone. */
export const scheduler = new Scheduler();
