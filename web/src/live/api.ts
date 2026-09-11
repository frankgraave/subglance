/**
 * The one HTTP call the dashboard makes.
 *
 * Kept apart from the hook so a test can supply its own fetcher without
 * stubbing globals, and so the query key lives next to the request it names.
 */

import { monitorsFromApi } from "../monitors/types";
import type { ApiMonitor, Monitor } from "../monitors/types";

/**
 * How much history the first load asks for.
 *
 * It matches MAX_LIVE_BEATS: the bar must not visibly change shape once the
 * stream takes over from the fetch. 100 beats over ~60 monitors is a single
 * window-function query on the server (`RecentHeartbeatsForAll`), not N+1.
 */
export const INITIAL_BEATS = 100;

export const monitorsQueryKey = ["monitors", INITIAL_BEATS] as const;

/**
 * Loads every monitor with its recent heartbeats.
 *
 * `credentials: "same-origin"` because the browser authenticates with the
 * session cookie; the API also accepts a bearer token, but that path belongs
 * to machines.
 */
export async function fetchMonitors(signal?: AbortSignal): Promise<Monitor[]> {
  const res = await fetch(`/api/v1/monitors?heartbeats=${INITIAL_BEATS}`, {
    credentials: "same-origin",
    headers: { Accept: "application/json" },
    signal,
  });
  if (!res.ok) {
    throw new Error(`could not load monitors: HTTP ${res.status}`);
  }
  const body = (await res.json()) as { monitors?: ApiMonitor[] };
  return monitorsFromApi(body);
}
