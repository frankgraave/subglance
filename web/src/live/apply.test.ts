import { describe, expect, it } from "vitest";
import { MAX_LIVE_BEATS, applyHeartbeat, applyStatus, statusAfterHeartbeat } from "./apply";
import type { HeartbeatEvent, StatusEvent } from "./events";
import { parseEvent } from "./events";
import type { Monitor } from "../monitors/types";

const monitor = (over: Partial<Monitor> = {}): Monitor => ({
  id: "1",
  name: "api",
  status: "up",
  tags: {},
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
    expect(m.status).toBe("warning");
    expect(m.error).toBe("timeout");
  });

  it("replaces the failure kind with the error it classifies", () => {
    // The kind belongs to one error: a later failure without a kind must not
    // keep the earlier failure's words, and a pass clears both.
    const down = monitor({ status: "down", error: "500", failureKind: "status" });
    const [timedOut] = applyHeartbeat([down], beat({ ok: false, error: "deadline", failureKind: "timeout" }));
    expect(timedOut).toMatchObject({ error: "deadline", failureKind: "timeout" });
    const [unclassed] = applyHeartbeat([timedOut], beat({ ok: false, error: "odd" }));
    expect(unclassed.failureKind).toBeUndefined();
    const [passed] = applyHeartbeat([timedOut], beat({ ok: true }));
    expect(passed.failureKind).toBeUndefined();
  });

  it("keeps a confirmed outage red until the incident resolves", () => {
    // One passing check is not a recovery: the server waits for the recovery
    // threshold and then sends `incident_resolved`. Going green on the pass
    // would show an all-clear the server has not given.
    const down = monitor({ status: "down" });
    expect(applyHeartbeat([down], beat({ ok: false }))[0].status).toBe("down");
    const [passed] = applyHeartbeat([down], beat({ ok: true }));
    expect(passed.status).toBe("down");
    expect(applyStatus([passed], status({ event: "incident_resolved" }))[0].status).toBe("up");
  });

  it("keeps a monitor expiring, with its notice text, on a pass the server marks expiring", () => {
    // The pass is assessed up, because it is not downtime; `expiring` is
    // what keeps it from reading as a plain up.
    const notice = monitor({ status: "expiring", error: "certificate expires in 6 days", failureKind: "cert_expiry" });
    const [m] = applyHeartbeat([notice], beat({ ok: true, assessment: "up", expiring: true }));
    expect(m).toMatchObject({ status: "expiring", error: "certificate expires in 6 days", failureKind: "cert_expiry" });
    // A failure against it is judged like any other: a warning first.
    const [failed] = applyHeartbeat([m], beat({ ok: false, assessment: "warning", error: "timeout" }));
    expect(failed.status).toBe("warning");
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

  it("takes the incident's cause with its error, and only with it", () => {
    const [confirmed] = applyStatus([monitor()], status({ error: "refused", cause: "connection" }));
    expect(confirmed).toMatchObject({ error: "refused", failureKind: "connection" });
    // A frame with no error keeps the error it had, and so its kind.
    const kept = monitor({ status: "warning", error: "500", failureKind: "status" });
    expect(applyStatus([kept], status({ cause: "timeout" }))[0]).toMatchObject({ error: "500", failureKind: "status" });
    const [resolved] = applyStatus([confirmed], status({ event: "incident_resolved" }));
    expect(resolved.failureKind).toBeUndefined();
  });

  it("clears the error on recovery", () => {
    const down = monitor({ status: "down", error: "500" });
    const [m] = applyStatus([down], status({ event: "incident_resolved" }));
    expect(m.status).toBe("up");
    expect(m.error).toBeUndefined();
  });

  it("confirms a certificate notice as expiring, not down, and resolves it to up", () => {
    const [expiring] = applyStatus([monitor()], status({ notice: true, error: "certificate expires in 6 days" }));
    expect(expiring.status).toBe("expiring");
    expect(applyStatus([expiring], status({ event: "incident_resolved", notice: true }))[0].status).toBe("up");
  });

  it("treats an opened but unconfirmed incident as warning", () => {
    expect(applyStatus([monitor()], status({ event: "incident_opened" }))[0].status).toBe("warning");
  });

  it("ignores an event it does not know", () => {
    const list = [monitor()];
    expect(applyStatus(list, status({ event: "flap_started" }))).toBe(list);
  });

  it("never un-pauses a monitor", () => {
    expect(applyStatus([monitor({ status: "paused" })], status())[0].status).toBe("paused");
  });
});


it("updates current maintenance at both boundaries without rewriting history", () => {
  let list = [monitor({ maintenance: false })];
  list = applyHeartbeat(list, beat({maintenance: true, currentMaintenance: true}));
  expect(list[0].maintenance).toBe(true);
  list = applyHeartbeat(list, beat({at: 3_000, maintenance: false, currentMaintenance: false}));
  expect(list[0].maintenance).toBe(false);
  expect(list[0].beats.map((b) => b.maintenance)).toEqual([true, false]);
});

it("keeps current maintenance separate from the sample timestamp", () => {
 const event = parseEvent("heartbeat", JSON.stringify({monitor_id:1, data:{ok:false, maintenance:true, current_maintenance:false}}));
 expect(event?.kind).toBe("heartbeat");
 if(event?.kind !== "heartbeat") throw new Error("missing heartbeat");
 const [m] = applyHeartbeat([monitor({maintenance:true})], event);
 expect(m.maintenance).toBe(false);
 expect(m.beats[0].maintenance).toBe(true);
});
