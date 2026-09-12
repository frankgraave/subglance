/**
 * Owning the event stream, outside React.
 *
 * This is a plain class rather than a hook because the interesting behaviour —
 * when a connection counts as lost, how long before it is retried, what a
 * reconnect does to a client that was away — is a state machine, and a state
 * machine is far easier to get right when it can be driven directly by a test
 * with a stub transport than through render cycles.
 *
 * React reaches it through `useSyncExternalStore`, which is also why the
 * subscribe/snapshot shape is what it is.
 */

/** The bit of EventSource this class uses. Narrow, so a test can stub it. */
export type EventSourceLike = {
  addEventListener(type: string, listener: (event: MessageEvent) => void): void;
  close(): void;
  onopen: ((event: Event) => void) | null;
  onerror: ((event: Event) => void) | null;
  readyState: number;
};

export type EventSourceFactory = (url: string) => EventSourceLike;

/**
 * What the user is told about the connection.
 *
 * Three states, not two. "Connecting" covers the first attempt, when nothing
 * has been shown yet and a warning would be alarmism; "offline" means we had a
 * stream and lost it, which is the state worth interrupting someone for
 * because every number on screen is now frozen.
 */
export type ConnectionStatus = "connecting" | "live" | "offline";

/**
 * Frames this class hands on, already tagged with their SSE event name.
 * Parsing is `events.ts`'s job; this one only moves bytes.
 */
export type RawFrame = { type: string; data: string; lastEventId: string };

/** Event names subscribed to. Unknown names are ignored by the browser. */
const FRAME_TYPES = ["hello", "ping", "lagged", "heartbeat", "status", "incident"] as const;

/**
 * How much silence counts as a dead stream, as a multiple of the server's
 * keepalive interval.
 *
 * 2.5 rather than 1.x because one missed ping is not evidence: a tab that the
 * browser throttled in the background, a laptop that slept for a moment, or a
 * proxy that buffered a frame all produce a single late ping on a perfectly
 * healthy connection, and a dashboard that drains its colour every time
 * someone switches tabs teaches people to ignore the signal. Three missed
 * pings in a row is not a hiccup.
 */
export const STALE_AFTER_PINGS = 2.5;

/**
 * Fallback keepalive interval, used until `hello` states the real one.
 *
 * Matches `sseHeartbeatInterval` in internal/api/stream.go. It is only a
 * starting value: the server reports its actual interval in `hello`, so a
 * deployment that tunes it is followed rather than second-guessed.
 */
export const DEFAULT_PING_INTERVAL_MS = 20_000;

/**
 * Reconnect backoff, in milliseconds.
 *
 * The browser retries an EventSource on its own, but only while it keeps the
 * socket; a stream the server closed outright (a restart, a 502 from a proxy)
 * leaves it CLOSED and it never comes back. This ladder covers that case.
 *
 * It climbs, and it stops at 30s rather than growing forever: a monitoring
 * dashboard is usually the thing people stare at *during* the outage that
 * killed the connection, and a client that has backed off to five minutes is
 * useless exactly when it is needed. Capping at 30s bounds the herd on a
 * restarting server while keeping recovery within a coffee sip.
 */
export const RETRY_LADDER_MS = [1_000, 2_000, 5_000, 10_000, 30_000];

/**
 * Which of the two timers this class runs.
 *
 * The real implementation ignores it — a timeout is a timeout — but the two
 * mean opposite things (one waits before retrying, one gives up on waiting),
 * so naming them keeps a test from asserting about whichever happened to be
 * scheduled first.
 */
export type TimerKind = "retry" | "watchdog";

export type ConnectionOptions = {
  url: string;
  /** Injected so tests never touch a real network. */
  create: EventSourceFactory;
  /** Injected for the same reason; defaults to the real timers. */
  setTimer?: (fn: () => void, ms: number, kind: TimerKind) => number;
  clearTimer?: (handle: number) => void;
};

export class LiveConnection {
  private readonly opts: Required<ConnectionOptions>;
  private source: EventSourceLike | null = null;
  private state: ConnectionStatus = "connecting";
  private readonly statusListeners = new Set<() => void>();
  private readonly frameListeners = new Set<(frame: RawFrame) => void>();
  private attempt = 0;
  private timer: number | null = null;
  private watchdog: number | null = null;
  private pingIntervalMs = DEFAULT_PING_INTERVAL_MS;
  private stopped = true;

  constructor(opts: ConnectionOptions) {
    this.opts = {
      setTimer: (fn, ms) => globalThis.setTimeout(fn, ms) as unknown as number,
      clearTimer: (handle) => globalThis.clearTimeout(handle),
      ...opts,
    };
  }

  /** Current connection state. Stable identity, safe for useSyncExternalStore. */
  getStatus = (): ConnectionStatus => this.state;

  /** Subscribes to status changes; returns the unsubscribe. */
  subscribe = (listener: () => void): (() => void) => {
    this.statusListeners.add(listener);
    return () => this.statusListeners.delete(listener);
  };

  /**
   * Adopts the keepalive interval the server announced in `hello`.
   *
   * Parsing that frame is `events.ts`'s job, so the value is handed in rather
   * than read here — this class still only moves bytes.
   */
  setPingInterval(ms: number): void {
    if (!Number.isFinite(ms) || ms <= 0) return;
    this.pingIntervalMs = ms;
    this.armWatchdog();
  }

