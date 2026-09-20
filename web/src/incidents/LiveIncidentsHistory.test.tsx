// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveIncidentsRoot } from "./LiveIncidents";
import { setToolbarSlot } from "../shell/topbarSlot";
import type { fetchResolvedIncidents } from "./api";
import type { EventSourceLike } from "../live/connection";
import type { Incident } from "../monitors/detail";

/**
 * SUB-127, where the pieces meet: the screen's history is one paged request,
 * and the two things that can lengthen it actually lengthen it.
 *
 * `api.test.ts` pins the request and `IncidentsToolbar.test.tsx` pins the
 * controls; neither can see the join, and the join is where this feature is
 * easiest to get subtly wrong. A "load older" button that refetches page one,
 * or a window control that relabels the card without refetching, passes both
 * of those files and is wrong on screen — the card would claim 90 days over 30
 * days of rows, which is the same class of quiet inaccuracy the endpoint was
 * built to end.
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

const T0 = Date.UTC(2026, 8, 16, 12, 0, 0);

const resolvedIncident = (id: string, monitorId: string): Incident => ({
  id,
  monitorId,
  startedAt: T0 - 3_600_000,
  confirmedAt: T0 - 3_540_000,
  resolvedAt: T0 - 1_800_000,
  ackedAt: null,
  confirmed: true,
  resolved: true,
  acked: false,
  durationS: 1800,
  cause: "dns",
  lastError: "",
});

let toolbar: HTMLElement;

/** Renders the screen with a toolbar for its controls and a stream that opens. */
function renderScreen(fetchHistory: typeof fetchResolvedIncidents) {
  toolbar = document.createElement("div");
  document.body.append(toolbar);
  setToolbarSlot(toolbar);

  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ monitors: [] }),
    })),
  );

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <LiveIncidentsRoot
      client={client}
      fetchIncidents={vi.fn(async () => [])}
      fetchHistory={fetchHistory}
      ack={vi.fn(async () => {})}
      createEventSource={() => new FakeSource()}
    />,
  );
}

afterEach(() => {
  cleanup();
  setToolbarSlot(null);
  vi.unstubAllGlobals();
});

describe("the history is paged through the API's own cursor", () => {
  it("keeps pending history visible without claiming an empty window", async () => {
    let finish!: (page: Awaited<ReturnType<typeof fetchResolvedIncidents>>) => void;
    renderScreen(() => new Promise((resolve) => { finish = resolve; }));

    await waitFor(() => expect(finish).toBeTypeOf("function"));
    expect(screen.getByRole("region", { name: "Resolved" }).textContent)
      .toContain("Loading resolved history…");
    expect(document.body.textContent).not.toMatch(/nothing resolved|nothing is broken/i);

    fireEvent.change(within(toolbar).getByLabelText("Show"), {
      target: { value: "resolved" },
    });
    expect(document.body.textContent).not.toMatch(/nothing resolved/i);
    expect(screen.getByRole("region", { name: "Resolved" }).textContent)
      .toContain("Loading resolved history…");

    finish({ incidents: [], hasMore: false, nextCursor: null });
    await waitFor(() => {
      expect(screen.getByRole("region", { name: "Resolved" }).textContent)
        .toContain("Nothing resolved in the last 30 days.");
    });
    expect(document.body.textContent).not.toContain("Loading resolved history…");
  });

  it("asks for the next page with the cursor it was given, and keeps the first", async () => {
    const calls: (string | null)[] = [];
    const fetchHistory = vi.fn(async (_days: number, cursor: string | null = null) => {
      calls.push(cursor);
      return cursor === null
        ? {
            incidents: [resolvedIncident("a", "1")],
            hasMore: true,
            nextCursor: "1755432000.1758024000.412",
          }
        : {
            incidents: [resolvedIncident("b", "2")],
            hasMore: false,
            nextCursor: null,
          };
    }) as unknown as typeof fetchResolvedIncidents;

    renderScreen(fetchHistory);

    const more = await screen.findByRole("button", { name: /load older/i });
    fireEvent.click(more);

    await waitFor(() => expect(calls).toEqual([null, "1755432000.1758024000.412"]));

    /*
     * Both pages on screen, not the second replacing the first. "Load older"
     * that swapped the list would lose the rows the reader was already
     * looking at, which is a worse answer than the truncation notice it
     * replaced.
     */
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: /load older/i })).toBeNull();
    });
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
  });

  it("refetches when the window moves rather than relabelling the same rows", async () => {
    const windows: number[] = [];
    const fetchHistory = vi.fn(async (days: number) => {
      windows.push(days);
      return {
        incidents: [resolvedIncident("a", "1")],
        hasMore: false,
        nextCursor: null,
      };
    }) as unknown as typeof fetchResolvedIncidents;

    renderScreen(fetchHistory);

    await waitFor(() => expect(windows).toEqual([30]));

    fireEvent.change(within(toolbar).getByLabelText("History"), {
      target: { value: "90" },
    });

    await waitFor(() => expect(windows).toEqual([30, 90]));
    // And the card says what it actually fetched.
    await waitFor(() =>
      expect(document.body.textContent).toContain("Last 90 days"),
    );
  });
});
