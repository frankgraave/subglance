import { describe, expect, it } from "vitest";
import {
  DEFAULT_PING_INTERVAL_MS,
  LiveConnection,
  RETRY_LADDER_MS,
  STALE_AFTER_PINGS,
} from "./connection";
import type { EventSourceLike, RawFrame, TimerKind } from "./connection";

/** A stub EventSource, driven by the test instead of a network. */
class FakeSource implements EventSourceLike {
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  readyState = 0;
  closed = false;
  private readonly listeners = new Map<string, (event: MessageEvent) => void>();

  addEventListener(type: string, listener: (event: MessageEvent) => void): void {
    this.listeners.set(type, listener);
  }

  close(): void {
    this.closed = true;
    this.readyState = 2;
  }

  open(): void {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }

  /** Server closed the stream: the browser will not retry this one. */
  fail(): void {
    this.readyState = 2;
    this.onerror?.(new Event("error"));
  }

  /** Transport blip: the browser retries the same socket by itself. */
  blip(): void {
    this.readyState = 0;
    this.onerror?.(new Event("error"));
  }

  send(type: string, data: string): void {
    this.listeners.get(type)?.({ data, lastEventId: "" } as MessageEvent);
  }
}

type Scheduled = { fn: () => void; ms: number; kind: TimerKind; handle: number };

/** A manual clock, so backoff is asserted rather than waited for. */
function harness() {
  const sources: FakeSource[] = [];
  const scheduled: Scheduled[] = [];
  let nextHandle = 1;
  const conn = new LiveConnection({
    url: "/api/v1/stream",
    create: () => {
      const s = new FakeSource();
      sources.push(s);
      return s;
    },
    setTimer: (fn, ms, kind) => {
      const handle = nextHandle;
      nextHandle += 1;
      scheduled.push({ fn, ms, kind, handle });
      return handle;
    },
    // Cancelling really removes it. A watchdog that keeps firing after it was
    // re-armed would make every test below assert about a stale timer.
    clearTimer: (handle) => {
      const at = scheduled.findIndex((t) => t.handle === handle);
      if (at >= 0) scheduled.splice(at, 1);
    },
  });
  const pending = (kind: TimerKind) => scheduled.filter((t) => t.kind === kind);
  const run = (kind: TimerKind) => {
    const at = scheduled.findIndex((t) => t.kind === kind);
    if (at < 0) throw new Error(`no ${kind} was scheduled`);
    const [t] = scheduled.splice(at, 1);
    t.fn();
    return t.ms;
  };
  return {
    conn,
    sources,
    scheduled,
    pending,
    run,
    runTimer: () => run("retry"),
    runWatchdog: () => run("watchdog"),
  };
}

