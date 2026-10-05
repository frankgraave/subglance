/**
 * The extra facts a detail view needs, and the translation from the API.
 *
 * The monitor itself is *not* fetched here. It already lives in the dashboard
 * query cache, where the SSE stream keeps it current, and fetching a second
 * copy would give the same monitor two versions of the truth on one screen —
 * a heartbeat bar advancing while the status line beside it stayed frozen at
 * whatever the detail fetch happened to return. So the detail view selects its
 * monitor out of the live list and asks the server only for what the list
 * endpoint does not carry: uptime over longer windows, and incident history.
 *
 * Both come from one hook and one query key, because they are always shown
 * together and a screen that half-loads is worse than one that loads.
 */

import { reminderFromApi, type ReminderInfo } from "../incidents/reminders";
import { apiFetch } from "../api/http";
import { PARTIAL_COVERAGE } from "../heartbeat/model";
import { toUnixMs } from "./types";
import { formatDuration } from "../format/format";

/** One uptime window as GET /api/v1/monitors/:id/uptime returns it. */
export type ApiUptimeWindow = {
  window: string;
  window_s: number;
  total: number;
  up: number;
  down: number;
  /** Null, not 0, when the window holds no checks at all. */
  uptime: number | null;
  warning?: number;
  maintenance?: number;
  legacy?: number;
  avg_latency_ms?: number;
};

/** One incident as GET /api/v1/monitors/:id/incidents returns it. */
export type ApiIncident = {
  id: number;
  monitor_id: number;
  started_at: string;
  confirmed_at?: string;
  resolved_at?: string;
  acked_at?: string;
  confirmed: boolean;
  resolved: boolean;
  acked: boolean;
  duration_s: number;
  cause?: string;
  last_error?: string;
  reminder_count?: unknown;
  reminded_at?: unknown;
  next_reminder_at?: unknown;
  reminder_status?: unknown;
};

export type UptimeWindow = {
  /** The window as requested, e.g. "24h". Used as the visible label. */
  window: string;
  windowS: number;
  total: number;
  up: number;
  down: number;
  /** Percentage 0-100, or null when nothing was checked in the window. */
  uptime: number | null;
  warning?: number;
  maintenance?: number;
  legacy?: number;
  avgLatencyMs: number | null;
};

export type Incident = {
  id: string;
  /**
   * Which monitor this incident belongs to.
   *
   * Carried even on the per-monitor list, where it is redundant, because the
   * all-monitors incidents screen reads the same shape and has to name the
   * service each row is about. A second type differing by one field would be
   * two translations of one API object, and the two would drift.
   */
  monitorId: string;
  /** Unix milliseconds, or null if unparseable. */
  startedAt: number | null;
  /**
   * When the failure count crossed the threshold and a human was told.
   *
   * Distinct from `startedAt` on purpose: the gap between "we saw it" and "we
   * alerted" is what support conversations turn on, and the server exposes
   * both for that reason (see internal/api/incidents.go).
   */
  confirmedAt: number | null;
  resolvedAt: number | null;
  /**
   * When somebody said "seen, working on it".
   *
   * **This is not a resolution time.** An acknowledged incident is still open;
   * acking stops the escalating repeat notifications and changes nothing about
   * whether the service is down. Any view that draws these two the same way is
   * telling a reader the outage is over when it is not.
   */
  ackedAt: number | null;
  confirmed: boolean;
  resolved: boolean;
  acked: boolean;
  /** Seconds to resolution, or so far when still open. */
  durationS: number;
  cause?: string;
  lastError?: string;
  reminder?: ReminderInfo | null;
};

export type MonitorDetail = {
  windows: UptimeWindow[];
  incidents: Incident[];
};

/**
 * How much incident history to ask for.
 *
 * Enough to cover a bad month without becoming a second screen inside a
 * screen. Past this the honest answer is a dedicated incidents view
 * (SUB-34), not a longer scroll here.
 */
export const INCIDENT_LIMIT = 20;

export const detailQueryKey = (id: string) => ["monitor-detail", id] as const;

function toNumber(value: number | null | undefined): number | null {
  return value === null || value === undefined || !Number.isFinite(value) ? null : value;
}

