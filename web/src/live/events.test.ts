import { describe, expect, it } from "vitest";
import { monitorKey, parseEvent } from "./events";

const frame = (body: unknown) => JSON.stringify(body);

describe("parseEvent", () => {
  it("reads a heartbeat frame", () => {
    const e = parseEvent(
      "heartbeat",
      frame({
        kind: "heartbeat",
        monitor_id: 7,
        at: "2026-09-11T08:30:00Z",
        seq: 12,
        data: { ok: true, latency_ms: 42, status_code: 200 },
      }),
    );
    expect(e).toEqual({
      kind: "heartbeat",
      monitorId: "7",
      at: Date.parse("2026-09-11T08:30:00Z"),
      ok: true,
      latencyMs: 42,
      statusCode: 200,
      error: undefined,
    });
  });

  it("keeps a missing latency null rather than zero", () => {
    const e = parseEvent(
      "heartbeat",
      frame({ monitor_id: 1, at: "2026-09-11T08:30:00Z", data: { ok: false, error: "timeout" } }),
    );
    // A failed check has no timing. Rendering that as 0 ms would claim the
    // probe answered instantly, which is the opposite of what happened.
    expect(e).toMatchObject({ latencyMs: null, ok: false, error: "timeout" });
  });

  it("drops a frame with no monitor id instead of inventing one", () => {
    expect(parseEvent("heartbeat", frame({ at: "2026-09-11T08:30:00Z", data: { ok: true } }))).toBeNull();
  });

  it("survives malformed JSON and unknown event types", () => {
    // An exception thrown inside an EventSource listener takes the whole
    // subscription down, so a bad frame must be a null, never a throw.
    expect(parseEvent("heartbeat", "{not json")).toBeNull();
    expect(parseEvent("something_new", frame({ monitor_id: 1 }))).toBeNull();
  });

  it("reads hello, including the gap flag", () => {
    expect(parseEvent("hello", frame({ seq: 40, gap: true, missed: 9 }))).toEqual({
      kind: "hello",
      pingIntervalMs: null,
      seq: 40,
      gap: true,
      missed: 9,
    });
  });

  it("reads lagged", () => {
    expect(parseEvent("lagged", frame({ dropped: 3 }))).toEqual({ kind: "lagged", dropped: 3 });
  });

  it("reads a status frame", () => {
    expect(
      parseEvent(
        "status",
        frame({
          monitor_id: "9",
          at: "2026-09-11T08:31:00Z",
          data: { event: "incident_confirmed", error: "500" },
        }),
      ),
    ).toMatchObject({ kind: "status", monitorId: "9", event: "incident_confirmed", error: "500" });
  });
});

describe("monitorKey", () => {
  it("makes a number and a string id the same key", () => {
    // The list endpoint sends a JSON number, the stream the same id; matching
    // them without normalising fails silently on every heartbeat.
    expect(monitorKey(7)).toBe(monitorKey("7"));
  });

  it("maps absent ids to the empty string", () => {
    expect(monitorKey(null)).toBe("");
    expect(monitorKey(undefined)).toBe("");
  });

  // The client sizes its silence watchdog from what the server promises, so a
  // server that states its keepalive must be believed over any constant here.
  it("reads the keepalive interval hello announces", () => {
    const event = parseEvent("hello", frame({ seq: 1, gap: false, ping_interval_ms: 20000 }));
    expect(event).toMatchObject({ kind: "hello", pingIntervalMs: 20000 });
  });

  // A server that predates the field, or a proxy that rewrote the frame. The
  // client falls back to its default rather than arming a watchdog on NaN.
  it("reports a missing keepalive interval as null", () => {
    const event = parseEvent("hello", frame({ seq: 1, gap: false }));
    expect(event).toMatchObject({ pingIntervalMs: null });
  });

  // Its arrival is the whole payload: it is what tells a silent-but-open
  // socket apart from a dead one.
  it("reads a ping as a frame with no state", () => {
    expect(parseEvent("ping", frame({ server_time: "2026-09-11T08:00:00Z" }))).toEqual({
      kind: "ping",
    });
  });
});
