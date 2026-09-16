import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchResolvedIncidents, HISTORY_MONITOR_LIMIT } from "./api";

/**
 * What the history card is allowed to claim.
 *
 * This assembles a month of resolved incidents from one request per monitor,
 * which means it has two ways of being incomplete — the fan-out cap, and a
 * request that simply failed. Both were once invisible in the same way: the
 * failed monitor contributed an empty array, the list came back shorter, and
 * `truncated: false` told the screen it was looking at the whole window.
 *
 * That is the specific failure this product cannot afford. A monitoring tool
 * that quietly omits an outage is worse than one that admits it does not know,
 * because the omission looks exactly like good news.
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

function mockFetch(handler: (url: string) => Response | Promise<Response>) {
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : String(input);
    return Promise.resolve(handler(url));
  }));
}

const ok = (incidents: unknown[]) =>
  new Response(JSON.stringify({ incidents }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("a failed request is not an empty month", () => {
  it("reports the history as incomplete when a monitor's request 500s", async () => {
    mockFetch((url) =>
      url.includes("/monitors/2/")
        ? new Response("boom", { status: 500 })
        : ok([apiIncident({ id: 11, monitor_id: 1 })]),
    );

    const history = await fetchResolvedIncidents(["1", "2"], 30, T0);

    // The surviving monitor's incident is still shown — a broken neighbour
    // must not empty the card.
    expect(history.incidents).toHaveLength(1);
    // But the card may not present that as the whole window.
    expect(history.truncated).toBe(true);
  });

  it("reports incomplete when a request throws", async () => {
    mockFetch((url) => {
      if (url.includes("/monitors/2/")) throw new TypeError("network down");
      return ok([apiIncident({ id: 11, monitor_id: 1 })]);
    });

    const history = await fetchResolvedIncidents(["1", "2"], 30, T0);
    expect(history.truncated).toBe(true);
  });

  it("says complete when every request succeeded", async () => {
    mockFetch(() => ok([apiIncident()]));
    const history = await fetchResolvedIncidents(["1", "2"], 30, T0);
    expect(history.truncated).toBe(false);
  });

  it("still reports incomplete when the fan-out cap bites", async () => {
    mockFetch(() => ok([]));
    const many = Array.from({ length: HISTORY_MONITOR_LIMIT + 1 }, (_, i) => String(i));
    const history = await fetchResolvedIncidents(many, 30, T0);
    expect(history.truncated).toBe(true);
  });

  it("rethrows an abort instead of calling it incomplete data", async () => {
    /*
     * React Query aborts the previous fetch on every refetch. Treating that as
     * a failure would mark nearly every load incomplete, and the notice would
     * become noise the reader learns to ignore — which is how a real short
     * history gets missed.
     */
    mockFetch(() => {
      throw new DOMException("aborted", "AbortError");
    });
    await expect(fetchResolvedIncidents(["1"], 30, T0)).rejects.toThrow(
      /abort/i,
    );
  });
});

describe("the window is about when an outage ended", () => {
  it("keeps a long outage that started before the cutoff and recovered inside it", async () => {
    /*
     * The card asks "what recovered recently". An outage that began five weeks
     * ago and came back yesterday is the single most interesting row it can
     * hold, and filtering on the start date is exactly what dropped it.
     */
    const started = T0 - 35 * 86_400_000;
    const resolved = T0 - 86_400_000;
    mockFetch(() =>
      ok([
        apiIncident({
          id: 99,
          started_at: new Date(started).toISOString(),
          resolved_at: new Date(resolved).toISOString(),
          duration_s: Math.round((resolved - started) / 1000),
        }),
      ]),
    );

    const history = await fetchResolvedIncidents(["1"], 30, T0);
    expect(history.incidents.map((i) => i.id)).toEqual(["99"]);
  });

  it("drops an outage that also ended before the cutoff", async () => {
    const started = T0 - 40 * 86_400_000;
    const resolved = T0 - 39 * 86_400_000;
    mockFetch(() =>
      ok([
        apiIncident({
          id: 98,
          started_at: new Date(started).toISOString(),
          resolved_at: new Date(resolved).toISOString(),
        }),
      ]),
    );

    const history = await fetchResolvedIncidents(["1"], 30, T0);
    expect(history.incidents).toHaveLength(0);
  });
});
