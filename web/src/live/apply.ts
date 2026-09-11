/**
 * Applying live events to the monitor list. Pure, and the heart of SUB-26.
 *
 * Every function here takes the current list and one event and returns a new
 * list. No fetching, no timers, no React — which is what makes the awkward
 * questions ("does a failed check turn a row red?") answerable in a unit test
 * instead of by staring at a dashboard for ten minutes.
 */

import type { HeartbeatEvent, StatusEvent } from "./events";
import type { Monitor, MonitorStatus } from "../monitors/types";

/**
 * How many checks a live-updated monitor keeps.
 *
 * A tab left open for a week would otherwise grow one array per monitor
 * without bound. 100 is comfortably more than the widest heartbeat bar draws
 * and is the same number the initial fetch asks for, so the bar looks the same
 * after an hour of streaming as it did on load.
 */
export const MAX_LIVE_BEATS = 100;

/**
 * The status a single check result implies, given what we already believed.
 *
 * This deliberately mirrors `describeMonitor` in internal/api/monitors.go: a
 * heartbeat says what the last probe saw, it does not say whether the monitor
 * is *down*. Down is a confirmed incident, and only a `status` event reports
 * that. A failed check therefore lowers a green row to `pending`, never
 * straight to red — otherwise the live view would call an outage several
 * checks before the server does, and the dashboard and the notifications would
 * disagree about reality.
 *
 * A paused monitor keeps its status whatever arrives: pausing is a human
 * decision the stream knows nothing about.
 */
export function statusAfterHeartbeat(current: MonitorStatus, ok: boolean): MonitorStatus {
  if (current === "paused") return "paused";
  if (current === "down") return ok ? "up" : "down";
  return ok ? "up" : "pending";
}

/** The status a state-engine event implies, or null when it implies nothing. */
export function statusAfterEvent(event: string): MonitorStatus | null {
  switch (event) {
    // The failure threshold was crossed. This, and only this, is "down".
    case "incident_confirmed":
      return "down";
    // Recovery: the incident closed, so the monitor is answering again.
    case "incident_resolved":
      return "up";
    // The first failure of a run. An incident row exists but is unconfirmed,
    // which is exactly what `pending` means on the API side too.
    case "incident_opened":
      return "pending";
    default:
      return null;
  }
}

function replace(
  monitors: readonly Monitor[],
  id: string,
  update: (m: Monitor) => Monitor,
): Monitor[] {
  let hit = false;
  const next = monitors.map((m) => {
    if (m.id !== id) return m;
    hit = true;
    return update(m);
  });
  // Returning the original array when nothing matched keeps React from
  // re-rendering 200 rows because of an event about a monitor we do not show
  // (one that was just created, or deleted in another tab).
  return hit ? next : (monitors as Monitor[]);
}

/** Folds one completed check into the list. */
export function applyHeartbeat(monitors: readonly Monitor[], e: HeartbeatEvent): Monitor[] {
  return replace(monitors, e.monitorId, (m) => ({
    ...m,
    status: statusAfterHeartbeat(m.status, e.ok),
    latencyMs: e.latencyMs,
    lastCheck: e.at,
    error: e.ok ? undefined : e.error,
    beats: [...m.beats, {
      ts: e.at,
      ok: e.ok,
      latencyMs: e.latencyMs,
      statusCode: e.statusCode,
      error: e.error,
    }].slice(-MAX_LIVE_BEATS),
  }));
}

/**
 * Folds a state transition into the list.
 *
 * Uptime is deliberately *not* recalculated here. It is a 24h window the
 * server computes from every stored heartbeat, and a client that has seen
 * three minutes of stream cannot approximate it. A slightly stale percentage
 * is honest; a locally invented one is not.
 */
export function applyStatus(monitors: readonly Monitor[], e: StatusEvent): Monitor[] {
  const status = statusAfterEvent(e.event);
  if (status === null) return monitors as Monitor[];
  return replace(monitors, e.monitorId, (m) => ({
    ...m,
    // A paused monitor is not rescheduled, so an event about one is stale by
    // definition; keeping the pause is the safer of the two wrong answers.
    status: m.status === "paused" ? "paused" : status,
    error: status === "up" ? undefined : (e.error ?? m.error),
  }));
}
