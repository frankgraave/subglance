// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveDashboardRoot } from "./LiveDashboard";
import type { EventSourceLike } from "./connection";

/**
 * The acceptance criterion of SUB-26, end to end in jsdom: a monitor that
 * falls over is visible on the dashboard without a refresh, and a dashboard
 * that lost its stream says so instead of quietly showing stale green.
 */

/** A stub EventSource the test drives directly. */
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

function renderLive(monitors: unknown[] = [apiMonitor()]) {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    json: async () => ({ monitors }),
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <LiveDashboardRoot
      client={client}
      compact={false}
      beatWidth={200}
      createEventSource={() => new FakeSource()}
    />,
  );
  return { fetchMock, client };
}

beforeEach(() => {
  FakeSource.last = null;
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("LiveDashboard", () => {
  it("renders the fetched monitors", async () => {
    renderLive();
    expect(await screen.findByText("api")).toBeTruthy();
  });

  it("turns a monitor red from a status frame, without refetching", async () => {
    const { fetchMock } = renderLive();
    await screen.findByText("api");
    // The count sits in its own <b>, so the label is the stable handle.
    expect(screen.queryByText("down", { exact: true })).toBeNull();

    act(() => {
      FakeSource.last?.open();
      FakeSource.last?.send("status", {
        kind: "status",
        monitor_id: 1,
        at: "2026-09-11T08:01:00Z",
        data: { event: "incident_confirmed", error: "500" },
      });
    });

    // The acceptance criterion: visible on the dashboard, no reload.
    await waitFor(() => {
      const label = screen.getByText("down", { exact: true });
      expect(label.parentElement?.textContent).toContain("1");
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("announces the transition in the live region", async () => {
    renderLive();
    await screen.findByText("api");
    act(() => {
      FakeSource.last?.open();
      FakeSource.last?.send("status", {
        monitor_id: 1,
        at: "2026-09-11T08:01:00Z",
        data: { event: "incident_confirmed" },
      });
    });
    await waitFor(() =>
      expect(screen.getByText(/1 monitor down: api\./)).toBeTruthy(),
    );
  });

  it("shows nothing about the connection while it is healthy", async () => {
    renderLive();
    await screen.findByText("api");
    act(() => FakeSource.last?.open());
    // A permanent "live" badge is decoration that trains people to ignore the
    // element that matters. Silence while healthy is the design.
    expect(screen.queryByText(/Connection lost/)).toBeNull();
  });

  it("warns that the numbers are frozen when the stream dies", async () => {
    renderLive();
    await screen.findByText("api");
    act(() => {
      FakeSource.last?.open();
      FakeSource.last?.fail();
    });
    expect(await screen.findByText(/Connection lost/)).toBeTruthy();
  });

  it("refetches instead of patching when the server reports a gap", async () => {
    const { fetchMock } = renderLive();
    await screen.findByText("api");
    act(() => {
      FakeSource.last?.open();
      FakeSource.last?.send("hello", { seq: 90, gap: true, missed: 12 });
    });
    // The held list is provably incomplete; frames that follow cannot fill a
    // hole, so the only honest move is to load it again.
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  });

  it("does not refetch on a clean hello", async () => {
    const { fetchMock } = renderLive();
    await screen.findByText("api");
    act(() => {
      FakeSource.last?.open();
      FakeSource.last?.send("hello", { seq: 90, gap: false });
    });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("reports a failed first load instead of showing an empty dashboard", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 503, json: async () => ({}) }),
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<LiveDashboardRoot client={client} createEventSource={() => new FakeSource()} />);
    expect(await screen.findByRole("alert")).toBeTruthy();
  });
});
