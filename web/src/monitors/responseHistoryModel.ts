import type { ApiHeartbeat } from "./types";

/** Raw detail-only history; never added to the live dashboard beat cache. */
export type ResponseHeartbeat = ApiHeartbeat & {
  /** Persisted heartbeat ID as a decimal string; timestamps have second precision. */
  id: string;
  response?: {
    body: string;
    headers?: Record<string, string>;
    truncated?: boolean;
  } | null;
  /** Absent on old rows or when no historical decision was recorded. */
  response_capture_reason?: "disabled" | "flapping" | "budget" | null;
};

/**
 * A run of consecutive failed checks that say the same thing.
 *
 * Newest first, as the API orders them. A monitor that has been down for
 * forty minutes answers forty checks with one sentence, and printing that
 * sentence forty times pushed everything under it six thousand pixels down
 * the page (SUB-184). The run is told once, with its count and its span.
 */
export type FailureRun = {
  /** Stable across refreshes while any member stays on screen; see below. */
  key: string;
  beats: ResponseHeartbeat[];
};

/**
 * What makes two failures "the same" for grouping: everything the row prints
 * above its captured responses. Any difference in assessment, maintenance,
 * kind, status code or error text starts a new run, so a grouped row never
 * hides a detail one of its members would have shown on its own line. The
 * capture decision is not part of it: it is per check, and the run lists it
 * per check.
 */
const sameFailure = (a: ResponseHeartbeat, b: ResponseHeartbeat) =>
  a.assessment === b.assessment &&
  Boolean(a.maintenance) === Boolean(b.maintenance) &&
  a.failure_kind === b.failure_kind &&
  a.status_code === b.status_code &&
  a.error === b.error;

/**
 * Folds consecutive identical failures into runs. A passed check between two
 * identical failures ends the run: those are two outages, and one row for
 * both would claim the service never recovered in between.
 *
 * **The key is carried, not derived.** A run grows at its newest end with
 * every failed check and, once an outage is longer than the hundred checks
 * the history holds, loses its oldest member on the same poll. No member is
 * on screen for the whole outage, so a key read off either end would change
 * every minute, and the row would remount and close whichever response
 * somebody had open. Instead a run keeps the key any of its members had on
 * the previous render — `previous` maps each heartbeat ID to its run's key —
 * and only a run with no member seen before takes its oldest member's ID.
 * Returns the map for the next render beside the runs.
 */
export function failureRuns(
  heartbeats: readonly ResponseHeartbeat[],
  previous: ReadonlyMap<string, string> = new Map(),
): { runs: FailureRun[]; keys: Map<string, string> } {
  const groups: ResponseHeartbeat[][] = [];
  let current: ResponseHeartbeat[] | null = null;
  for (const hb of heartbeats) {
    if (hb.ok) {
      current = null;
    } else if (current !== null && sameFailure(current[0], hb)) {
      current.push(hb);
    } else {
      current = [hb];
      groups.push(current);
    }
  }
  const used = new Set<string>();
  const keys = new Map<string, string>();
  const runs = groups.map((beats) => {
    const carried = beats.map((hb) => previous.get(hb.id)).find((k) => k !== undefined && !used.has(k));
    const key = carried ?? beats[beats.length - 1].id;
    used.add(key);
    for (const hb of beats) keys.set(hb.id, key);
    return { key, beats };
  });
  return { runs, keys };
}
