// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
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

  addEventListener(type: string, listener: (event: MessageEvent) => void): void {
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
    this.listeners.get(type)?.({ data: JSON.stringify(body), lastEventId: "" } as MessageEvent);
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

const UPTIME = {
  monitor_id: 1,
  windows: [
    { window: "24h", window_s: 86400, total: 100, up: 99, down: 1, uptime: 99, avg_latency_ms: 30 },
    { window: "30d", window_s: 2592000, total: 0, up: 0, down: 0, uptime: null },
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
function renderDetail(options: { monitors?: unknown[]; id?: string } = {}) {
  const { monitors = [apiMonitor()], id = "1" } = options;
  const fetchMock = vi.fn(async (url: string) => {
    const body = url.includes("/uptime")
      ? UPTIME
      : url.includes("/incidents")
        ? INCIDENTS
        : { monitors };
    return { ok: true, status: 200, json: async () => body };
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <LiveMonitorDetailRoot
      client={client}
      id={id}
      beatWidth={400}
      createEventSource={() => new FakeSource()}
    />,
  );
  return { fetchMock };
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
  it("shows the monitor with its uptime and incident history", async () => {
    renderDetail();
    expect(await screen.findByText("api")).toBeTruthy();
    expect(await screen.findByText("24h")).toBeTruthy();
    await waitFor(() => {
      expect(document.body.textContent).toContain("Resolved after 1 h");
    });
    expect(document.body.textContent).toContain("500 Internal Server Error");
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
      expect(document.querySelector(".mon-detail")?.getAttribute("data-conn")).toBe("stale");
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
    expect(await screen.findByText("30d")).toBeTruthy();
    expect(document.body.textContent).toContain("no checks");
  });
});
