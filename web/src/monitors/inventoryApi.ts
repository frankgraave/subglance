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
import { inventoryFromApi, inventoryFromPayload } from "./inventory";
import type { InventoryMonitor } from "./inventory";
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
  /*
   * A payload with no `monitors` array is a failure, not an empty instance.
   *
   * `inventoryFromPayload` would turn `null`, a missing key or an unexpected
   * shape into `[]`, and the screen would then say "Nothing is being watched
   * yet" to somebody with forty monitors. That claim is the most alarming
   * wrong thing this page can make, so it is never made on the strength of a
   * body we could not read.
   */
  if (!Array.isArray(body?.monitors)) {
    throw new Error(
      "could not load monitors: the server's reply had no monitor list in it",
    );
  }
  return inventoryFromPayload(body);
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

/** One monitor and the version stamp that was true of it at that moment. */
export type VersionedMonitor = {
  monitor: InventoryMonitor;
  etag: string | null;
};

/**
 * Reads one monitor together with the ETag that versions it.
 *
 * The list endpoint sends no ETag — only `GET /api/v1/monitors/{id}` does — so
 * opening the edit drawer costs one request. Both halves of that response are
 * returned, and the form is filled from *this* monitor rather than from the
 * list row that was clicked, because the two have to describe the same moment:
 * showing values read a minute ago while sending a validator read just now
 * would let a conditional PATCH sail through and overwrite a change the user
 * never saw. The ETag is only a promise about the values it came with.
 */
export async function fetchMonitorForEdit(
  id: string,
  signal?: AbortSignal,
): Promise<VersionedMonitor> {
  const res = await apiRequest(`/api/v1/monitors/${encodeURIComponent(id)}`, {
    signal,
  });
  const body = (await res.json()) as ApiMonitor;
  return {
    monitor: inventoryFromApi(body as Parameters<typeof inventoryFromApi>[0]),
    etag: res.headers.get("ETag"),
  };
}
