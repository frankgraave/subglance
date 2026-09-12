/**
 * The wire shape of GET /api/v1/stream, and the translation into something the
 * dashboard can apply.
 *
 * Same split as `monitors/types.ts`: the server speaks snake_case with RFC3339
 * timestamps, the components want a normalised object. Doing it once here
 * means no component ever parses a frame.
 *
 * Frames are parsed defensively. This is the one input that arrives while the
 * page is already live, from a server that may be a different version than the
 * bundle (the binary embeds the UI, but a browser tab can outlive a redeploy).
 * An unrecognised frame is dropped, never thrown: an exception inside an
 * EventSource listener kills the whole subscription, so a single bad frame
 * would silently stop all live updates.
 */

import { toUnixMs } from "../monitors/types";

/** `hello`, the first frame of every stream. */
export type HelloEvent = {
  kind: "hello";
  seq: number;
  /** True when the server knows this client missed events while away. */
  gap: boolean;
  missed: number;
  /**
   * How often this server promises to send a `ping`, in milliseconds, or null
   * when it did not say — an older server, or one behind a proxy that rewrote
   * the frame. The client sizes its silence watchdog from this instead of
   * hardcoding a number that a change to the server's interval would silently
   * invalidate.
   */
  pingIntervalMs: number | null;
};

/**
 * `ping`: the keepalive, and the only frame that says "still here" when
 * nothing is happening. Carries no state; its arrival is the entire payload.
 */
export type PingEvent = { kind: "ping" };

/** `lagged`: the server dropped events because this client could not keep up. */
export type LaggedEvent = { kind: "lagged"; dropped: number };

/** One completed check. The high-volume frame: one per monitor per interval. */
export type HeartbeatEvent = {
  kind: "heartbeat";
  monitorId: string;
  at: number;
  ok: boolean;
  latencyMs: number | null;
  statusCode?: number;
  error?: string;
};

/** A monitor changing state. Rare, and the only frame worth announcing. */
export type StatusEvent = {
  kind: "status";
  monitorId: string;
  at: number;
  /** The state engine's event name, e.g. "incident_confirmed". */
  event: string;
  error?: string;
};

export type LiveEvent = HelloEvent | PingEvent | LaggedEvent | HeartbeatEvent | StatusEvent;

/**
 * Monitor ids are strings on this side of the boundary, always.
 *
 * The API serialises an int64 as a JSON number, while an SSE frame carries the
 * same id in `monitor_id`. Matching a number against a number would work right
 * up until one of the two paths stringifies it, and then every heartbeat would
 * silently fail to find its row. Normalising both to a string at the edge
 * removes the class of bug rather than the instance.
 */
export function monitorKey(id: string | number | null | undefined): string {
  return id === null || id === undefined ? "" : String(id);
}

function num(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function str(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}

/**
 * Parses one frame's `data` payload for a named event type.
 *
 * Returns null for anything it does not recognise or cannot use, including a
 * heartbeat or status frame without a monitor id — such a frame cannot be
 * applied to any row, and guessing would corrupt one.
 */
export function parseEvent(type: string, data: string): LiveEvent | null {
  let raw: unknown;
  try {
    raw = JSON.parse(data);
  } catch {
    return null;
  }
  if (typeof raw !== "object" || raw === null) return null;
  const body = raw as Record<string, unknown>;
  const payload = (body.data ?? {}) as Record<string, unknown>;

  switch (type) {
    case "hello":
      return {
        kind: "hello",
        seq: num(body.seq) ?? 0,
        gap: body.gap === true,
        missed: num(body.missed) ?? 0,
        pingIntervalMs: num(body.ping_interval_ms),
      };
    case "ping":
      return { kind: "ping" };
    case "lagged":
      return { kind: "lagged", dropped: num(body.dropped) ?? 0 };
    case "heartbeat": {
      const monitorId = monitorKey(body.monitor_id as string | number);
      if (monitorId === "") return null;
      return {
        kind: "heartbeat",
        monitorId,
        at: toUnixMs(str(body.at)) ?? Date.now(),
        ok: payload.ok === true,
        latencyMs: num(payload.latency_ms),
        statusCode: num(payload.status_code) ?? undefined,
        error: str(payload.error),
      };
    }
    case "status": {
      const monitorId = monitorKey(body.monitor_id as string | number);
      if (monitorId === "") return null;
      return {
        kind: "status",
        monitorId,
        at: toUnixMs(str(body.at)) ?? Date.now(),
        event: str(payload.event) ?? "",
        error: str(payload.error),
      };
    }
    default:
      return null;
  }
}
