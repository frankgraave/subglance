import { apiFetch, apiJSON, apiRequest } from "../api/http";

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

export interface Retention {
  raw: RetentionWindow;
  rollup: RetentionWindow;
  minimum_raw_seconds: number;
  tables: RetentionTable[];
}

export interface RetentionImpact {
  heartbeats: number;
  hourly_buckets: number;
  incidents: number;
}

export const retentionKey = ["retention"] as const;

export async function fetchRetention(signal?: AbortSignal): Promise<Retention> {
  const data: unknown = await apiJSON<unknown>("/api/v1/settings/retention", { signal, cache: "no-store" });
  // A window misread as 0 would render as "forever" and could be saved back
  // that way, so anything off-shape is an error rather than a guess.
  if (!validRetention(data)) throw new Error("Retention settings unavailable.");
  return data;
}

const count = (value: unknown) => typeof value === "number" && Number.isFinite(value) && value >= 0;

function validWindow(value: unknown): value is RetentionWindow {
  if (!value || typeof value !== "object") return false;
  const w = value as Record<string, unknown>;
  return count(w.seconds) && ["default", "database", "pinned"].includes(w.source as string) &&
    (w.pinned_by === null || typeof w.pinned_by === "string") &&
    (w.set_aside_seconds === null || count(w.set_aside_seconds));
}

function validRetention(value: unknown): value is Retention {
  if (!value || typeof value !== "object") return false;
  const d = value as Record<string, unknown>;
  return validWindow(d.raw) && validWindow(d.rollup) && count(d.minimum_raw_seconds) && Array.isArray(d.tables) &&
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
 * Resolves to null when the save succeeded but the server could not read the windows
 * back (204), so the caller refetches instead of reporting a failure.
 */
export async function saveRetention(body: { raw_seconds?: number; rollup_seconds?: number }): Promise<Retention | null> {
  const res = await apiRequest("/api/v1/settings/retention", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (res.status === 204) return null;
  return (await res.json()) as Retention;
}
