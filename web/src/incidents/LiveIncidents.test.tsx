// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveIncidentsRoot } from "./LiveIncidents";
import { ackIncident, fetchOpenIncidents } from "./api";
import type { EventSourceLike } from "../live/connection";

/**
 * SUB-34, end to end in jsdom: the acknowledge button reaches the server, and
 * the screen learns the new state from the server rather than assuming it.
 *
 * That second half is the reason this file exists rather than a unit test on
 * the mutation. An optimistic write would be the obvious implementation and it
 * is the wrong one here: the whole claim an acked row makes is "a human has
 * seen this", and a row that says so because the browser guessed a request
 * would succeed is the product inventing the one fact this control records.
 * So the test asserts a refetch, not a local flip.
 */

/**
 * A stream that opens.
 *
 * It has to: anything other than a confirmed live connection is stale, and a
 * stale screen deliberately states everything in the past tense (SUB-111). A
 * fake that never opens would make every assertion here about the dead-stream
 * wording instead of about acknowledging.
 */
class FakeSource implements EventSourceLike {
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  readyState = 0;
  constructor() {
    queueMicrotask(() => {
      this.readyState = 1;
      this.onopen?.(new Event("open"));
    });
  }
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}

type ApiIncidentFixture = {
  id: number;
  monitor_id: number;
  started_at: string;
  confirmed_at: string;
  acked_at?: string;
  confirmed: boolean;
  resolved: boolean;
  acked: boolean;
  duration_s: number;
  cause: string;
};

const apiIncident = (
  over: Partial<ApiIncidentFixture> = {},
): ApiIncidentFixture => ({
  id: 5,
  monitor_id: 1,
  started_at: "2026-09-10T08:00:00Z",
  confirmed_at: "2026-09-10T08:01:00Z",
  confirmed: true,
  resolved: false,
  acked: false,
  duration_s: 720,
  cause: "dns",
  ...over,
});

const apiMonitor = {
  id: 1,
  name: "api",
  type: "http",
  target: "https://api.example.com",
  interval_s: 60,
  timeout_s: 10,
  enabled: true,
  status: "down",
  last_check: "2026-09-10T08:00:00Z",
  created_at: "2026-09-01T00:00:00Z",
};

beforeEach(() => {
  vi.useRealTimers();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderScreen(pages: ApiIncidentFixture[][]) {
  let call = 0;
  const fetchIncidents = vi.fn(async () => {
    const page = pages[Math.min(call, pages.length - 1)];
    call += 1;
    return page.map((raw) => ({
      id: String(raw.id),
      monitorId: String(raw.monitor_id),
      startedAt: Date.parse(raw.started_at),
      confirmedAt: Date.parse(raw.confirmed_at),
      resolvedAt: null,
      ackedAt: raw.acked_at === undefined ? null : Date.parse(raw.acked_at),
      confirmed: raw.confirmed,
      resolved: raw.resolved,
      acked: raw.acked,
      durationS: raw.duration_s,
      cause: raw.cause,
    }));
  });
  const ack = vi.fn(async () => {});
  /* No history in these fixtures: this file is about the ack round trip, and a
     second list would add noise without adding an assertion. */
  const fetchHistory = vi.fn(async () => ({
    incidents: [],
    hasMore: false,
    nextCursor: null,
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ monitors: [apiMonitor] }),
    })),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <LiveIncidentsRoot
      client={client}
      fetchIncidents={fetchIncidents as unknown as typeof fetchOpenIncidents}
      fetchHistory={
        fetchHistory as unknown as typeof import("./api").fetchResolvedIncidents
      }
      ack={ack as unknown as typeof ackIncident}
      createEventSource={() => new FakeSource()}
    />,
  );
  return { fetchIncidents, ack };
}

describe("acknowledging, end to end", () => {
  it("sends the ack and then re-reads the state from the server", async () => {
    const { ack, fetchIncidents } = renderScreen([
      [apiIncident()],
      [apiIncident({ acked: true, acked_at: "2026-09-10T08:07:00Z" })],
    ]);

    const button = await screen.findByRole("button", { name: /mute repeat/i });
    button.click();

    await waitFor(() => expect(ack).toHaveBeenCalledWith("5"));
    // The refetch, not a local flip: the second page is what turns the row.
    await waitFor(() =>
      expect(fetchIncidents.mock.calls.length).toBeGreaterThan(1),
    );
    await waitFor(() =>
      expect(document.body.textContent).toMatch(/still down/i),
    );
  });

  it("leaves the incident open after an ack, never closed", async () => {
    // The row must still read as an outage in progress. This is the assertion
    // that would fail if anyone ever wired ack to the resolve path.
    renderScreen([
      [apiIncident({ acked: true, acked_at: "2026-09-10T08:07:00Z" })],
    ]);
    await waitFor(() =>
      expect(
        document.querySelector(".inc-row")?.getAttribute("data-state"),
      ).toBe("acked"),
    );
    expect(document.body.textContent).not.toMatch(/Recovered|Resolved/);
  });

  it("names the monitor from the live list rather than printing an id", async () => {
    renderScreen([[apiIncident()]]);
    await waitFor(() => expect(document.body.textContent).toContain("api"));
    expect(document.body.textContent).not.toContain("Monitor 1");
  });
});

describe("a monitor list that failed is not a monitor list of zero", () => {
  it("does not announce an all-clear it cannot back up", async () => {
    /*
     * `useLiveMonitors` reports an empty array both while loading and after a
     * failed fetch. Passing `monitors.length` unconditionally let the empty
     * state say "0 monitors watched, zero confirmed outages" — an
     * authoritative all-clear about a population we had just failed to read.
     *
     * The wording without a count claims nothing, which is the honest thing
     * to say when the answer is unknown.
     */
    const fetchIncidents = vi.fn(async () => []);
    const fetchHistory = vi.fn(async () => ({
      incidents: [],
      hasMore: false,
      nextCursor: null,
    }));
    // The monitor list 500s; the incidents endpoint is fine.
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: false,
        status: 500,
        json: async () => ({}),
      })),
    );
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <LiveIncidentsRoot
        client={client}
        fetchIncidents={fetchIncidents as unknown as typeof fetchOpenIncidents}
        fetchHistory={
          fetchHistory as unknown as typeof import("./api").fetchResolvedIncidents
        }
        ack={vi.fn(async () => {}) as unknown as typeof ackIncident}
        createEventSource={() => new FakeSource()}
      />,
    );

    await waitFor(() => {
      expect(document.body.textContent).toMatch(/nothing is broken/i);
    });
    expect(document.body.textContent).not.toMatch(/0 monitors watched/i);
  });
});
