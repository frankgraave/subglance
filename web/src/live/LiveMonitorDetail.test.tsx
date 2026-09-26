// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorDetailRoot } from "./LiveMonitorDetail";
import type { EventSourceLike } from "./connection";

/**
 * SUB-63 end to end in jsdom: opening one monitor shows what is wrong with it,
 * and — the part that is easy to get wrong — it keeps updating from the same
 * stream the dashboard uses instead of freezing on a snapshot.
 */

class FakeSource implements EventSourceLike {
  static last: FakeSource | null = null;
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  readyState = 0;
  private readonly listeners = new Map<string, (event: MessageEvent) => void>();

  constructor() {
    FakeSource.last = this;
  }

  addEventListener(
    type: string,
    listener: (event: MessageEvent) => void,
  ): void {
    this.listeners.set(type, listener);
  }

  close(): void {
    this.readyState = 2;
  }

  open(): void {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }

  fail(): void {
    this.readyState = 2;
    this.onerror?.(new Event("error"));
  }

  send(type: string, body: unknown): void {
    this.listeners.get(type)?.({
      data: JSON.stringify(body),
      lastEventId: "",
    } as MessageEvent);
  }
}

const apiMonitor = (over: Record<string, unknown> = {}) => ({
  id: 1,
  name: "api",
  type: "http",
  target: "https://api.example.com",
  interval_s: 60,
  timeout_s: 10,
  enabled: true,
  status: "up",
  last_check: "2026-09-11T08:00:00Z",
  latency_ms: 30,
  uptime_24h: 99.9,
  created_at: "2026-09-01T00:00:00Z",
  heartbeats: [{ ts: "2026-09-11T08:00:00Z", ok: true, latency_ms: 30 }],
  ...over,
});

const LATENCY = {
  monitor_id: 1,
  window: "24h",
  step_s: 900,
  from: "2026-09-19T12:00:00Z",
  to: "2026-09-20T12:00:00Z",
  points: [
    { t: "2026-09-20T11:30:00Z", checks: 15, samples: 15, down: 0, avg_ms: 42, min_ms: 30, max_ms: 60 },
  ],
};

const UPTIME = {
  monitor_id: 1,
  windows: [
    {
      window: "24h",
      window_s: 86400,
      total: 100,
      up: 99,
      down: 1,
      uptime: 99,
      avg_latency_ms: 30,
    },
    {
      window: "30d",
      window_s: 2592000,
      total: 0,
      up: 0,
      down: 0,
      uptime: null,
    },
  ],
};

const INCIDENTS = {
  incidents: [
    {
      id: 5,
      monitor_id: 1,
      started_at: "2026-09-10T08:00:00Z",
      confirmed_at: "2026-09-10T08:01:00Z",
      resolved_at: "2026-09-10T09:00:00Z",
      confirmed: true,
      resolved: true,
      acked: false,
      duration_s: 3600,
      last_error: "500 Internal Server Error",
    },
  ],
};

