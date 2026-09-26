import type { Diagnostics } from "./api";

// A steady instance: nothing queued, nothing failing.
export const steadyDiagnostics: Diagnostics = {
  version: "0.1.0", commit: "a17f3c9", go_version: "go1.25.1", platform: "linux/amd64",
  started_at: "2026-09-25T06:41:00Z", uptime_seconds: 3_600,
  database: { path: "/var/lib/subglance/subglance.db", bytes: 25_165_824, wal_bytes: 4_194_304, journal_mode: "wal" },
  scheduler: { workers: 16, busy: 2, queue_depth: 0, scheduled: 62, skipped_checks: 0, checks_recorded: 184_220, heartbeat_write_failures: 0 },
};
