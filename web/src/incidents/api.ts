/**
 * The two incident requests that are not part of a monitor's detail fetch.
 *
 * `fetchOpenIncidents` answers "what is broken right now, across everything",
 * which is the question the incidents screen exists for and the one the
 * dashboard cannot answer without opening twenty monitors.
 *
 * `ackIncident` is the write. It goes through `apiRequest` like every other
 * call, so a 401 still announces itself in one place; there is no CSRF header
 * because the server authenticates the origin with Sec-Fetch-Site rather than
 * with a token (internal/api/auth.go).
 */

import { apiFetch, apiRequest } from "../api/http";
import { incidentFromApi } from "../monitors/detail";
import type { ApiIncident, Incident } from "../monitors/detail";

export const openIncidentsQueryKey = ["incidents", "open"] as const;

/**
 * How far back the history card reaches.
 *
 * 30 days, which is what fits a grouped-by-day list before it needs paging.
 * It is a *parameter* rather than a constant spread through the code for a
 * reason: the SLA question people eventually ask is "how did last quarter
 * look", and when a longer window or a date picker arrives it changes this
 * number and the query key, not the screen. Nothing is staged for that today —
 * a disabled date control shipped ahead of the feature is a promise with the
 * wiring cut, which is the mistake the sidebar's "Soon" labels already made.
 */
export const HISTORY_DAYS = 30;

export const resolvedIncidentsQueryKey = (days: number) =>
  ["incidents", "resolved", days] as const;

/** Every incident that has not resolved, newest first. */
export async function fetchOpenIncidents(
  signal?: AbortSignal,
): Promise<Incident[]> {
  const res = await apiFetch("/api/v1/incidents", { signal });
  if (!res.ok) {
    throw new Error(`could not load incidents: HTTP ${res.status}`);
  }
  const body = (await res.json()) as { incidents?: ApiIncident[] };
  return (body.incidents ?? []).map(incidentFromApi);
}

/**
 * How many monitors the history card will ask for incidents from.
 *
 * **This cap exists because the backend has no endpoint for this question.**
 * `GET /api/v1/incidents` returns open incidents only, and resolved history is
 * per monitor — so "what resolved across the instance in the last 30 days" has
 * to be assembled from one request per monitor. On a 12-monitor instance that
 * is nothing; on a 200-monitor one it is 200 requests to draw a card nobody
 * opened the page for.
 *
 * So the fan-out stops here and the card says it stopped, rather than quietly
 * showing a partial history as though it were complete. The real fix is a
 * server-side endpoint, which is a backend ticket rather than something to
 * fake on this side.
 */
export const HISTORY_MONITOR_LIMIT = 24;

/**
 * Page size asked of the API, matching the server's own `LIMIT 50`.
 *
 * Named rather than inlined because the comparison that decides whether a
 * page was capped has to use the same number the request asked for.
 */
export const INCIDENT_PAGE_LIMIT = 50;

export type ResolvedHistory = {
  incidents: Incident[];
  /**
   * True when this history is not the whole window.
   *
   * Three causes, one flag, because the reader's question is the same for all
   * of them: can I trust this list to be complete? `HISTORY_MONITOR_LIMIT` cut
   * the fan-out short; a request that did go out failed; or a monitor filled
   * its page and the API gave no way to ask for the rest.
   *
   * All three were once invisible. A failed monitor contributed an empty array
   * and the card reported a complete history silently missing that monitor's
   * outages, and a capped page looked exactly like a monitor that happened to
   * have fifty. For a tool whose whole claim is that the screen can be
   * trusted, quietly short is not acceptable; visibly short is.
   */
  truncated: boolean;
};

/**
 * Recently resolved incidents across monitors, assembled client-side.
 *
 * One request per monitor, in parallel, then filtered to the window and sorted
 * newest first. A monitor whose request fails contributes nothing rather than
 * failing the whole card: a history panel that disappears because one monitor
 * of twelve 500'd is worse than one that is quietly short — but it must say
 * so, which is what `truncated` carries.
 */
export async function fetchResolvedIncidents(
  monitorIds: readonly string[],
  days: number = HISTORY_DAYS,
  now: number = Date.now(),
  signal?: AbortSignal,
): Promise<ResolvedHistory> {
  const ids = monitorIds.slice(0, HISTORY_MONITOR_LIMIT);
  const cutoff = now - days * 86_400_000;

  let incomplete = false;

  const pages = await Promise.all(
    ids.map(async (id) => {
      try {
        const res = await apiFetch(
          `/api/v1/monitors/${encodeURIComponent(id)}/incidents?limit=${INCIDENT_PAGE_LIMIT}`,
          { signal },
        );
        if (!res.ok) {
          incomplete = true;
          return [];
        }
        const body = (await res.json()) as { incidents?: ApiIncident[] };
        const page = body.incidents ?? [];
        /*
         * A full page means there may be more behind it.
         *
         * `ListIncidents` applies LIMIT 50 and returns no completeness
         * metadata, so a monitor that filled the page may have older incidents
         * still inside the 30-day window that this card will never see. That
         * is indistinguishable, on screen, from a monitor that simply had 50 —
         * unless we say so. Pagination or a total from the API is the real
         * fix and belongs on the backend ticket; until then the card admits
         * the ceiling rather than presenting a capped list as a full month.
         */
        if (page.length >= INCIDENT_PAGE_LIMIT) incomplete = true;
        return page.map(incidentFromApi);
      } catch (err) {
        /*
         * A cancelled request is not a failure and must not be reported as
         * one: React Query aborts the previous fetch on every refetch, so
         * swallowing an abort here would mark almost every load incomplete.
         * Rethrowing lets the query layer recognise its own cancellation.
         */
        if (err instanceof DOMException && err.name === "AbortError") throw err;
        incomplete = true;
        return [];
      }
    }),
  );

  const incidents = pages
    .flat()
    .filter(
      (incident) =>
        /*
         * Filtered on `resolvedAt`, not `startedAt`. This list answers "what
         * recovered recently", so an outage that began five weeks ago and
         * came back yesterday belongs in it — filtering on the start date
         * dropped exactly the long outages a reader most wants to find.
         */
        incident.resolved &&
        incident.resolvedAt !== null &&
        incident.resolvedAt >= cutoff,
    )
    .sort((a, b) => (b.resolvedAt ?? 0) - (a.resolvedAt ?? 0));

  return {
    incidents,
    truncated: incomplete || monitorIds.length > ids.length,
  };
}

/**
 * Acknowledges an incident: "seen, working on it".
 *
 * It returns nothing, and that is the API being precise rather than terse —
 * `POST /api/v1/incidents/{id}/ack` answers 204, because acking changes one
 * timestamp and leaves everything else about the incident exactly as it was.
 * The caller refetches to see the new state rather than trusting a body that
 * does not exist.
 */
export async function ackIncident(id: string, signal?: AbortSignal): Promise<void> {
  await apiRequest(`/api/v1/incidents/${encodeURIComponent(id)}/ack`, {
    method: "POST",
    signal,
  });
}
