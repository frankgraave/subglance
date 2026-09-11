import { describe, expect, it } from "vitest";
import { LiveConnection, RETRY_LADDER_MS } from "./connection";
import type { EventSourceLike, RawFrame } from "./connection";

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

/** A manual clock, so backoff is asserted rather than waited for. */
function harness() {
  const sources: FakeSource[] = [];
  const timers: { fn: () => void; ms: number }[] = [];
  const conn = new LiveConnection({
    url: "/api/v1/stream",
    create: () => {
      const s = new FakeSource();
      sources.push(s);
      return s;
    },
    setTimer: (fn, ms) => timers.push({ fn, ms }),
    clearTimer: () => {},
  });
  const runTimer = () => {
    const t = timers.shift();
    if (!t) throw new Error("no retry was scheduled");
    t.fn();
    return t.ms;
  };
  return { conn, sources, timers, runTimer };
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
    const { conn, sources, timers } = harness();
    conn.start();
    sources[0].open();
    sources[0].blip();
    expect(conn.getStatus()).toBe("offline");
    // readyState CONNECTING means EventSource is reconnecting itself; a second
    // socket here would double every event for as long as both survive.
    expect(timers).toHaveLength(0);
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
    const { conn, sources, timers } = harness();
    conn.start();
    sources[0].open();
    conn.stop();
    sources[0].fail();
    expect(timers).toHaveLength(0);
  });
});