  /**
   * Reopens the stream immediately, ignoring the backoff ladder.
   *
   * Wired to the banner's button. Someone staring at a dashboard they know is
   * broken must never have to sit out our patience (DESIGN.md §6), so this
   * also resets the ladder: an explicit ask is not a failed retry.
   */
  reconnect = (): void => {
    if (this.stopped) {
      this.start();
      return;
    }
    this.cancelTimer();
    this.attempt = 0;
    this.open();
  };

  /** Subscribes to stream frames; returns the unsubscribe. */
  onFrame(listener: (frame: RawFrame) => void): () => void {
    this.frameListeners.add(listener);
    return () => {
      this.frameListeners.delete(listener);
    };
  }

  /** Opens the stream. Calling it twice is a no-op, not a second socket. */
  start(): void {
    if (!this.stopped) return;
    this.stopped = false;
    this.open();
  }

  /** Closes the stream and cancels any pending retry. */
  stop(): void {
    this.stopped = true;
    this.cancelTimer();
    this.cancelWatchdog();
    this.source?.close();
    this.source = null;
  }

  /**
   * Replaces the current source, and it is the only place that does.
   *
   * Closing the old socket and cancelling its watchdog happen here together
   * because they are one act. When they were separate, `reconnect()` closed
   * the source but left the watchdog armed, so a timer belonging to a stream
   * that no longer exists could fire against its replacement and declare a
   * healthy connection offline.
   */
  private open(): void {
    this.cancelWatchdog();
    this.source?.close();

    const source = this.opts.create(this.opts.url);
    this.source = source;

    /*
     * Every callback below is scoped to the source that registered it.
     *
     * A closed EventSource is supposed to go quiet, but "supposed to" is doing
     * a lot of work across browsers and, once a frame is in flight, across the
     * event loop too. Without this guard a dying socket's last error can
     * schedule a reopen for the socket that replaced it — the reconnect loop
     * this class exists to avoid.
     */
    const current = () => this.source === source;

    source.onopen = () => {
      if (!current()) return;
      this.markAlive();
    };

    source.onerror = () => {
      if (!current()) return;
      this.setStatus("offline");
      // readyState CONNECTING (0) means the browser is retrying by itself and
      // a second socket here would be a duplicate subscription. Only a CLOSED
      // stream (2) is ours to reopen.
      if (source.readyState === 2) this.scheduleReopen();
    };

    for (const type of FRAME_TYPES) {
      source.addEventListener(type, (event: MessageEvent) => {
        if (!current()) return;
        // Any frame arriving is proof the stream works, including one that
        // arrives before onopen fired.
        this.markAlive();
        this.emit({
          type,
          data: typeof event.data === "string" ? event.data : "",
          lastEventId: event.lastEventId ?? "",
        });
      });
    }
  }

  /**
   * Records proof that the current source is working.
   *
   * Cancelling the pending retry is the part that is easy to miss. A watchdog
   * that fired a moment before the stream came back leaves a retry armed, and
   * that retry closes a source which is now delivering frames — the dashboard
   * drops out for no reason, and on a stream that only just recovered it can
   * do so repeatedly. Evidence of life has to disarm the recovery machinery,
   * not just push the watchdog back.
   */
  private markAlive(): void {
    this.cancelTimer();
    // The next outage starts at one second again rather than inheriting the
    // patience the last one earned.
    this.attempt = 0;
    this.setStatus("live");
    this.armWatchdog();
  }

  private scheduleReopen(): void {
    if (this.stopped || this.timer !== null) return;
    const delay = RETRY_LADDER_MS[Math.min(this.attempt, RETRY_LADDER_MS.length - 1)];
    this.attempt += 1;
    this.timer = this.opts.setTimer(() => {
      this.timer = null;
      if (this.stopped) return;
      this.open();
    }, delay, "retry");
  }

  /**
   * (Re)starts the silence watchdog.
   *
   * `readyState` is not enough on its own. A TCP connection that is open but
   * mute — a proxy holding the socket after the origin died, a server wedged
   * mid-handler — leaves the browser reporting a perfectly healthy stream
   * while nothing arrives, and that is the failure mode that makes a
   * monitoring dashboard lie confidently (DESIGN.md §6). Silence longer than
   * the server's own promised keepalive is therefore treated as an outage no
   * matter what the socket claims.
   */
  private armWatchdog(): void {
    this.cancelWatchdog();
    if (this.stopped) return;
    const delay = this.pingIntervalMs * STALE_AFTER_PINGS;
    this.watchdog = this.opts.setTimer(() => {
      this.watchdog = null;
      if (this.stopped) return;
      this.setStatus("offline");
      // The socket is not trustworthy, so it is replaced rather than waited
      // on. This goes through the ladder: a mute stream that reopens mute
      // must not become a reconnect loop at full speed.
      this.scheduleReopen();
    }, delay, "watchdog");
  }

  private cancelWatchdog(): void {
    if (this.watchdog !== null) {
      this.opts.clearTimer(this.watchdog);
      this.watchdog = null;
    }
  }

  private cancelTimer(): void {
    if (this.timer !== null) {
      this.opts.clearTimer(this.timer);
      this.timer = null;
    }
  }

  private setStatus(next: ConnectionStatus): void {
    if (this.state === next) return;
    this.state = next;
    for (const listener of this.statusListeners) listener();
  }

  private emit(frame: RawFrame): void {
    for (const listener of this.frameListeners) listener(frame);
  }
}
