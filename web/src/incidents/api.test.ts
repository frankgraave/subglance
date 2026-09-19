import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchResolvedIncidents, HISTORY_PAGE_LIMIT } from "./api";

/**
 * What the history card is allowed to claim, now that the server answers the
 * question instead of the browser assembling it.
 *
 * This used to test a client-side fan-out — one request per monitor, capped at
 * 24 — and most of what it asserted was about the ways that construction could
 * be silently short. Two of those ways no longer exist: there is no cap and
 * there is no per-monitor page to fill. What survives is the rule they all
 * served, and it is the one this product cannot afford to break: **a list that
 * is not the whole window must say so.**
 *
 * The difference is that the answer is now the server's `has_more` rather than
 * an inference from a full page. A page of exactly fifty and a page of fifty
 * with more behind it are the same array; only the API can tell them apart,
 * and these tests pin that the client carries its answer through rather than
 * re-deriving one.
 */

const T0 = Date.UTC(2026, 8, 16, 12, 0, 0);

function apiIncident(over: Record<string, unknown> = {}) {
  return {
    id: 1,
    monitor_id: 1,
    started_at: new Date(T0 - 3_600_000).toISOString(),
    resolved_at: new Date(T0 - 1_800_000).toISOString(),
    confirmed: true,
    resolved: true,
    acked: false,
    duration_s: 1800,
    cause: "dns",
    ...over,
  };
}

let requested: string[] = [];

function mockFetch(handler: (url: string) => Response | Promise<Response>) {
  requested = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : String(input);
      requested.push(url);
      return Promise.resolve(handler(url));
    }),
  );
}

const ok = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { "content-type": "application/json" },
  });

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("one instance-wide request, not one per monitor", () => {
  it("asks the resolved endpoint once, whatever the estate size", async () => {
    mockFetch(() => ok({ incidents: [apiIncident()], has_more: false }));

    const page = await fetchResolvedIncidents(30);

    expect(requested).toHaveLength(1);
    expect(requested[0]).toContain("/api/v1/incidents/resolved");
    expect(page.incidents).toHaveLength(1);
  });

  it("sends the window and the page size it was asked for", async () => {
    mockFetch(() => ok({ incidents: [], has_more: false }));

    await fetchResolvedIncidents(90);

    const url = new URL(requested[0], "https://example.test");
    expect(url.searchParams.get("days")).toBe("90");
    expect(url.searchParams.get("limit")).toBe(String(HISTORY_PAGE_LIMIT));
    expect(url.searchParams.has("cursor")).toBe(false);
  });

  it("passes a cursor through when continuing a page", async () => {
    mockFetch(() => ok({ incidents: [], has_more: false }));

    await fetchResolvedIncidents(30, "1755432000.1758024000.412");

    const url = new URL(requested[0], "https://example.test");
    expect(url.searchParams.get("cursor")).toBe("1755432000.1758024000.412");
  });
});

describe("completeness is the server's answer, not a guess", () => {
  it("reports more behind the page when the API says so, with the cursor", async () => {
    mockFetch(() =>
      ok({
        incidents: [apiIncident()],
        has_more: true,
        next_cursor: "1755432000.1758024000.412",
      }),
    );

    const page = await fetchResolvedIncidents(30);

    expect(page.hasMore).toBe(true);
    expect(page.nextCursor).toBe("1755432000.1758024000.412");
  });

  /*
   * The bug the old client could not avoid, pinned so it cannot return.
   *
   * A page that is exactly full used to be treated as evidence of more behind
   * it, because the per-monitor endpoint gave nothing better to go on — so a
   * monitor with exactly fifty outages was reported as an incomplete month.
   * The server now reads one row past the page to answer this, and the client
   * must believe it rather than re-deriving an answer from the length.
   */
  it("does not invent truncation from a page that happens to be full", async () => {
    const incidents = Array.from({ length: HISTORY_PAGE_LIMIT }, (_, i) =>
      apiIncident({ id: i + 1, monitor_id: i + 1 }),
    );
    mockFetch(() => ok({ incidents, has_more: false }));

    const page = await fetchResolvedIncidents(30);

    expect(page.incidents).toHaveLength(HISTORY_PAGE_LIMIT);
    expect(page.hasMore).toBe(false);
    expect(page.nextCursor).toBeNull();
  });

  it("treats a missing has_more as complete rather than as unknown", async () => {
    mockFetch(() => ok({ incidents: [apiIncident()] }));

    const page = await fetchResolvedIncidents(30);

    expect(page.hasMore).toBe(false);
    expect(page.nextCursor).toBeNull();
  });
});

/*
 * A failed request is an error, not a short list.
 *
 * Under the fan-out a single monitor's 500 left most of the card usable, so it
 * was folded into the completeness flag. One request means a failure is total:
 * returning an empty array with `hasMore: false` would be this function
 * claiming a quiet month it did not read.
 */
describe("a failed request is not an empty month", () => {
  it("throws rather than reporting an empty, complete history", async () => {
    mockFetch(() => new Response("boom", { status: 500 }));

    await expect(fetchResolvedIncidents(30)).rejects.toThrow(/HTTP 500/);
  });
});
