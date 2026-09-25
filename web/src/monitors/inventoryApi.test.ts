import { afterEach, describe, expect, it, vi } from "vitest";
import {
  checkMonitorNow,
  fetchInventory,
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

  it("refuses a 200 with no monitor list rather than reading it as empty", async () => {
    // "Nothing is being watched yet" to somebody with forty monitors is the
    // most alarming wrong thing this page can say, so it is never said on the
    // strength of a body we could not read.
    vi.spyOn(globalThis, "fetch").mockResolvedValue(json({ ok: true }));
    await expect(fetchInventory()).rejects.toThrow(/no monitor list/);
  });

  it("reports a failed list rather than rendering an empty instance", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json({ error: "nope" }, { status: 500 }),
    );
    await expect(fetchInventory()).rejects.toThrow("HTTP 500");
  });
});

describe("inventory channel attachments", () => {
  const monitor = (channels: unknown) => ({
    id: 1, name: "site", type: "http", target: "https://example.com",
    interval_s: 60, timeout_s: 10, enabled: true, status: "up",
    created_at: "2026-09-01T00:00:00Z", channels,
  });

  it("reads every attachment past forty monitors in one request", async () => {
    const monitors = Array.from({ length: 45 }, (_, i) => ({
      ...monitor([{ id: i + 1, name: `channel ${i}` }]), id: i + 1,
    }));
    const request = vi.spyOn(globalThis, "fetch").mockResolvedValue(json({ monitors }));
    const result = await fetchInventory();
    expect(result).toHaveLength(45);
    for (const [i, row] of result.entries()) {
      expect(row.channels).toEqual({ known: true, names: [`channel ${i}`], ids: [String(i + 1)] });
    }
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("preserves a known empty set", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(json({ monitors: [monitor([])] }));
    expect((await fetchInventory())[0].channels).toEqual({ known: true, names: [], ids: [] });
  });

  it.each([undefined, null, {}, "bad", [null], [{}], [{ id: 1 }], [{ id: 1, name: 2 }], [{ id: 1, name: "" }]])(
    "keeps malformed or unavailable channels unknown: %j", async (channels) => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(json({ monitors: [monitor(channels)] }));
      expect((await fetchInventory())[0].channels).toEqual({ known: false });
    },
  );
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

  it.each([undefined, null, "", "*", "junk", 'W/""'])("refuses an edit without a usable version %s before fetching", async (version) => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(json({}));
    await expect(patchMonitor("7", { name: "new" }, version)).rejects.toThrow(/version.*reload/i);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
