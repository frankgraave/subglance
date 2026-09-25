import type { Retention } from "./api";

// A small instance at the defaults: 30 days raw, summaries forever.
export const defaultRetention: Retention = {
  raw: { seconds: 30 * 86_400, source: "default", pinned_by: null, set_aside_seconds: null },
  rollup: { seconds: 0, source: "default", pinned_by: null, set_aside_seconds: null },
  minimum_raw_seconds: 86_400,
  tables: [
    { name: "heartbeats", rows: 72_000, bytes: 5_400_000, rows_per_day: 14_400 },
    { name: "heartbeat_responses", rows: 12, bytes: 40_960, rows_per_day: 2 },
    { name: "heartbeat_hourly", rows: 8_760, bytes: 210_240, rows_per_day: 240 },
    { name: "incidents", rows: 40, bytes: 8_192, rows_per_day: 0.5 },
  ],
};
