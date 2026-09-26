import type { Diagnostics } from "./api";

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let v = n / 1024, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86_400), h = Math.floor(seconds % 86_400 / 3_600), m = Math.floor(seconds % 3_600 / 60);
  return d > 0 ? `${d} d ${h} h ${m} m` : h > 0 ? `${h} h ${m} m` : `${m} m`;
}

/**
 * The text "Copy diagnostics" puts on the clipboard, meant for a public bug
 * report. The database path is left out on purpose: it often carries a home
 * directory or a user name, and nothing in a bug report needs it.
 *
 * `staleSince` is the time of the last successful reading when the card is
 * showing it as stale. The pasted text then says so on its first line, so an
 * old queue depth cannot read as the current one in a bug report.
 */
export function diagnosticsText(d: Diagnostics, staleSince?: Date): string {
  const s = d.scheduler;
  return [
    ...(staleSince ? [`stale: last successful reading at ${staleSince.toISOString()}; the latest refresh failed or is overdue`] : []),
    `subglance ${d.version}${d.commit ? ` (${d.commit})` : ""}`,
    `runtime ${d.go_version} ${d.platform}`,
    `uptime ${formatUptime(d.uptime_seconds)} (started ${d.started_at})`,
    `database ${formatBytes(d.database.bytes)}, wal ${formatBytes(d.database.wal_bytes)}, journal ${d.database.journal_mode}`,
    s === null ? "scheduler not attached"
      : `workers ${s.busy}/${s.workers} busy, queue ${s.queue_depth}, monitors ${s.scheduled}, skipped ${s.skipped_checks}, recorded ${s.checks_recorded}, write failures ${s.heartbeat_write_failures}`,
  ].join("\n");
}
