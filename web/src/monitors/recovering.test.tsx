// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { applyHeartbeat, applyStatus } from "../live/apply";
import type { HeartbeatEvent, StatusEvent } from "../live/events";
import { parseEvent } from "../live/events";
import { statusWord } from "./format";
import { Led } from "./Led";
import { describeTransitions, partition, summarise } from "./model";
import { fromApi, type ApiMonitor, type Monitor } from "./types";

afterEach(cleanup);

/*
 * SUB-157: a confirmed incident closes only after `recovery_threshold` passes
 * in a row. In between, the server reports the monitor as `recovering` with a
 * streak, and the UI says "Recovering (1 of 2)" in the warning style: the
 * outage is still open and nobody has been told it is over, so it is neither
 * green nor a plain red.
 */

const api = (over: Partial<ApiMonitor> = {}): ApiMonitor => ({
  id: "m1",
  name: "api",
  type: "http",
  target: "https://api.example.com/healthz",
  interval_s: 60,
  timeout_s: 10,
  enabled: true,
  status: "recovering",
  recovery: { passes: 1, threshold: 2 },
  last_check: "2026-09-11T12:00:00Z",
  latency_ms: 128,
  uptime_24h: 99.94,
  created_at: "2026-08-01T00:00:00Z",
  ...over,
});

const monitor = (over: Partial<Monitor> = {}): Monitor => ({
  id: "1",
  name: "api",
  status: "down",
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
  assessment: "up",
  latencyMs: 51,
  ...over,
});

const status = (event: string): StatusEvent => ({
  kind: "status",
  monitorId: "1",
  at: 3_000,
  event,
});

describe("recovering on the wire", () => {
  it("keeps the status and the streak from the list read", () => {
    const m = fromApi(api());
    expect(m.status).toBe("recovering");
    expect(m.recovery).toEqual({ passes: 1, threshold: 2 });
  });

  it("drops a malformed streak rather than rendering it", () => {
    expect(fromApi(api({ recovery: { passes: "1", threshold: 2 } })).recovery).toBeUndefined();
    expect(fromApi(api({ recovery: { passes: 0, threshold: 2 } })).recovery).toBeUndefined();
    expect(fromApi(api({ recovery: undefined })).status).toBe("recovering");
  });

  it("ignores a streak on any other status", () => {
    expect(fromApi(api({ status: "down" })).recovery).toBeUndefined();
    expect(fromApi(api({ enabled: false })).recovery).toBeUndefined();
  });

  it("reads the streak from a heartbeat frame", () => {
    const e = parseEvent(
      "heartbeat",
      JSON.stringify({
        monitor_id: 1,
        at: "2026-09-11T08:30:00Z",
        data: { ok: true, assessment: "up", latency_ms: 42, recovery: { passes: 1, threshold: 3 } },
      }),
    );
    expect(e).toMatchObject({ kind: "heartbeat", recovery: { passes: 1, threshold: 3 } });
  });
});

describe("recovering in the live view", () => {
  it("turns a red row amber on a pass that carries the streak, not green", () => {
    const [m] = applyHeartbeat([monitor()], beat({ recovery: { passes: 1, threshold: 2 } }));
    expect(m.status).toBe("recovering");
    expect(m.recovery).toEqual({ passes: 1, threshold: 2 });
  });

  it("does not read an up assessment on an open outage as the all-clear", () => {
    // An older server, or a frame whose streak was dropped: the pass is
    // assessed up because it is not downtime, not because the incident closed.
    expect(applyHeartbeat([monitor()], beat())[0].status).toBe("down");
    const recovering = monitor({ status: "recovering", recovery: { passes: 1, threshold: 3 } });
    const [m] = applyHeartbeat([recovering], beat());
    expect(m.status).toBe("recovering");
    expect(m.recovery).toEqual({ passes: 1, threshold: 3 });
  });

  it("drops back to down, without the streak, on a failure", () => {
    const recovering = monitor({ status: "recovering", recovery: { passes: 1, threshold: 2 } });
    const [m] = applyHeartbeat([recovering], beat({ ok: false, assessment: "down" }));
    expect(m.status).toBe("down");
    expect(m.recovery).toBeUndefined();
  });

  it("goes green only on incident_resolved, and forgets the streak", () => {
    const recovering = monitor({ status: "recovering", recovery: { passes: 1, threshold: 2 } });
    const [m] = applyStatus([recovering], status("incident_resolved"));
    expect(m.status).toBe("up");
    expect(m.recovery).toBeUndefined();
  });
});

describe("recovering in words", () => {
  it("says how far the streak has got", () => {
    expect(statusWord("recovering", false, { passes: 1, threshold: 2 })).toBe("Recovering (1 of 2)");
    expect(statusWord("recovering", true, { passes: 1, threshold: 2 })).toBe("Was recovering (1 of 2)");
    expect(statusWord("recovering")).toBe("Recovering");
  });

  it("never attaches a streak to another status", () => {
    expect(statusWord("down", false, { passes: 1, threshold: 2 })).toBe("Down");
  });

  it("lights the amber lamp and names the streak", () => {
    const { container } = render(
      <Led status="recovering" hideLabel={false} recovery={{ passes: 1, threshold: 2 }} />,
    );
    expect(container.querySelector(".led")?.getAttribute("data-state")).toBe("warn");
    expect(screen.getByText("Recovering (1 of 2)")).toBeTruthy();
  });

  it("keeps a recovering monitor in the attention section", () => {
    const { attention } = partition([monitor({ id: "a", status: "recovering" }), monitor({ id: "b", status: "up" })]);
    expect(attention.map((m) => m.id)).toEqual(["a"]);
    expect(summarise([monitor({ status: "recovering" })]).recovering).toBe(1);
  });

  it("does not announce a recovering monitor as an all-clear", () => {
    const before = [monitor({ id: "a", status: "down" }), monitor({ id: "b", status: "up" })];
    const after = [monitor({ id: "a", status: "recovering" }), monitor({ id: "b", status: "up" })];
    const said = describeTransitions(before, after);
    expect(said).toBe("No monitors down. 1 recovering, 1 up.");
  });
});
