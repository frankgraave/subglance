import { apiFetch } from "../api/http";

/**
 * GET /api/v1/connectivity: whether the server can reach its connectivity
 * targets (SUB-151), and whether its DNS resolver answers.
 *
 * While it cannot, every check that fails on a network error is held as a
 * warning with the cause `local_network` (or `local_dns`, when only the
 * resolver is silent) rather than confirmed as an outage. This is what lets
 * the dashboard say that once, above the list, instead of leaving twenty
 * amber rows to read as twenty separate problems.
 */
export interface ConnectivityState {
  /** False when the operator turned the check off. */
  enabled: boolean;
  offline: boolean;
  /** Epoch milliseconds; null while online. */
  offlineSince: number | null;
  /**
   * The server can see out, but its DNS resolver answered none of the
   * connectivity check's reference names. Never true together with
   * `offline`.
   */
  resolverDown: boolean;
  /** Epoch milliseconds; null while the resolver is answering. */
  resolverDownSince: number | null;
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
 * server has lost its own connection or its resolver, so a body that does not
 * say exactly that must never produce it. An `offline: true` without a
 * parseable time, or on a check that is switched off, is treated as unknown
 * rather than guessed at; so is a resolver fault claimed together with an
 * offline host, which the server never sends.
 *
 * A body without the resolver fields is a server from before they existed,
 * and says nothing about the resolver: read as answering.
 */
export function parseConnectivity(value: unknown): ConnectivityState | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const d = value as Record<string, unknown>;
  if (typeof d.enabled !== "boolean" || typeof d.offline !== "boolean") return null;
  const resolver = parseResolver(d);
  if (resolver === null) return null;
  if (!d.offline) {
    if (d.offline_since !== null) return null;
    if (resolver.resolverDown && !d.enabled) return null;
    return { enabled: d.enabled, offline: false, offlineSince: null, ...resolver };
  }
  if (!d.enabled || typeof d.offline_since !== "string" || resolver.resolverDown) return null;
  const since = Date.parse(d.offline_since);
  if (!Number.isFinite(since)) return null;
  return { enabled: true, offline: true, offlineSince: since, ...resolver };
}

function parseResolver(
  d: Record<string, unknown>,
): Pick<ConnectivityState, "resolverDown" | "resolverDownSince"> | null {
  if (d.resolver_down === undefined && d.resolver_down_since === undefined) {
    return { resolverDown: false, resolverDownSince: null };
  }
  if (typeof d.resolver_down !== "boolean") return null;
  if (!d.resolver_down) {
    return d.resolver_down_since === null ? { resolverDown: false, resolverDownSince: null } : null;
  }
  if (typeof d.resolver_down_since !== "string") return null;
  const since = Date.parse(d.resolver_down_since);
  if (!Number.isFinite(since)) return null;
  return { resolverDown: true, resolverDownSince: since };
}
