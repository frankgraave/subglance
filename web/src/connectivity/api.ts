import { apiFetch } from "../api/http";

/**
 * GET /api/v1/connectivity: whether the server can reach its connectivity
 * targets (SUB-151).
 *
 * While it cannot, every check that fails on a network error is held as a
 * warning with the cause `local_network` rather than confirmed as an outage.
 * This is what lets the dashboard say that once, above the list, instead of
 * leaving twenty amber rows to read as twenty separate problems.
 */
export interface ConnectivityState {
  /** False when the operator turned the check off. */
  enabled: boolean;
  offline: boolean;
  /** Epoch milliseconds; null while online. */
  offlineSince: number | null;
}

export const connectivityKey = ["connectivity"] as const;

export async function fetchConnectivity(signal?: AbortSignal): Promise<ConnectivityState> {
  const response = await apiFetch("/api/v1/connectivity", { signal, cache: "no-store" });
  if (!response.ok) throw new Error("Connectivity state unavailable.");
  const state = parseConnectivity(await response.json());
  if (state === null) throw new Error("Connectivity state unavailable.");
  return state;
}

/**
 * Validates the wire shape, returning null for anything it does not describe.
 *
 * Strict on purpose, in the one direction that matters: the banner claims the
 * server has lost its own connection, so a body that does not say exactly that
 * must never produce it. An `offline: true` without a parseable time, or on a
 * check that is switched off, is treated as unknown rather than guessed at.
 */
export function parseConnectivity(value: unknown): ConnectivityState | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const d = value as Record<string, unknown>;
  if (typeof d.enabled !== "boolean" || typeof d.offline !== "boolean") return null;
  if (!d.offline) {
    return d.offline_since === null ? { enabled: d.enabled, offline: false, offlineSince: null } : null;
  }
  if (!d.enabled || typeof d.offline_since !== "string") return null;
  const since = Date.parse(d.offline_since);
  if (!Number.isFinite(since)) return null;
  return { enabled: true, offline: true, offlineSince: since };
}