export function windowFromApi(api: ApiUptimeWindow): UptimeWindow {
  return {
    window: api.window,
    windowS: api.window_s,
    total: api.total,
    up: api.up,
    down: api.down,
    uptime: toNumber(api.uptime),
    warning: toNumber(api.warning) ?? 0,
    maintenance: toNumber(api.maintenance) ?? 0,
    legacy: toNumber(api.legacy) ?? 0,
    // Absent means zero measured latency, which for an average over real
    // checks means "no timings", so it collapses to unknown rather than 0.
    avgLatencyMs: toNumber(api.avg_latency_ms),
  };
}

export function incidentFromApi(api: ApiIncident): Incident {
  return {
    // Stringified for the same reason monitor ids are: these are React keys,
    // and a number that arrives as a string from some other endpoint would
    // silently become a second key for the same incident.
    id: String(api.id),
    // Stringified for the same reason: a monitor id is a string everywhere
    // else in this app, and a number here would fail every `===` against one.
    monitorId: String(api.monitor_id),
    startedAt: toUnixMs(api.started_at),
    confirmedAt: toUnixMs(api.confirmed_at),
    resolvedAt: toUnixMs(api.resolved_at),
    ackedAt: toUnixMs(api.acked_at),
    confirmed: api.confirmed,
    resolved: api.resolved,
    acked: api.acked,
    durationS: api.duration_s,
    cause: api.cause,
    lastError: api.last_error,
    reminder: reminderFromApi(api),
  };
}

async function getJSON<T>(url: string, what: string, signal?: AbortSignal): Promise<T> {
  const res = await apiFetch(url, { signal });
  if (!res.ok) {
    throw new Error(`could not load ${what}: HTTP ${res.status}`);
  }
  return (await res.json()) as T;
}

/**
 * Loads uptime windows and incident history for one monitor.
 *
 * The two requests go out together rather than in sequence: they do not
 * depend on each other, and waiting for the first before starting the second
 * would double the time the panel spends empty for no gain.
 */
export async function fetchMonitorDetail(id: string, signal?: AbortSignal): Promise<MonitorDetail> {
  const base = `/api/v1/monitors/${encodeURIComponent(id)}`;
  const [uptime, incidents] = await Promise.all([
    getJSON<{ windows?: ApiUptimeWindow[] }>(`${base}/uptime`, "uptime", signal),
    getJSON<{ incidents?: ApiIncident[] }>(
      `${base}/incidents?limit=${INCIDENT_LIMIT}`,
      "incidents",
      signal,
    ),
  ]);
  return {
    windows: (uptime.windows ?? []).map(windowFromApi),
    incidents: (incidents.incidents ?? []).map(incidentFromApi),
  };
}

/**
 * The caveat on an uptime window that is longer than the monitor has existed,
 * or undefined when the figure covers its whole window.
 *
 * This is what a warning value means on a monitor screen: the number is true,
 * but it is about less than its label claims. "100%" under "30d" for a
 * monitor added two days ago is two days of evidence presented as a month,
 * which reads as a record the monitor does not have. A threshold breach (slow
 * responses, a certificate close to expiry) is not a caveat of this kind: it
 * already fails the check and shows up as the monitor's status.
 *
 * The cut-off is the heartbeat bar's partial-column rule (DESIGN.md §8.5): a
 * window counts as covered from 90% of its span. Below that the caveat
 * appears; a monitor added 23 hours ago does not get one on its 24h figure,
 * because the missing hour cannot move the reading enough to mislead.
 *
 * Age, not a count of checks against the interval: the interval can have
 * changed during the window, and the stored history is what the figure is
 * computed from. Gaps from pausing are therefore not caught here.
 */
export function windowCoverageCaveat(
  w: Pick<UptimeWindow, "windowS" | "uptime">,
  createdAt: number | null | undefined,
  now: number,
): string | undefined {
  if (w.uptime === null) return undefined;
  if (createdAt === null || createdAt === undefined) return undefined;
  if (!Number.isFinite(w.windowS) || w.windowS <= 0) return undefined;
  const ageS = (now - createdAt) / 1000;
  if (!Number.isFinite(ageS) || ageS < 0) return undefined;
  if (ageS >= w.windowS * PARTIAL_COVERAGE) return undefined;
  const age = formatDuration(ageS);
  return `the monitor was added ${age} ago, so this covers ${age}, not the whole window`;
}

