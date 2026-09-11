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
const FRAME_TYPES = ["hello", "lagged", "heartbeat", "status", "incident"] as const;

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

export type ConnectionOptions = {
  url: string;
  /** Injected so tests never touch a real network. */
  create: EventSourceFactory;
  /** Injected for the same reason; defaults to the real timers. */
  setTimer?: (fn: () => void, ms: number) => number;
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
    this.source?.close();
    this.source = null;
  }

  private open(): void {
    const source = this.opts.create(this.opts.url);
    this.source = source;

    source.onopen = () => {
      // A successful open resets the ladder: the next outage starts at one
      // second again rather than inheriting the last one's patience.
      this.attempt = 0;
      this.setStatus("live");
    };

    source.onerror = () => {
      this.setStatus("offline");
      // readyState CONNECTING (0) means the browser is retrying by itself and
      // a second socket here would be a duplicate subscription. Only a CLOSED
      // stream (2) is ours to reopen.
      if (source.readyState === 2) this.scheduleReopen();
    };

    for (const type of FRAME_TYPES) {
      source.addEventListener(type, (event: MessageEvent) => {
        // Any frame arriving is proof the stream works, including one that
        // arrives before onopen fired.
        this.setStatus("live");
        this.emit({
          type,
          data: typeof event.data === "string" ? event.data : "",
          lastEventId: event.lastEventId ?? "",
        });
      });
    }
  }

  private scheduleReopen(): void {
    if (this.stopped || this.timer !== null) return;
    const delay = RETRY_LADDER_MS[Math.min(this.attempt, RETRY_LADDER_MS.length - 1)];
    this.attempt += 1;
    this.timer = this.opts.setTimer(() => {
      this.timer = null;
      if (this.stopped) return;
      this.source?.close();
      this.open();
    }, delay);
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