/** Routes each URL the screen asks for to its own payload. */
function renderDetail(options: {
  monitors?: unknown[]; id?: string;
  check?: typeof import("../monitors/inventoryApi").checkMonitorNow;
  canWrite?: boolean;
} = {}) {
  const { monitors = [apiMonitor()], id = "1" } = options;
  const fetchMock = vi.fn(async (url: string) => {
    const body = url.includes("/heartbeats")
      ? { heartbeats: [] }
      : url.includes("/latency")
      ? LATENCY
      : url.includes("/uptime")
      ? UPTIME
      : url.includes("/incidents")
        ? INCIDENTS
        : { monitors };
    return { ok: true, status: 200, json: async () => body };
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const mounted = render(
    <LiveMonitorDetailRoot
      client={client}
      id={id}
      check={options.check}
      canWrite={options.canWrite}
      beatWidth={400}
      createEventSource={() => new FakeSource()}
    />,
  );
  return { fetchMock, client, mounted };
}

beforeEach(() => {
  FakeSource.last = null;
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("LiveMonitorDetail", () => {
  it("keeps pending results on their monitor across route changes", async () => {
    const resolvers: Record<string, (value: import("../monitors/inventoryApi").CheckOutcome) => void> = {};
    const check = vi.fn((id: string) => new Promise<import("../monitors/inventoryApi").CheckOutcome>((done) => { resolvers[id] = done; }));
    const { client, mounted } = renderDetail({ check, monitors: [apiMonitor(), apiMonitor({ id: 2, name: "cdn" })] });
    const invalidate = vi.spyOn(client, "invalidateQueries");
    fireEvent.click(await screen.findByRole("button", { name: "Check now" }));
    await waitFor(() => expect(check).toHaveBeenCalledWith("1"));
    mounted.rerender(<LiveMonitorDetailRoot client={client} id="2" check={check} createEventSource={() => new FakeSource()} />);
    await screen.findByRole("heading", { name: "cdn" });
    fireEvent.click(screen.getByRole("button", { name: "Check now" }));
    await waitFor(() => expect(check).toHaveBeenCalledWith("2"));
    await act(async () => resolvers["1"]({ ok: true, latencyMs: 123, recorded: true }));
    expect(screen.queryByText(/Check passed/)).toBeNull();
    expect((screen.getByRole("button", { name: "Checking…" }) as HTMLButtonElement).disabled).toBe(true);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["monitor-detail", "1"] });
    await act(async () => resolvers["2"]({ ok: false, latencyMs: 9, error: "cdn refused", recorded: false }));
    expect((await screen.findByText(/Check failed/)).textContent).toContain("cdn refused");
    expect(screen.queryByText(/123 ms/)).toBeNull();
  });
  it("checks once while pending and refreshes recorded results without inventing status", async () => {
    let resolve!: (value: import("../monitors/inventoryApi").CheckOutcome) => void;
    const check = vi.fn(() => new Promise<import("../monitors/inventoryApi").CheckOutcome>((done) => { resolve = done; }));
    const { client } = renderDetail({ check });
    const invalidate = vi.spyOn(client, "invalidateQueries");
    const button = await screen.findByRole("button", { name: "Check now" });
    act(() => { button.click(); button.click(); });
    await waitFor(() => expect(check).toHaveBeenCalledTimes(1));
    expect(check).toHaveBeenCalledWith("1");
    expect((screen.getByRole("button", { name: "Checking…" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => resolve({ ok: true, latencyMs: 12, statusCode: 204, recorded: true }));
    expect(await screen.findByText(/Check passed/)).toBeTruthy();
    expect(screen.getByText(/Check passed/).textContent).toContain("12 ms · HTTP 204 · Recorded");
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["monitors"] }));
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["monitor-detail", "1"] });
  });

  it("shows an unrecorded paused check without changing or refetching dashboard state", async () => {
    const check = vi.fn(async () => ({ ok: true, latencyMs: 8, recorded: false }));
    const { client } = renderDetail({ check, monitors: [apiMonitor({ enabled: false })] });
    const invalidate = vi.spyOn(client, "invalidateQueries");
    fireEvent.click(await screen.findByRole("button", { name: "Check now" }));
    expect(await screen.findByText(/Check passed/)).toBeTruthy();
    expect(screen.getByText(/Check passed/).textContent).toContain("Not recorded");
    expect(document.querySelector(".mon-detail")?.getAttribute("data-status")).toBe("paused");
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("reports a rejected check in place and allows a retry", async () => {
    const check = vi.fn().mockRejectedValueOnce(new Error("please wait five seconds"))
      .mockResolvedValueOnce({ ok: false, latencyMs: 3, error: "connection refused", recorded: true });
    renderDetail({ check });
    fireEvent.click(await screen.findByRole("button", { name: "Check now" }));
    expect((await screen.findByRole("alert")).textContent).toContain("please wait five seconds");
    fireEvent.click(screen.getByRole("button", { name: "Check now" }));
    expect(await screen.findByText(/Check failed/)).toBeTruthy();
    expect(screen.getByText(/Check failed/).textContent).toContain("connection refused");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(check).toHaveBeenCalledTimes(2);
  });

  it.each(["push", "viewer"])("does not offer an impossible check: %s", async (kind) => {
    renderDetail({ monitors: [apiMonitor(kind === "push" ? { type: "push", push_interval_s: 3600 } : {})], canWrite: kind !== "viewer" });
    await screen.findByText("api");
    expect(screen.queryByRole("button", { name: "Check now" })).toBeNull();
  });

  it("shows the monitor with its uptime and incident history", async () => {
    renderDetail();
    expect(await screen.findByText("api")).toBeTruthy();
    expect(await screen.findByText("24h", { selector: "dt" })).toBeTruthy();
    await waitFor(() => {
      // SUB-34 turned the log line into a sentence: "Down from …, 1 h.
      // Recovered at 09:00." The duration and the ending are both asserted,
      // because either alone would pass against a row that lost the other.
      expect(document.body.textContent).toContain("1 h");
      expect(document.body.textContent).toMatch(/Recovered at/);
    });
    expect(document.body.textContent).toContain("500 Internal Server Error");
  });

  it("keeps the loaded history on screen when a later refresh fails", async () => {
    const { fetchMock, client } = renderDetail();
    expect(await screen.findByText("24h", { selector: "dt" })).toBeTruthy();
    const ok = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string) =>
      url.includes("/uptime") || url.includes("/incidents")
        ? ({ ok: false, status: 502, json: async () => ({ error: "bad gateway" }) }) as never
        : ok(url));
    await act(async () => { await client.refetchQueries({ queryKey: ["monitor-detail"] }); });
    await waitFor(() => expect(document.body.textContent).toContain("Could not refresh uptime"));
    expect(screen.getByText("24h", { selector: "dt" })).toBeTruthy();
    expect(document.body.textContent).toMatch(/Recovered at/);
  });

  it("keeps following the stream, so the status is not a frozen snapshot", async () => {
    // The whole reason the monitor is selected out of the live list rather
    // than fetched from /monitors/:id. If this breaks, the page shows a
    // heartbeat bar advancing beside a status line that never changes.
    const { fetchMock } = renderDetail();
    await screen.findByText("api");
    expect(screen.queryByText("Down")).toBeNull();

    const before = fetchMock.mock.calls.length;
    act(() => {
      FakeSource.last?.open();
      FakeSource.last?.send("status", {
        kind: "status",
        monitor_id: 1,
        at: "2026-09-11T08:01:00Z",
        data: { event: "incident_confirmed", error: "connection refused" },
      });
    });

    expect(await screen.findByText("Down")).toBeTruthy();
    expect(document.body.textContent).toContain("connection refused");
    // Patched from the frame, not refetched.
    expect(fetchMock.mock.calls.length).toBe(before);
  });

  it("stays stale until the stream is actually connected", async () => {
    // The monitor can arrive from the shared cache before the SSE connection
    // is up. Showing live colours then presents cached data as current truth.
    renderDetail();
    await screen.findByText("api");
    expect(
      document.querySelector(".mon-detail")?.getAttribute("data-conn"),
    ).toBe("stale");
    act(() => {
      FakeSource.last?.open();
    });
    await waitFor(() => {
      expect(
        document.querySelector(".mon-detail")?.getAttribute("data-conn"),
      ).toBe("live");
    });
  });

  it("drains the colour when the stream drops, like the dashboard does", async () => {
    renderDetail();
    await screen.findByText("api");
    act(() => {
      FakeSource.last?.open();
    });
    act(() => {
      FakeSource.last?.fail();
    });
    await waitFor(() => {
      expect(
        document.querySelector(".mon-detail")?.getAttribute("data-conn"),
      ).toBe("stale");
    });
  });

  it("says a missing monitor is missing, and does not say so while loading", async () => {
    // The common version of this bug tells the user their monitor was deleted
    // while the list request is still in flight.
    renderDetail({ monitors: [], id: "999" });
    expect(document.body.textContent).toContain("Loading monitor…");
    expect(document.body.textContent).not.toContain("does not exist");
    await waitFor(() => {
      expect(document.body.textContent).toContain("does not exist");
    });
  });

  it("renders an unknown long-window uptime as unknown, not as 0%", async () => {
    renderDetail();
    expect(await screen.findByText("30d", { selector: "dt" })).toBeTruthy();
    expect(document.body.textContent).toContain("no eligible checks");
  });
});

describe("LiveMonitorDetail latency", () => {
  it("draws the latency series and re-asks for it when the window changes", async () => {
    const { fetchMock } = renderDetail();
    expect(await screen.findByTestId("lat-plot")).toBeTruthy();
    expect(screen.getByRole("group", { name: "Latency window" })).toBeTruthy();
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/latency?window=24h"))).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "7d" }));
    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/latency?window=7d"))).toBe(true);
    });
  });

  it("asks for no latency on a push monitor, which is reported to rather than probed", async () => {
    const { fetchMock } = renderDetail({ monitors: [apiMonitor({ type: "push", push_interval_s: 3600 })] });
    await screen.findByText("api");
    expect(screen.queryByRole("group", { name: "Latency window" })).toBeNull();
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/latency"))).toBe(false);
  });
});
