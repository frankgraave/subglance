/**
 * The requests the monitors inventory makes.
 *
 * Apart from the components for the same reason `incidents/api.ts` is: the
 * screen renders a model, it does not know what a fetch is, so a test can
 * drive every state from a fixture. Everything here goes through `apiRequest`
 * or `apiFetch`, so an expired session still reaches the session owner in one
 * place rather than surfacing as "HTTP 401" on a row.
 */

import { apiFetch, apiRequest } from "../api/http";
import { inventoryFromPayload } from "./inventory";
import type { ChannelState, InventoryMonitor } from "./inventory";
import type { ApiMonitor } from "./types";

export const inventoryQueryKey = ["monitors", "inventory"] as const;

/**
 * The inventory list.
 *
 * Deliberately without `?heartbeats=`: this screen draws no beat bar, and
 * asking for a hundred checks per monitor to render columns that do not use
 * them would make the management page the most expensive request in the app.
 */
export async function fetchInventory(
  signal?: AbortSignal,
): Promise<InventoryMonitor[]> {
  const res = await apiFetch("/api/v1/monitors", { signal });
  if (!res.ok) {
    throw new Error(`could not load monitors: HTTP ${res.status}`);
  }
  const body = (await res.json()) as { monitors?: ApiMonitor[] };
  return inventoryFromPayload(body);
}

/**
 * How many monitors the page will ask for channel attachments.
 *
 * **The cap exists because the backend has no endpoint for this question.**
 * Channels are a per-monitor sub-resource (`GET /api/v1/monitors/{id}/channels`),
 * so "which monitors have no channel attached" — the finding this column is
 * for — costs one request per monitor. At 12 monitors that is nothing; at 200
 * it is 200 requests to fill a column.
 *
 * So the fan-out stops, and the rows it did not reach say `not loaded` rather
 * than `none`. The real fix is the list endpoint carrying channel ids, which
 * is a backend ticket rather than something to paper over here.
 */
export const CHANNEL_FANOUT_LIMIT = 40;

export const channelsQueryKey = (ids: readonly string[]) =>
  ["monitors", "channels", ids.join(",")] as const;

export type ChannelMap = {
  /** Per monitor id. A missing id means the request never happened. */
  byMonitor: Readonly<Record<string, ChannelState>>;
  /** True when the fan-out was capped or a request failed. */
  truncated: boolean;
};

/**
 * Which channels each monitor alerts through.
 *
 * A monitor whose request fails contributes `{ known: false }` rather than an
 * empty list. That distinction is the whole reason this returns a map of
 * states instead of a map of arrays: an empty list is the finding "nobody will
 * hear about this monitor", and a failed request that rendered as one would
 * manufacture that finding out of a 500.
 */
export async function fetchMonitorChannels(
  monitorIds: readonly string[],
  signal?: AbortSignal,
): Promise<ChannelMap> {
  const ids = monitorIds.slice(0, CHANNEL_FANOUT_LIMIT);
  let incomplete = monitorIds.length > ids.length;
  const byMonitor: Record<string, ChannelState> = {};

  await Promise.all(
    ids.map(async (id) => {
      try {
        const res = await apiFetch(
          `/api/v1/monitors/${encodeURIComponent(id)}/channels`,
          { signal },
        );
        if (!res.ok) {
          incomplete = true;
          byMonitor[id] = { known: false };
          return;
        }
        const body = (await res.json()) as {
          channels?: { name?: string }[];
        };
        byMonitor[id] = {
          known: true,
          names: (body.channels ?? []).map(
            (channel) => channel.name ?? "unnamed",
          ),
        };
      } catch (err) {
        /*
         * A cancelled request is not a failure: React Query aborts the
         * previous fetch on every refetch, so swallowing an abort here would
         * mark almost every load incomplete. Rethrowing lets the query layer
         * recognise its own cancellation.
         */
        if (err instanceof DOMException && err.name === "AbortError") throw err;
        incomplete = true;
        byMonitor[id] = { known: false };
      }
    }),
  );

  return { byMonitor, truncated: incomplete };
}

