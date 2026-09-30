import { apiFetch, apiPost, apiRequest } from "../api/http";

/** One retention window as the server reports it. `seconds: 0` is forever. */
export interface RetentionWindow {
  seconds: number;
  source: "default" | "database" | "pinned";
  pinned_by: string | null;
  set_aside_seconds: number | null;
}

export interface RetentionTable {
  name: "heartbeats" | "heartbeat_responses" | "heartbeat_hourly" | "incidents";
  rows: number;
  bytes: number | null;
  rows_per_day: number;
}

type Source = "default" | "database" | "pinned";

/** What the size limit removed beyond the windows on one pass. */
export interface SizeCap {
  limit_bytes: number;
  before_bytes: number;
  after_bytes: number;
  heartbeats: number;
  hourly_buckets: number;
  raw_since: string | null;
  hourly_since: string | null;
  at_floor: boolean;
}

/** One maintenance pass, as recorded in the database. */
export interface RetentionPass {
  started_at: string;
  duration_ms: number;
  trigger: "schedule" | "startup" | "manual";
  heartbeats: number;
  hourly_buckets: number;
  incidents: number;
  deliveries: number;
  freed_bytes: number;
  size_cap: SizeCap | null;
  error: string | null;
}

/** Whether a full VACUUM would help, and whether the disk could take it. */
export interface CompactPlan {
  size_bytes: number;
  free_bytes: number;
  auto_vacuum: "none" | "full" | "incremental";
  recommended: boolean;
  estimate_seconds: number;
  disk_shortfall: { need_bytes: number; free_bytes: number } | null;
  running: boolean;
  last: {
    finished_at: string; duration_ms: number; before_bytes: number; after_bytes: number;
    shrink_pending: boolean; error: string | null;
  } | null;
}

export interface Retention {
  raw: RetentionWindow;
  rollup: RetentionWindow;
  minimum_raw_seconds: number;
  run_at: { value: string; source: Source; pinned_by: string | null };
  max_database_size: { bytes: number; source: Source; pinned_by: string | null; minimum_bytes: number };
  running: boolean;
  last_pass: RetentionPass | null;
  compact: CompactPlan | null;
  tables: RetentionTable[];
  /**
   * The ETag that came with these windows, sent back as If-Match so a save
   * cannot overwrite a change it never saw. Client-side only; null when the
   * response carried none.
   */
  etag?: string | null;
}

export interface RetentionImpact {
  heartbeats: number;
  hourly_buckets: number;
  incidents: number;
}

export const retentionKey = ["retention"] as const;

export async function fetchRetention(signal?: AbortSignal): Promise<Retention> {
  return readRetention(await apiRequest("/api/v1/settings/retention", { signal, cache: "no-store" }));
}

async function readRetention(res: Response): Promise<Retention> {
  const data: unknown = await res.json();
  // A window misread as 0 would render as "forever" and could be saved back
  // that way, so anything off-shape is an error rather than a guess.
  if (!validRetention(data)) throw new Error("Retention settings unavailable.");
  // The reports are read-only, so one the page cannot read is left out
  // rather than failing the card that holds the settings.
  return {
    ...data,
    last_pass: validPass(data.last_pass) ? data.last_pass : null,
    compact: validPlan(data.compact) ? data.compact : null,
    etag: res.headers.get("ETag"),
  };
}

const count = (value: unknown) => typeof value === "number" && Number.isFinite(value) && value >= 0;
const record = (value: unknown) => (value && typeof value === "object" ? value as Record<string, unknown> : null);
const text = (value: unknown) => value === null || typeof value === "string";

/** The fields every setting shares: where its value came from, and what pinned it. */
function validSource(w: Record<string, unknown>): boolean {
  return ["default", "database", "pinned"].includes(w.source as string) && text(w.pinned_by);
}

function validWindow(value: unknown): value is RetentionWindow {
  const w = record(value);
  return !!w && count(w.seconds) && validSource(w) && (w.set_aside_seconds === null || count(w.set_aside_seconds));
}

function validPass(value: unknown): value is RetentionPass {
  const p = record(value);
  const cap = p && record(p.size_cap);
  return !!p && typeof p.started_at === "string" && ["schedule", "startup", "manual"].includes(p.trigger as string) &&
    ["duration_ms", "heartbeats", "hourly_buckets", "incidents", "deliveries", "freed_bytes"].every((key) => count(p[key])) &&
    text(p.error) && (p.size_cap === null ||
      (!!cap && count(cap.limit_bytes) && count(cap.heartbeats) && count(cap.hourly_buckets) && text(cap.raw_since) && text(cap.hourly_since)));
}

function validPlan(value: unknown): value is CompactPlan {
  const c = record(value);
  const last = c && record(c.last);
  return !!c && count(c.size_bytes) && count(c.free_bytes) && count(c.estimate_seconds) &&
    typeof c.recommended === "boolean" && typeof c.running === "boolean" &&
    (c.disk_shortfall === null || (!!record(c.disk_shortfall))) &&
    (c.last === null || (!!last && typeof last.finished_at === "string" && count(last.before_bytes) && count(last.after_bytes) && text(last.error)));
}

function validRetention(value: unknown): value is Retention {
  const d = record(value);
  const runAt = d && record(d.run_at);
  const size = d && record(d.max_database_size);
  return !!d && validWindow(d.raw) && validWindow(d.rollup) && count(d.minimum_raw_seconds) &&
    !!runAt && typeof runAt.value === "string" && /^\d\d:\d\d$/.test(runAt.value) && validSource(runAt) &&
    !!size && count(size.bytes) && count(size.minimum_bytes) && validSource(size) &&
    typeof d.running === "boolean" && Array.isArray(d.tables) &&
    d.tables.every((t: unknown) => {
      const row = t as Record<string, unknown> | null;
      return !!row && ["heartbeats", "heartbeat_responses", "heartbeat_hourly", "incidents"].includes(row.name as string) &&
        count(row.rows) && (row.bytes === null || count(row.bytes)) && count(row.rows_per_day);
    });
}

/** Rows the next pass would remove under the proposed windows, without saving them. */
export async function previewRetention(raw: number, rollup: number, signal?: AbortSignal): Promise<RetentionImpact> {
  const res = await apiFetch(`/api/v1/settings/retention/preview?raw_seconds=${raw}&rollup_seconds=${rollup}`, { signal, cache: "no-store" });
  if (!res.ok) throw new Error("Preview unavailable.");
  return (await res.json()) as RetentionImpact;
}

/**
 * Saves only the windows that are not pinned; a pinned one would be refused with a 409.
 * Conditional on `etag`: if anyone saved since it was read, the server answers 412
 * (an `ApiError`) and writes nothing. Resolves to null when the save succeeded but
 * the server could not read the windows back (204), so the caller refetches instead
 * of reporting a failure.
 */
export type RetentionChange = { raw_seconds?: number; rollup_seconds?: number; run_at?: string; max_database_bytes?: number };

export async function saveRetention(body: RetentionChange, etag: string): Promise<Retention | null> {
  const res = await apiRequest("/api/v1/settings/retention", {
    method: "PUT",
    headers: { "Content-Type": "application/json", "If-Match": etag },
    body: JSON.stringify(body),
  });
  return res.status === 204 ? null : readRetention(res);
}

/**
 * Starts the daily pass now (202), or compacts the database. Both run in the
 * background; their outcome is read back from the settings. A refusal (409
 * already running, 503 no scheduler, 507 no room for the copy) is an `ApiError`.
 */
export async function startRetentionAction(action: "run" | "compact"): Promise<void> {
  await apiPost(`/api/v1/settings/retention/${action}`, {});
}
