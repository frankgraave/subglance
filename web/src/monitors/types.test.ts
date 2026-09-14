import { describe, expect, it } from "vitest";
import { fromApi, monitorsFromApi, toUnixMs, type ApiMonitor } from "./types";

/** A healthy API monitor; each test overrides only the field it is about. */
const api = (over: Partial<ApiMonitor> = {}): ApiMonitor => ({
  id: "m1",
  name: "api",
  type: "http",
  target: "https://api.example.com/healthz",
  interval_s: 60,
  timeout_s: 10,
  enabled: true,
  status: "up",
  last_check: "2026-09-11T12:00:00Z",
  latency_ms: 128,
  uptime_24h: 99.94,
  created_at: "2026-08-01T00:00:00Z",
  ...over,
});

describe("toUnixMs", () => {
  it("parses RFC3339 into unix milliseconds", () => {
    expect(toUnixMs("2026-09-11T12:00:00Z")).toBe(Date.UTC(2026, 8, 11, 12, 0, 0));
  });

  it("honours the offset in a non-UTC RFC3339 timestamp", () => {
    expect(toUnixMs("2026-09-11T14:00:00+02:00")).toBe(Date.UTC(2026, 8, 11, 12, 0, 0));
  });

  it("returns null rather than NaN for missing or unparseable input", () => {
    expect(toUnixMs(null)).toBeNull();
    expect(toUnixMs(undefined)).toBeNull();
    expect(toUnixMs("")).toBeNull();
    expect(toUnixMs("not a date")).toBeNull();
  });
});

describe("fromApi", () => {
  it("maps the wire format onto the render model", () => {
    const monitor = fromApi(api());
    expect(monitor).toMatchObject({
      id: "m1",
      name: "api",
      status: "up",
      target: "https://api.example.com/healthz",
      latencyMs: 128,
      uptime24h: 99.94,
      lastCheck: Date.UTC(2026, 8, 11, 12, 0, 0),
    });
  });

  it("reports a disabled monitor as paused whatever its last check said", () => {
    expect(fromApi(api({ enabled: false, status: "up" })).status).toBe("paused");
    expect(fromApi(api({ enabled: false, status: "down" })).status).toBe("paused");
  });

  it("keeps status as-is while the monitor is enabled", () => {
    expect(fromApi(api({ status: "down" })).status).toBe("down");
    expect(fromApi(api({ status: "pending" })).status).toBe("pending");
  });

  it("turns absent latency and uptime into null, never 0", () => {
    const monitor = fromApi(api({ latency_ms: undefined, uptime_24h: undefined }));
    expect(monitor.latencyMs).toBeNull();
    expect(monitor.uptime24h).toBeNull();
  });

  it("turns explicit null latency and uptime into null", () => {
    const monitor = fromApi(api({ latency_ms: null, uptime_24h: null }));
    expect(monitor.latencyMs).toBeNull();
    expect(monitor.uptime24h).toBeNull();
  });

  it("preserves a real zero, because 0% uptime is a fact", () => {
    const monitor = fromApi(api({ uptime_24h: 0, latency_ms: 0 }));
    expect(monitor.uptime24h).toBe(0);
    expect(monitor.latencyMs).toBe(0);
  });

  it("returns null lastCheck for a monitor that has never run", () => {
    expect(fromApi(api({ last_check: null })).lastCheck).toBeNull();
    expect(fromApi(api({ last_check: undefined })).lastCheck).toBeNull();
  });

  it("converts heartbeats to unix ms and keeps them oldest first", () => {
    const monitor = fromApi(
      api({
        heartbeats: [
          { ts: "2026-09-11T11:58:00Z", ok: true, latency_ms: 100 },
          { ts: "2026-09-11T11:59:00Z", ok: false, latency_ms: null, error: "timeout" },
          { ts: "2026-09-11T12:00:00Z", ok: true, latency_ms: 120, status_code: 200 },
        ],
      }),
    );
    expect(monitor.beats.map((b) => b.ts)).toEqual([
      Date.UTC(2026, 8, 11, 11, 58, 0),
      Date.UTC(2026, 8, 11, 11, 59, 0),
      Date.UTC(2026, 8, 11, 12, 0, 0),
    ]);
    // A failed check has no timing; that must stay null, not become 0.
    expect(monitor.beats[1]).toMatchObject({ ok: false, latencyMs: null, error: "timeout" });
    expect(monitor.beats[2]).toMatchObject({ ok: true, latencyMs: 120, statusCode: 200 });
  });

  it("treats an omitted heartbeats field as no history", () => {
    expect(fromApi(api()).beats).toEqual([]);
  });

  it("carries the failure reason through", () => {
    expect(fromApi(api({ status: "down", error: "502 Bad Gateway" })).error).toBe("502 Bad Gateway");
  });
});

describe("fromApi tags", () => {
  it("carries tags through unchanged", () => {
    const m = fromApi(api({ tags: { env: "prod", customer: "Acme" } }));
    expect(m.tags).toEqual({ env: "prod", customer: "Acme" });
  });

  it("turns an absent tag object into an empty one, so consumers need no guard", () => {
    expect(fromApi(api()).tags).toEqual({});
  });

  it("drops values that are not non-empty strings rather than rendering them", () => {
    const hostile = { env: "prod", broken: 7, blank: "" } as unknown as Record<string, string>;
    expect(fromApi(api({ tags: hostile })).tags).toEqual({ env: "prod" });
  });
});

describe("monitorsFromApi", () => {
  it("maps a whole payload", () => {
    const list = monitorsFromApi({ monitors: [api(), api({ id: "m2", name: "db" })] });
    expect(list.map((m) => m.id)).toEqual(["m1", "m2"]);
  });

  it("survives an empty or missing payload instead of throwing", () => {
    expect(monitorsFromApi({ monitors: [] })).toEqual([]);
    expect(monitorsFromApi({})).toEqual([]);
    expect(monitorsFromApi(null)).toEqual([]);
  });
});

describe("push monitors", () => {
  const push = (over: Partial<ApiMonitor> = {}): ApiMonitor =>
    api({
      type: "push",
      target: "",
      status: "pending",
      last_check: null,
      latency_ms: null,
      push_interval_s: 3600,
      push_grace_s: 300,
      push_token_prefix: "sgu_abcd",
      ...over,
    });

  it("carries the window, so the detail view need not refetch it", () => {
    expect(fromApi(push()).push).toEqual({
      intervalS: 3600,
      graceS: 300,
      tokenPrefix: "sgu_abcd",
    });
  });

  it("leaves push unset for a probed monitor", () => {
    expect(fromApi(api()).push).toBeUndefined();
  });

  it("renders a never-pinged push monitor as waiting, not pending", () => {
    // The API has one word for "no result yet" and it means two things. For a
    // probed monitor a check is coming; for a push monitor nothing is coming
    // until somebody wires up the URL, and amber "pending" reads as a monitor
    // that is failing to start.
    expect(fromApi(push()).status).toBe("waiting");
  });

  it("leaves a push monitor that has reported alone", () => {
    expect(fromApi(push({ status: "up", last_check: "2026-09-11T12:00:00Z" })).status).toBe("up");
  });

  it("does not turn a pending probed monitor into a waiting one", () => {
    // The guard has to key on the push window, not on the missing timestamp:
    // a brand-new HTTP monitor also has no last_check.
    expect(fromApi(api({ status: "pending", last_check: null })).status).toBe("pending");
  });

  it("keeps paused ahead of waiting, because a person decided it", () => {
    expect(fromApi(push({ enabled: false })).status).toBe("paused");
  });
});
