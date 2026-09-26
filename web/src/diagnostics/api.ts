import { apiFetch } from "../api/http";

export interface Diagnostics {
  version: string;
  commit: string;
  go_version: string;
  platform: string;
  started_at: string;
  uptime_seconds: number;
  database: { path: string; bytes: number; wal_bytes: number; journal_mode: string };
  /** Null when no check pipeline is attached: unknown, not idle. */
  scheduler: {
    workers: number; busy: number; queue_depth: number; scheduled: number;
    skipped_checks: number; checks_recorded: number; heartbeat_write_failures: number;
  } | null;
}

export const diagnosticsKey = ["diagnostics"] as const;

export async function fetchDiagnostics(signal?: AbortSignal): Promise<Diagnostics> {
  const response = await apiFetch("/api/v1/diagnostics", { signal, cache: "no-store" });
  if (!response.ok) throw new Error("Diagnostics unavailable.");
  const data: unknown = await response.json();
  // A body of the wrong shape would render as zeros, and a zero on this card
  // is the healthy reading. Refusing it is the only honest option.
  if (!valid(data)) throw new Error("Diagnostics unavailable.");
  return data;
}

const count = (v: unknown) => typeof v === "number" && Number.isInteger(v) && v >= 0;
const text = (v: unknown) => typeof v === "string";
const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v);

function valid(d: unknown): d is Diagnostics {
  if (!record(d) || !record(d.database)) return false;
  if (![d.version, d.commit, d.go_version, d.platform, d.database.path, d.database.journal_mode].every(text)) return false;
  if (!text(d.started_at) || !Number.isFinite(Date.parse(d.started_at as string))) return false;
  if (![d.uptime_seconds, d.database.bytes, d.database.wal_bytes].every(count)) return false;
  if (d.scheduler === null) return true;
  const s = d.scheduler;
  return record(s) && [s.workers, s.busy, s.queue_depth, s.scheduled, s.skipped_checks,
    s.checks_recorded, s.heartbeat_write_failures].every(count);
}
