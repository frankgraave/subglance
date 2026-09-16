import { afterEach, describe, expect, it, vi } from "vitest";
import {
  CHANNEL_FANOUT_LIMIT,
  checkMonitorNow,
  fetchInventory,
  fetchMonitorChannels,
  patchMonitor,
  setMonitorPaused,
} from "./inventoryApi";

function json(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
    ...init,
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("fetchInventory", () => {
  it("does not ask for heartbeats, which this screen never draws", () => {
    // Asking for a hundred checks per monitor to render columns that do not
    // use them would make the management page the most expensive request in
    // the app.
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(json({ monitors: [] }));
    void fetchInventory();
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/monitors");
  });

  it("reports a failed list rather than rendering an empty instance", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json({ error: "nope" }, { status: 500 }),
    );
    await expect(fetchInventory()).rejects.toThrow("HTTP 500");
  });
});

describe("fetchMonitorChannels", () => {
  it("marks a failed request as unknown, never as 'no channels'", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(((input: string) =>
      Promise.resolve(
        input.includes("/2/")
          ? json({ error: "boom" }, { status: 500 })
          : json({ channels: [{ name: "slack" }] }),
      )) as typeof fetch);

    const result = await fetchMonitorChannels(["1", "2"]);
    expect(result.byMonitor["1"]).toEqual({ known: true, names: ["slack"] });
    // The distinction this whole module exists for: an empty list is the
    // finding "nobody hears about this monitor", and a 500 must not be able
    // to manufacture it.
    expect(result.byMonitor["2"]).toEqual({ known: false });
    expect(result.truncated).toBe(true);
  });

  it("reports a genuinely empty attachment as known-and-empty", async () => {
    // A fresh Response per call, not one shared object: a body can only be
    // read once, so a reused Response makes the second request throw — which
    // would set `truncated` and let a test pass for the wrong reason.
    vi.spyOn(globalThis, "fetch").mockImplementation((() =>
      Promise.resolve(json({ channels: [] }))) as typeof fetch);
    const result = await fetchMonitorChannels(["1"]);
    expect(result.byMonitor["1"]).toEqual({ known: true, names: [] });
    expect(result.truncated).toBe(false);
  });

  it("stops at the fan-out cap and says that it stopped", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation((() =>
      Promise.resolve(json({ channels: [] }))) as typeof fetch);
    const ids = Array.from({ length: CHANNEL_FANOUT_LIMIT + 3 }, (_, i) =>
      String(i),
    );
    const result = await fetchMonitorChannels(ids);
    expect(Object.keys(result.byMonitor)).toHaveLength(CHANNEL_FANOUT_LIMIT);
    // Every request it did make succeeded, so the only reason to be short is
    // the cap — which is exactly the claim under test.
    expect(result.truncated).toBe(true);
  });
});

describe("setMonitorPaused", () => {
  it("uses the pause and resume verbs the server offers", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation((() => Promise.resolve(json({}))) as typeof fetch);
    await setMonitorPaused("7", true);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/monitors/7/pause");
    await setMonitorPaused("7", false);
    expect(fetchMock.mock.calls[1][0]).toBe("/api/v1/monitors/7/resume");
  });
});

describe("checkMonitorNow", () => {
  it("reads 'recorded' off the wire and never assumes it", async () => {
    // A paused monitor's check is deliberately not recorded. Defaulting to
    // true would put a phantom measurement into someone's uptime.
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json({ ok: true, latency_ms: 12 }),
    );
    const result = await checkMonitorNow("7");
    expect(result.recorded).toBe(false);
    expect(result.ok).toBe(true);
    expect(result.latencyMs).toBe(12);
  });

  it("passes the server's recorded flag through when it is there", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json({ ok: false, latency_ms: 4, error: "connection refused", recorded: true }),
    );
    const result = await checkMonitorNow("7");
    expect(result.recorded).toBe(true);
    expect(result.error).toBe("connection refused");
  });
});

describe("patchMonitor", () => {
  it("sends If-Match when it has a version, so a concurrent edit is refused", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(json({}));
    await patchMonitor("7", { name: "new" }, 'W/"123"');
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect((init.headers as Record<string, string>)["If-Match"]).toBe('W/"123"');
  });

  it("omits If-Match when there is no version rather than sending an empty one", async () => {
    // An empty If-Match is a 400 from the server, which would turn "we could
    // not read the ETag" into "your edit is malformed".
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(json({}));
    await patchMonitor("7", { name: "new" }, null);
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect((init.headers as Record<string, string>)["If-Match"]).toBeUndefined();
  });
});