describe("LiveConnection", () => {
  it("starts as connecting and goes live when the stream opens", () => {
    const { conn, sources } = harness();
    expect(conn.getStatus()).toBe("connecting");
    conn.start();
    expect(conn.getStatus()).toBe("connecting");
    sources[0].open();
    expect(conn.getStatus()).toBe("live");
  });

  it("reports offline the moment the stream errors", () => {
    const { conn, sources } = harness();
    conn.start();
    sources[0].open();
    sources[0].fail();
    // This is the whole point of the issue: a frozen dashboard must say so.
    expect(conn.getStatus()).toBe("offline");
  });

  it("does not open a second stream while the browser is retrying", () => {
    const { conn, sources, pending } = harness();
    conn.start();
    sources[0].open();
    sources[0].blip();
    expect(conn.getStatus()).toBe("offline");
    // readyState CONNECTING means EventSource is reconnecting itself; a second
    // socket here would double every event for as long as both survive.
    expect(pending("retry")).toHaveLength(0);
    expect(sources).toHaveLength(1);
  });

  it("reopens a closed stream on a climbing backoff", () => {
    const { conn, sources, runTimer } = harness();
    conn.start();
    sources[0].open();

    sources[0].fail();
    expect(runTimer()).toBe(RETRY_LADDER_MS[0]);
    expect(sources).toHaveLength(2);

    sources[1].fail();
    expect(runTimer()).toBe(RETRY_LADDER_MS[1]);
    expect(sources).toHaveLength(3);
  });

  it("resets the backoff after a successful reconnect", () => {
    const { conn, sources, runTimer } = harness();
    conn.start();
    sources[0].open();
    sources[0].fail();
    runTimer();
    sources[1].open();
    expect(conn.getStatus()).toBe("live");
    sources[1].fail();
    // Back to one second: an hour-old outage must not make the next blip take
    // thirty seconds to recover from.
    expect(runTimer()).toBe(RETRY_LADDER_MS[0]);
  });

  it("caps the backoff instead of growing forever", () => {
    const { conn, sources, runTimer } = harness();
    conn.start();
    const last = RETRY_LADDER_MS[RETRY_LADDER_MS.length - 1];
    const waited: number[] = [];
    for (let i = 0; i < RETRY_LADDER_MS.length + 3; i += 1) {
      sources[i].fail();
      waited.push(runTimer());
    }
    expect(Math.max(...waited)).toBe(last);
    // A dashboard is watched *during* the outage that killed the stream, so
    // the ladder must plateau rather than back off into uselessness.
    expect(waited.slice(-3)).toEqual([last, last, last]);
  });

  it("delivers frames with their event name", () => {
    const { conn, sources } = harness();
    const seen: RawFrame[] = [];
    conn.onFrame((f) => seen.push(f));
    conn.start();
    sources[0].open();
    sources[0].send("heartbeat", "{\"ok\":true}");
    expect(seen).toEqual([{ type: "heartbeat", data: "{\"ok\":true}", lastEventId: "" }]);
  });

  it("counts an arriving frame as proof the stream is alive", () => {
    const { conn, sources } = harness();
    conn.start();
    sources[0].send("hello", "{}");
    expect(conn.getStatus()).toBe("live");
  });

  it("notifies status subscribers and stops after unsubscribing", () => {
    const { conn, sources } = harness();
    let calls = 0;
    const unsubscribe = conn.subscribe(() => {
      calls += 1;
    });
    conn.start();
    sources[0].open();
    expect(calls).toBe(1);
    unsubscribe();
    sources[0].fail();
    expect(calls).toBe(1);
  });

  it("ignores a second start and closes on stop", () => {
    const { conn, sources } = harness();
    conn.start();
    conn.start();
    expect(sources).toHaveLength(1);
    conn.stop();
    expect(sources[0].closed).toBe(true);
  });

  it("does not reopen after stop", () => {
    const { conn, sources, pending } = harness();
    conn.start();
    sources[0].open();
    conn.stop();
    sources[0].fail();
    expect(pending("retry")).toHaveLength(0);
    // And nothing is left ticking either: a watchdog outliving the connection
    // would flip a torn-down dashboard to offline.
    expect(pending("watchdog")).toHaveLength(0);
  });

  describe("silence watchdog", () => {
    it("goes offline when the stream falls silent, however healthy the socket looks", () => {
      const { conn, sources, runWatchdog } = harness();
      conn.start();
      sources[0].open();
      expect(conn.getStatus()).toBe("live");

      // No error, no close: readyState still says OPEN. This is the failure
      // mode that fools naive clients — a proxy holding a socket whose origin
      // died — and the whole reason the watchdog exists.
      expect(sources[0].readyState).toBe(1);
      runWatchdog();

      expect(conn.getStatus()).toBe("offline");
    });

    it("waits 2.5 keepalives before giving up", () => {
      const { conn, sources, pending } = harness();
      conn.start();
      sources[0].open();
      // One late ping is a throttled tab or a sleeping laptop, not an outage.
      expect(pending("watchdog")[0].ms).toBe(DEFAULT_PING_INTERVAL_MS * STALE_AFTER_PINGS);
    });

    it("follows the interval the server announced", () => {
      const { conn, sources, pending } = harness();
      conn.start();
      sources[0].open();
      conn.setPingInterval(5_000);
      expect(pending("watchdog")[0].ms).toBe(5_000 * STALE_AFTER_PINGS);
    });

    it("is pushed back by every arriving frame, including a ping", () => {
      const { conn, sources, pending, runWatchdog } = harness();
      conn.start();
      sources[0].open();

      sources[0].send("ping", "{}");
      // Exactly one armed watchdog, not two: re-arming must replace, not stack.
      expect(pending("watchdog")).toHaveLength(1);

      runWatchdog();
      expect(conn.getStatus()).toBe("offline");
    });

    it("replaces the mute socket rather than waiting on it", () => {
      const { conn, sources, runWatchdog, runTimer } = harness();
      conn.start();
      sources[0].open();
      runWatchdog();

      // Through the ladder, not immediately: a stream that reopens mute must
      // not become a reconnect loop at full speed.
      expect(runTimer()).toBe(RETRY_LADDER_MS[0]);
      expect(sources).toHaveLength(2);
    });

    it("recovers without a reload when frames come back", () => {
      const { conn, sources, runWatchdog, runTimer } = harness();
      conn.start();
      sources[0].open();
      runWatchdog();
      runTimer();
      sources[1].open();
      expect(conn.getStatus()).toBe("live");
    });
  });

  describe("manual reconnect", () => {
    it("opens a new stream now instead of waiting out the backoff", () => {
      const { conn, sources, pending } = harness();
      conn.start();
      sources[0].open();
      sources[0].fail();
      expect(pending("retry")).toHaveLength(1);

      conn.reconnect();

      // The pending retry is dropped rather than left to fire into a stream
      // that has already been replaced.
      expect(pending("retry")).toHaveLength(0);
      expect(sources).toHaveLength(2);
      expect(sources[0].closed).toBe(true);
    });

    it("resets the ladder, because an explicit ask is not a failed retry", () => {
      const { conn, sources, runTimer } = harness();
      conn.start();
      sources[0].open();
      sources[0].fail();
      runTimer();
      sources[1].fail();
      // The ladder has climbed a rung by now.
      conn.reconnect();
      sources[2].fail();
      expect(runTimer()).toBe(RETRY_LADDER_MS[0]);
    });

    it("starts a stopped connection rather than doing nothing", () => {
      const { conn, sources } = harness();
      conn.reconnect();
      expect(sources).toHaveLength(1);
    });
  });

  /*
   * Timers belong to the source that armed them.
   *
   * Both failures below share a shape: a timer outlives the stream it was
   * scheduled for and then acts on its replacement. Nothing in the class
   * crashes when that happens — the dashboard simply goes offline while the
   * data is arriving, which is exactly the lie this connection exists to
   * prevent, so it has to be asserted rather than watched for.
   */
  describe("timers scoped to their own source", () => {
    it("cancels the old watchdog when a reconnect replaces the source", () => {
      const { conn, sources, pending } = harness();
      conn.start();
      sources[0].open();
      expect(pending("watchdog")).toHaveLength(1);

      // Reconnect while the replacement is still connecting — the case the
      // old code got wrong. A new source that has not opened yet arms no
      // watchdog of its own, so nothing comes along to overwrite the handle:
      // the first source's watchdog survives, and its deadline is counted
      // from a stream that no longer exists.
      conn.reconnect();
      expect(sources).toHaveLength(2);
      expect(sources[1].readyState).toBe(0);

      // No timer may be left over from the socket that was just discarded.
      // Firing one would declare a connection that is still being negotiated
      // dead, and schedule a retry that closes it mid-handshake.
      expect(pending("watchdog")).toHaveLength(0);
    });

    it("drops a pending retry once a frame proves the stream recovered", () => {
      const { conn, sources, pending, runWatchdog } = harness();
      conn.start();
      sources[0].open();

      // The stream falls silent and the watchdog gives up, arming a retry
      // that will replace the socket when it fires.
      runWatchdog();
      expect(conn.getStatus()).toBe("offline");
      expect(pending("retry")).toHaveLength(1);

      // Then a frame arrives on that very socket, before the retry fires.
      // This is the race the fix is about: the stream was slow, not dead.
      sources[0].send("ping", "{}");

      expect(conn.getStatus()).toBe("live");
      // Left armed, the retry would close a source that is working — the
      // dashboard would drop out moments after recovering, for no reason.
      expect(pending("retry")).toHaveLength(0);
      expect(sources[0].closed).toBe(false);
      expect(sources).toHaveLength(1);
    });

    it("resets the ladder when a frame proves the stream recovered", () => {
      const { conn, sources, runTimer } = harness();
      conn.start();
      sources[0].open();

      // Climb a rung, then recover by frame rather than by onopen — a socket
      // can deliver before it fires open, and a stream that has come back
      // must not inherit the patience the outage earned.
      sources[0].fail();
      runTimer();
      sources[1].send("ping", "{}");

      sources[1].fail();
      expect(runTimer()).toBe(RETRY_LADDER_MS[0]);
    });

    it("ignores a replaced source that errors on its way out", () => {
      const { conn, sources, pending } = harness();
      conn.start();
      sources[0].open();

      conn.reconnect();
      expect(sources).toHaveLength(2);

      // The abandoned socket fires a last error after being replaced. Acting
      // on it would schedule a reopen against the source that is now live.
      sources[0].fail();

      expect(pending("retry")).toHaveLength(0);
      expect(conn.getStatus()).not.toBe("offline");
      expect(sources).toHaveLength(2);
    });
  });
});