/**
 * Pauses or resumes one monitor.
 *
 * Two endpoints rather than `PATCH {"enabled": …}` because that is what the
 * server offers as the intentional verb, and because the PATCH path carries
 * the whole optimistic-concurrency apparatus for a change that has no
 * conflict to lose.
 */
export async function setMonitorPaused(
  id: string,
  paused: boolean,
  signal?: AbortSignal,
): Promise<void> {
  await apiRequest(
    `/api/v1/monitors/${encodeURIComponent(id)}/${paused ? "pause" : "resume"}`,
    { method: "POST", signal },
  );
}

/** Deletes one monitor. The server answers 204 and there is nothing to read. */
export async function deleteMonitor(
  id: string,
  signal?: AbortSignal,
): Promise<void> {
  await apiRequest(`/api/v1/monitors/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal,
  });
}

/** What one manual check reported. */
export type CheckOutcome = {
  ok: boolean;
  latencyMs: number;
  statusCode?: number;
  error?: string;
  /**
   * Whether the result became part of the monitor's history.
   *
   * False for a paused monitor, and the row says so. A green check that did
   * not move the dashboard otherwise looks like a bug in the dashboard, which
   * is the one impression a monitoring tool cannot afford to give.
   */
  recorded: boolean;
};

/** Runs one check now. */
export async function checkMonitorNow(
  id: string,
  signal?: AbortSignal,
): Promise<CheckOutcome> {
  const res = await apiRequest(
    `/api/v1/monitors/${encodeURIComponent(id)}/check`,
    { method: "POST", signal },
  );
  const body = (await res.json()) as {
    ok?: boolean;
    latency_ms?: number;
    status_code?: number;
    error?: string;
    recorded?: boolean;
  };
  return {
    ok: body.ok === true,
    latencyMs: body.latency_ms ?? 0,
    ...(body.status_code !== undefined ? { statusCode: body.status_code } : {}),
    ...(body.error !== undefined && body.error !== ""
      ? { error: body.error }
      : {}),
    // Absent is read as "not recorded" rather than as "recorded": claiming a
    // check counted when the server did not say so is the direction that puts
    // a phantom measurement into someone's uptime.
    recorded: body.recorded === true,
  };
}

/** The fields the edit drawer may change. All optional; omitted is untouched. */
export type MonitorPatch = {
  name?: string;
  interval_s?: number;
  timeout_s?: number;
  tags?: Record<string, string>;
  capture_response?: boolean;
  repeat_after_s?: number;
};

/**
 * Applies an edit.
 *
 * The `If-Match` header is sent whenever the caller has a version, which makes
 * the write conditional on nobody else having changed the monitor since it was
 * read. Without it the endpoint is last-write-wins, and two people tidying the
 * inventory on a Friday afternoon is exactly the situation this page creates.
 * A 412 comes back as an `ApiError` with the server's own sentence, which
 * tells the user to re-open the row rather than silently discarding their
 * edit or somebody else's.
 */
export async function patchMonitor(
  id: string,
  patch: MonitorPatch,
  version?: string | null,
  signal?: AbortSignal,
): Promise<void> {
  await apiRequest(`/api/v1/monitors/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: {
      "Content-Type": "application/json",
      ...(version !== undefined && version !== null && version !== ""
        ? { "If-Match": version }
        : {}),
    },
    body: JSON.stringify(patch),
    signal,
  });
}

/**
 * Reads one monitor and the ETag that versions it.
 *
 * The list endpoint sends no ETag — only `GET /api/v1/monitors/{id}` does — so
 * opening the edit drawer costs one request. That request is what makes the
 * conditional PATCH above possible: without a validator read at the moment the
 * form was filled, "nothing changed underneath me" is a claim with nothing
 * behind it.
 */
export async function fetchMonitorVersion(
  id: string,
  signal?: AbortSignal,
): Promise<string | null> {
  const res = await apiRequest(`/api/v1/monitors/${encodeURIComponent(id)}`, {
    signal,
  });
  return res.headers.get("ETag");
}
