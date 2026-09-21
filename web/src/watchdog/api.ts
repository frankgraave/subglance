import { apiFetch } from "../api/http";

export interface WatchdogState {
  configured: boolean;
  interval_seconds: number | null;
  last_decision_at: string | null;
  last_attempt_at: string | null;
  last_success_at: string | null;
  last_result: "succeeded" | "rejected" | "transport_error" | "timeout" | "canceled" | "request_error" | null;
  last_status_code: number | null;
  last_event: "alive" | "stopped" | null;
  suppressed: boolean;
  in_flight: boolean;
  overdue: boolean;
}

export const watchdogKey = ["watchdog"] as const;

export async function fetchWatchdog(signal?: AbortSignal): Promise<WatchdogState> {
  const response = await apiFetch("/api/v1/watchdog", { signal, cache: "no-store" });
  if (!response.ok) throw new Error("Watchdog state unavailable.");
  const data: unknown = await response.json();
  if (!validState(data)) throw new Error("Watchdog state unavailable.");
  // Keep only the documented diagnostic values, not unexpected server fields.
  const { configured, interval_seconds, last_decision_at, last_attempt_at, last_success_at,
    last_result, last_status_code, last_event, suppressed, in_flight, overdue } = data;
  return { configured, interval_seconds, last_decision_at, last_attempt_at, last_success_at,
    last_result, last_status_code, last_event, suppressed, in_flight, overdue };
}

function validState(value: unknown): value is WatchdogState {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const d = value as Record<string, unknown>;
  if (!["configured", "suppressed", "in_flight", "overdue"].every((key) => typeof d[key] === "boolean")) return false;
  const dates = [d.last_decision_at, d.last_attempt_at, d.last_success_at];
  if (!dates.every((date) => date === null || (typeof date === "string" && /^\d{4}-\d{2}-\d{2}T/.test(date) && Number.isFinite(Date.parse(date))))) return false;
  if (![null, "succeeded", "rejected", "transport_error", "timeout", "canceled", "request_error"].includes(d.last_result as string | null)) return false;
  if (![null, "alive", "stopped"].includes(d.last_event as string | null)) return false;
  const code = d.last_status_code;
  if (code !== null && (typeof code !== "number" || !Number.isInteger(code) || code < 100 || code > 599)) return false;
  if (!d.configured) return d.interval_seconds === null && dates.every((date) => date === null) && d.last_result === null && code === null && d.last_event === null && !d.suppressed && !d.in_flight && !d.overdue;
  return typeof d.interval_seconds === "number" && Number.isFinite(d.interval_seconds) && d.interval_seconds > 0;
}
