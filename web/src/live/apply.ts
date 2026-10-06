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
import { confirmedOutage } from "../monitors/model";

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
 * that. A failed check therefore lowers a green row to `warning`, never
 * straight to red — otherwise the live view would call an outage several
 * checks before the server does, and the dashboard and the notifications would
 * disagree about reality.
 *
 * The same holds on the way back up. A passing check does not end a confirmed
 * outage: the server closes it only after the monitor's recovery threshold of
 * passes in a row, and says so with an `incident_resolved` status event. Until
 * then a red row stays red, so the live view never shows green while the
 * incident is still open and the all-clear has not gone out. A recovering row
 * stays recovering on a pass and drops back to down on a failure, which is
 * what the server's state engine does with the same check.
 *
 * A paused monitor keeps its status whatever arrives: pausing is a human
 * decision the stream knows nothing about.
 */
export function statusAfterHeartbeat(current: MonitorStatus, ok: boolean): MonitorStatus {
  if (current === "paused") return "paused";
  if (current === "down") return "down";
  if (current === "recovering") return ok ? "recovering" : "down";
  return ok ? "up" : "warning";
}

/**
 * The status one heartbeat frame implies for a monitor, and its streak.
 *
 * Three sources, in order of authority. A frame that carries `recovery` is the
 * server saying "recovering, n of m" and wins outright. A passing frame
 * against an open outage is ignored for status: its assessment is `up`
 * because the check is not downtime, not because the incident closed — that
 * news comes as `incident_resolved`. Everything else is the frame's own
 * assessment, falling back to `statusAfterHeartbeat` for older servers.
 */
function heartbeatStatus(m: Monitor, e: HeartbeatEvent): Pick<Monitor, "status" | "recovery"> {
  if (m.status === "paused") return { status: "paused" };
  if (e.recovery !== undefined) return { status: "recovering", recovery: e.recovery };
  // The server's word that the certificate notice is open; the assessment
  // beside it is `up`, because the check passed.
  if (e.expiring === true) return { status: "expiring" };
  if (e.ok && confirmedOutage(m.status)) return { status: m.status, recovery: m.recovery };
  return { status: e.assessment || statusAfterHeartbeat(m.status, e.ok) };
}

/**
 * The status a state-engine event implies, or null when it implies nothing.
 *
 * `notice` marks an event about a certificate notice: confirming one is
 * "expiring", not "down", and resolving one is "up" like any resolution.
 */
export function statusAfterEvent(event: string, notice = false): MonitorStatus | null {
  switch (event) {
    // The failure threshold was crossed. This, and only this, is "down".
    case "incident_confirmed":
      return notice ? "expiring" : "down";
    // Recovery: the incident closed, so the monitor is answering again.
    case "incident_resolved":
      return "up";
    // The first failure of a run. An incident row exists but is unconfirmed,
    // which is exactly what `warning` means on the API side too.
    case "incident_opened":
      return "warning";
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
    // The streak is dropped on every frame and put back only by a frame that
    // leaves the monitor recovering, so it cannot outlive its status.
    ...m,
    recovery: undefined,
    ...heartbeatStatus(m, e),
    maintenance: e.currentMaintenance ?? m.maintenance,
    latencyMs: e.latencyMs,
    lastCheck: e.at,
    // An expiring pass keeps the notice's text: it is the reason the row
    // is amber, and the pass did not change it.
    error: e.ok ? (e.expiring === true ? m.error : undefined) : e.error,
    // The kind belongs to this error, so it is replaced with it, never kept
    // from an earlier failure that said something else.
    failureKind: e.ok ? (e.expiring === true ? m.failureKind : undefined) : e.failureKind,
    beats: [...m.beats, {
      ts: e.at,
      ok: e.ok,
      assessment: e.assessment,
      maintenance: e.maintenance,
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
  const status = statusAfterEvent(e.event, e.notice === true);
  if (status === null) return monitors as Monitor[];
  return replace(monitors, e.monitorId, (m) => ({
    // Every status event ends a recovery streak: it either closes the
    // incident or opens or confirms one.
    ...m,
    recovery: undefined,
    // A paused monitor is not rescheduled, so an event about one is stale by
    // definition; keeping the pause is the safer of the two wrong answers.
    status: m.status === "paused" ? "paused" : status,
    error: status === "up" ? undefined : (e.error ?? m.error),
    // Only with its own error: a cause without one would relabel a message
    // it was not sent with.
    failureKind:
      status === "up" ? undefined : e.error !== undefined ? e.cause : m.failureKind,
  }));
}
