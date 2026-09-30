import type { Retention } from "./api";

// A small instance at the defaults: 30 days raw, summaries forever, no size
// limit, the pass at 03:30, and last night's pass on record.
export const defaultRetention: Retention = {
  raw: { seconds: 30 * 86_400, source: "default", pinned_by: null, set_aside_seconds: null },
  rollup: { seconds: 0, source: "default", pinned_by: null, set_aside_seconds: null },
  minimum_raw_seconds: 86_400,
  run_at: { value: "03:30", source: "default", pinned_by: null },
  max_database_size: { bytes: 0, source: "default", pinned_by: null, minimum_bytes: 33_554_432 },
  running: false,
  last_pass: {
    started_at: "2026-09-30T03:30:00Z", duration_ms: 1_840, trigger: "schedule",
    heartbeats: 14_400, hourly_buckets: 0, incidents: 0, deliveries: 3, freed_bytes: 1_081_344,
    size_cap: null, error: null,
  },
  compact: {
    size_bytes: 6_553_600, free_bytes: 131_072, auto_vacuum: "incremental", recommended: false,
    estimate_seconds: 1, disk_shortfall: null, running: false, last: null,
  },
  tables: [
    { name: "heartbeats", rows: 72_000, bytes: 5_400_000, rows_per_day: 14_400 },
    { name: "heartbeat_responses", rows: 12, bytes: 40_960, rows_per_day: 2 },
    { name: "heartbeat_hourly", rows: 8_760, bytes: 210_240, rows_per_day: 240 },
    { name: "incidents", rows: 40, bytes: 8_192, rows_per_day: 0.5 },
  ],
};
