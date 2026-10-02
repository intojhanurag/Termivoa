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
 * - Connection attempts across all computers pass connectGate: at most
 *   `concurrentConnects` at once (the active terminal never waits), and a
 *   computer that keeps failing waits a jittered backoff, then an offline
 *   cooldown, before background attempts.
 * - Reads across all computers pass read: at most `concurrentReads` at once.
 *   poll polls up to `maxComputers` computers with one outstanding poll per
 *   computer and publishes each computer's result as soon as it arrives.
 *
 * Pure apart from timers (global setTimeout, so fake timers work) and an
 * injectable clock and random source. Tile changes are announced in a
 * microtask, never inside the caller's own call stack.
 */

export type TileState = 'live' | 'paused';

export const DEFAULT_LIMITS = {
  /** Live Wall tiles; tuned on real phones. */
  liveTiles: 4,
  /** Open Wall tile connections. */
  tileConnections: 4,
  /** Decoded bytes per second across all live tiles. */
  tileBytesPerSecond: 1024 * 1024,
  /** How long a tile paused for bytes stays paused. */
  bytePauseMs: 10_000,
  /** Computers polled at most: home and up to 8 peers. */
  maxComputers: 9,
  concurrentReads: 3,
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
  release(): void;
}

export interface TileOptions {
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

export type PollOutcome<T> = { readonly ok: true; readonly value: T } | { readonly ok: false; readonly error: unknown };

interface Tile {
  readonly key: string;
  readonly onChange: (state: TileState) => void;
  visible: boolean;
  state: TileState;
  notified: TileState;
  pausedUntil: number;
  /** Bytes in the current one-second bucket. */
  bytes: number;
  bucket: number;
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
  private connecting = 0;
  private readonly connectQueue: Waiter[] = [];
  private connectTimer: ReturnType<typeof setTimeout> | undefined;
  private reading = 0;
  private readonly readQueue: Array<() => void> = [];
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
    this.tiles.delete(key);
    const tile: Tile = {
      key,
      onChange: options.onChange,
      // Offscreen until the IntersectionObserver says otherwise, so tiles
      // below the fold never take the first live slots.
      visible: false,
      state: 'paused',
      notified: 'paused',
      pausedUntil: 0,
      bytes: 0,
      bucket: 0,
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
      release: () => {
        if (current()) {
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

  /** Runs `load` when fewer than `concurrentReads` reads are open across all computers. */
  read<T>(load: () => Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      this.readQueue.push(() => {
        this.reading++;
        // Promise.resolve().then: a load that throws at once still frees its slot.
        Promise.resolve().then(load).then(resolve, reject).finally(() => {
          this.reading--;
          this.pumpReads();
        });
      });
      this.pumpReads();
    });
  }

  /**
   * Like read, but one at a time per computer: `load` starts only after every
   * earlier serial read of `origin` has settled. Polls, reloads and route
   * reads all pass here, so at most one request per computer is in flight.
   */
  serial<T>(origin: string, load: () => Promise<T>): Promise<T> {
    const after = this.flights.get(origin) ?? Promise.resolve();
    const next = after.then(() => this.read(load));
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
  ): () => void {
    const timers = new Map<string, ReturnType<typeof setTimeout>>();
    const open = new Set<string>();
    let stopped = false;
    const run = (origin: string) => {
      clearTimeout(timers.get(origin));
      timers.delete(origin);
      if (stopped || open.has(origin) || !this.foreground) return;
      open.add(origin);
      this.serial(origin, () => load(origin)).then(
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

  /** True after `offlineAfterFailures` failures in a row: show "<label> · can't reach". */
  offline(origin: string): boolean {
    return (this.health.get(origin)?.failures ?? 0) >= this.limits.offlineAfterFailures;
  }

  private record(origin: string, ok: boolean): void {
    if (ok) {
      this.health.delete(origin);
      return;
    }
    const failures = (this.health.get(origin)?.failures ?? 0) + 1;
    const { retryBaseMs, retryMaxMs, offlineAfterFailures, offlineCooldownMs } = this.limits;
    const base = failures >= offlineAfterFailures ? offlineCooldownMs : Math.min(retryMaxMs, retryBaseMs * 2 ** (failures - 1));
    // Jitter (0.5x to 1.5x) so computers and tiles do not retry in step.
    this.health.set(origin, { failures, retryAt: this.now() + base * (0.5 + this.random()) });
  }

  private pumpReads(): void {
    while (this.reading < this.limits.concurrentReads) {
      const next = this.readQueue.shift();
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
  }

  private addBytes(tile: Tile, count: number): void {
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
        next.set(tile, 'paused');
      }
    };
    for (const tile of this.tiles.values()) if (tile.state === 'live') consider(tile);
    for (const tile of this.tiles.values()) if (!next.has(tile)) consider(tile);
    let wake = Infinity;
    for (const [tile, state] of next) {
      if (tile.pausedUntil > now && tile.visible) wake = Math.min(wake, tile.pausedUntil);
      if (tile.state !== state) {
        tile.state = state;
        this.announce(tile);
      }
    }
    clearTimeout(this.wakeTimer);
    this.wakeTimer = wake === Infinity ? undefined : setTimeout(() => this.plan(), wake - now);
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
