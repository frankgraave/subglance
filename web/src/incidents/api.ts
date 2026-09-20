/**
 * The incident requests that are not part of a monitor's detail fetch.
 *
 * `fetchOpenIncidents` answers "what is broken right now, across everything",
 * which is the question the incidents screen exists for and the one the
 * dashboard cannot answer without opening twenty monitors.
 *
 * `fetchResolvedIncidents` answers its past tense — what recovered — and it is
 * one request because the server finally has an endpoint for that question.
 * The version this replaced asked every monitor separately and gave up after
 * 24 of them.
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
 * How far back the history card reaches by default.
 *
 * 30 days, which is what fits a grouped-by-day list before it needs paging,
 * and what the screen has always shown. It is a default rather than a constant
 * because the window is now a control the reader can move (SUB-136): the SLA
 * question people actually ask is "how did last quarter look", and until
 * `GET /api/v1/incidents/resolved` existed a longer window was the *expensive*
 * direction — every extra day multiplied a per-monitor fan-out. One paged
 * request makes 90 days cost what 30 did, which is why the control could be
 * built working rather than shipped disabled.
 */
export const HISTORY_DAYS = 30;

/**
 * The windows the history control offers.
 *
 * Four rungs rather than a date picker: these are the four questions people
 * actually ask of an incident history — what happened overnight, this week,
 * this month, this quarter — and a free date range invites precision the card
 * does not otherwise have (it groups by day and counts in minutes). A picker
 * can come when somebody needs an exact range, and it will change this list
 * rather than the screen.
 */
export const HISTORY_WINDOWS = [
  { days: 1, label: "24 hours" },
  { days: 7, label: "7 days" },
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
] as const;

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
 * How long a page of history is.
 *
 * A page rather than everything, because "everything" on a year-old instance
 * is a request nobody asked for to draw a card they may not scroll. 50 is a
 * screenful of grouped days; the rest is one click away and the card says so.
 */
export const HISTORY_PAGE_LIMIT = 50;

export type ResolvedHistoryPage = {
  incidents: Incident[];
  /**
   * True when older incidents remain inside the window.
   *
   * This is the server's answer, not a guess. It used to be inferred here from
   * a full page — which cannot distinguish a monitor that had exactly fifty
   * outages from one that had sixty — and before that the card carried a
   * single `truncated` flag covering three different causes, because all three
   * left the reader asking the same question: can I trust this list?
   *
   * Two of those causes are gone rather than renamed. The 24-monitor fan-out
   * cap is gone because there is no fan-out; the full-page heuristic is gone
   * because the server now reads one row past the page and states the answer.
   * The third, a request that simply failed, is not a completeness flag at
   * all — it is one request now, so a failure fails the query and the card
   * reports it as the error it is.
   */
  hasMore: boolean;
  /** Pass back as `cursor` for the next page. Null when there is no next. */
  nextCursor: string | null;
};

/**
 * One page of recently resolved incidents, across every monitor.
 *
 * One request, answered by `GET /api/v1/incidents/resolved`. The version this
 * replaced sent one request per monitor and stopped at 24 of them, so on a
 * 200-monitor instance the history was simply missing most of the estate —
 * and the per-monitor endpoint's silent `LIMIT 50` meant even the monitors it
 * did reach could be short without anyone knowing.
 *
 * Ordered by resolution, newest first, and the window is measured on
 * resolution too: this card answers "what came back", so an outage that began
 * five weeks ago and recovered yesterday belongs in a 30-day list.
 */
export async function fetchResolvedIncidents(
  days: number = HISTORY_DAYS,
  cursor: string | null = null,
  signal?: AbortSignal,
): Promise<ResolvedHistoryPage> {
  const params = new URLSearchParams({
    days: String(days),
    limit: String(HISTORY_PAGE_LIMIT),
  });
  if (cursor !== null) params.set("cursor", cursor);

  const res = await apiFetch(`/api/v1/incidents/resolved?${params}`, { signal });
  if (!res.ok) {
    throw new Error(`could not load resolved incidents: HTTP ${res.status}`);
  }
  const body = (await res.json()) as {
    incidents?: ApiIncident[];
    has_more?: boolean;
    next_cursor?: string;
  };
  return {
    incidents: (body.incidents ?? []).map(incidentFromApi),
    hasMore: body.has_more === true,
    nextCursor: body.next_cursor ?? null,
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
