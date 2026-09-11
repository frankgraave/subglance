import { describe, expect, it } from "vitest";
import { MAX_LIVE_BEATS, applyHeartbeat, applyStatus, statusAfterHeartbeat } from "./apply";
import type { HeartbeatEvent, StatusEvent } from "./events";
import type { Monitor } from "../monitors/types";

const monitor = (over: Partial<Monitor> = {}): Monitor => ({
  id: "1",
  name: "api",
  status: "up",
  target: "https://api.example.com",
  latencyMs: 30,
  uptime24h: 99.5,
  beats: [],
  lastCheck: 1_000,
  ...over,
});

const beat = (over: Partial<HeartbeatEvent> = {}): HeartbeatEvent => ({
  kind: "heartbeat",
  monitorId: "1",
  at: 2_000,
  ok: true,
  latencyMs: 51,
  ...over,
});

const status = (over: Partial<StatusEvent> = {}): StatusEvent => ({
  kind: "status",
  monitorId: "1",
  at: 2_000,
  event: "incident_confirmed",
  ...over,
});

describe("applyHeartbeat", () => {
  it("appends the beat and refreshes latency and last check", () => {
    const [m] = applyHeartbeat([monitor()], beat());
    expect(m.latencyMs).toBe(51);
    expect(m.lastCheck).toBe(2_000);
    expect(m.beats).toHaveLength(1);
    expect(m.beats[0]).toMatchObject({ ts: 2_000, ok: true });
  });

  it("does not turn a green monitor red on one failed check", () => {
    // The server calls an outage only once the failure threshold is crossed.
    // A client that goes red sooner disagrees with the alert that never fired.
    const [m] = applyHeartbeat([monitor()], beat({ ok: false, latencyMs: null, error: "timeout" }));
    expect(m.status).toBe("pending");
    expect(m.error).toBe("timeout");
  });

  it("keeps a confirmed outage red until a check succeeds", () => {
    const down = monitor({ status: "down" });
    expect(applyHeartbeat([down], beat({ ok: false }))[0].status).toBe("down");
    expect(applyHeartbeat([down], beat({ ok: true }))[0].status).toBe("up");
  });

  it("leaves a paused monitor paused", () => {
    expect(applyHeartbeat([monitor({ status: "paused" })], beat())[0].status).toBe("paused");
    expect(statusAfterHeartbeat("paused", false)).toBe("paused");
  });

  it("caps the beat history so a long-lived tab cannot grow forever", () => {
    const beats = Array.from({ length: MAX_LIVE_BEATS }, (_, i) => ({
      ts: i,
      ok: true,
      latencyMs: 1,
    }));
    const [m] = applyHeartbeat([monitor({ beats })], beat({ at: 9_999 }));
    expect(m.beats).toHaveLength(MAX_LIVE_BEATS);
    expect(m.beats.at(-1)?.ts).toBe(9_999);
    expect(m.beats[0].ts).toBe(1);
  });

  it("returns the same array for an unknown monitor", () => {
    // Identity matters: a new array would re-render every row for an event
    // about a monitor this view does not even show.
    const list = [monitor()];
    expect(applyHeartbeat(list, beat({ monitorId: "404" }))).toBe(list);
  });
});

describe("applyStatus", () => {
  it("confirms an incident as down", () => {
    expect(applyStatus([monitor()], status({ error: "500" }))[0]).toMatchObject({
      status: "down",
      error: "500",
    });
  });

  it("clears the error on recovery", () => {
    const down = monitor({ status: "down", error: "500" });
    const [m] = applyStatus([down], status({ event: "incident_resolved" }));
    expect(m.status).toBe("up");
    expect(m.error).toBeUndefined();
  });

  it("treats an opened but unconfirmed incident as pending", () => {
    expect(applyStatus([monitor()], status({ event: "incident_opened" }))[0].status).toBe("pending");
  });

  it("ignores an event it does not know", () => {
    const list = [monitor()];
    expect(applyStatus(list, status({ event: "flap_started" }))).toBe(list);
  });

  it("never un-pauses a monitor", () => {
    expect(applyStatus([monitor({ status: "paused" })], status())[0].status).toBe("paused");
  });
});
