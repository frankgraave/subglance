/**
 * The frontend's monitor model, and the translation from the API's JSON.
 *
 * The wire format and the render model are deliberately not the same type.
 * The API speaks snake_case, RFC3339 timestamps and an `enabled` flag; the
 * components want camelCase, unix milliseconds and a single `status` they can
 * switch on. Doing that conversion once, here, keeps every component free of
 * defensive parsing — and gives the awkward cases exactly one home.
 */

import type { Beat } from "../heartbeat/model";

export type { Beat };

/**
 * What the dashboard can show about a monitor.
 *
 * `paused` has no counterpart on the wire: the API reports a monitor's last
 * known check status regardless of whether the scheduler is still running it.
 * Collapsing `enabled: false` into a status is a presentation decision, and it
 * belongs on this side of the boundary — a paused monitor that last checked
 * green is not "up", it is "not being watched".
 */
export type MonitorStatus = "up" | "down" | "pending" | "paused";

export type Monitor = {
  id: string;
  name: string;
  status: MonitorStatus;
  /** What is being checked — a URL, a host:port. Shown, and searched. */
  target: string;
  /**
   * Latency of the most recent check, or null.
   *
   * Nullable rather than 0 because "we have no timing" and "it answered in
   * under a millisecond" are different facts, and a dashboard that renders the
   * first as `0 ms` is lying. The backend already draws this distinction;
   * flattening it here would throw it away at the last step.
   */
  latencyMs: number | null;
  /** Uptime over the last 24h as a percentage 0–100, or null when unknown. */
  uptime24h: number | null;
  /** Checks oldest first, ready to hand to HeartbeatBar. */
  beats: Beat[];
  /** Unix milliseconds of the last completed check, or null if never checked. */
  lastCheck: number | null;
  /** Failure reason for the last check, when there was one. */
  error?: string;
  /**
   * Key/value labels: `{ env: "prod", customer: "acme" }`.
   *
   * Always an object here, never undefined, so a component that groups or
   * filters can read `Object.entries(m.tags)` without a guard at every call
   * site. The API omits the field for untagged monitors; that absence is
   * resolved once, in `fromApi`.
   *
   * At most one value per key — the backend enforces it with a primary key,
   * so grouping by a key yields exactly one bucket per monitor.
   */
  tags: Record<string, string>;
};

/** One heartbeat as GET /api/v1/monitors?heartbeats=N returns it. */
export type ApiHeartbeat = {
  /** RFC3339, e.g. "2026-09-11T08:30:00Z". */
  ts: string;
  ok: boolean;
  latency_ms?: number | null;
  status_code?: number;
  error?: string;
};

/** One monitor as the API returns it. Optional fields really are absent. */
export type ApiMonitor = {
  /**
   * A JSON number in practice: the server's id is an int64 and encoding/json
   * writes it unquoted. Typed as either because the render model uses strings
   * and the difference must be resolved here rather than at a comparison
   * three components away.
   */
  id: string | number;
  name: string;
  type: string;
  target: string;
  interval_s: number;
  timeout_s: number;
  enabled: boolean;
  status: "up" | "pending" | "down";
  last_check?: string | null;
  latency_ms?: number | null;
  status_code?: number;
  error?: string;
  incident_id?: string;
  incident_since?: string;
  uptime_24h?: number | null;
  created_at: string;
  heartbeats?: ApiHeartbeat[];
  /**
   * Omitted entirely when the monitor has no tags, which is why this is
   * optional while the render model's `tags` is not.
   */
  tags?: Record<string, string>;
};

/**
 * RFC3339 to unix milliseconds.
 *
 * Returns null instead of NaN for anything unparseable. NaN propagates
 * silently through arithmetic and formatting and surfaces as "Invalid Date"
 * three components away from the cause; null is checked at the one place that
 * renders it.
 */
export function toUnixMs(value: string | null | undefined): number | null {
  if (value === null || value === undefined || value === "") return null;
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? null : ms;
}

/** Absent, null and non-finite all mean "no number", never 0. */
function toNumber(value: number | null | undefined): number | null {
  return value === null || value === undefined || !Number.isFinite(value) ? null : value;
}

function beatFromApi(hb: ApiHeartbeat): Beat {
  return {
    // A heartbeat with an unparseable timestamp still happened; 0 keeps it in
    // the series rather than dropping a check the user may need to see.
    ts: toUnixMs(hb.ts) ?? 0,
    ok: hb.ok,
    latencyMs: toNumber(hb.latency_ms),
    statusCode: hb.status_code,
    error: hb.error,
  };
}

/**
 * Keeps only string values from the tag object.
 *
 * The payload is JSON from a server this build does not control the version
 * of, and a non-string value would reach a component expecting to render it.
 * Dropping the entry is better than rendering `[object Object]` in a filter.
 */
function sanitiseTags(tags: Record<string, string> | null | undefined): Record<string, string> {
  if (!tags) return {};
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(tags)) {
    if (typeof value === "string" && value !== "") out[key] = value;
  }
  return out;
}

/** Translates one API monitor into the render model. Pure. */
export function fromApi(api: ApiMonitor): Monitor {
  return {
    // Stringified, always. The list endpoint sends a number and an SSE frame
    // sends the same id in `monitor_id`; a live update matching one against
    // the other silently finds nothing, so every heartbeat would be dropped
    // and the dashboard would sit frozen while claiming to be live.
    id: String(api.id),
    name: api.name,
    target: api.target,
    status: api.enabled ? api.status : "paused",
    latencyMs: toNumber(api.latency_ms),
    uptime24h: toNumber(api.uptime_24h),
    // Absent `heartbeats` (the caller omitted ?heartbeats=N) and an empty
    // array both render as "no history", so they collapse to the same thing.
    beats: (api.heartbeats ?? []).map(beatFromApi),
    lastCheck: toUnixMs(api.last_check),
    error: api.error,
    // Absent and empty both mean "no tags", so they collapse to one shape
    // and no consumer needs a null check.
    tags: sanitiseTags(api.tags),
  };
}

/** Translates a whole `{ monitors: [...] }` payload. */
export function monitorsFromApi(payload: { monitors?: ApiMonitor[] } | null | undefined): Monitor[] {
  return (payload?.monitors ?? []).map(fromApi);
}
