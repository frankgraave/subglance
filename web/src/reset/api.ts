import { apiPost } from "../api/http";

/** The phrase the operator types. The server compares the same string exactly. */
export const RESET_PHRASE = "DELETE ALL DATA";

/** What a reset removed directly; rows that went by cascade are not counted. */
export interface ResetResult {
  monitors: number;
  channels: number;
  api_tokens: number;
  maintenance_windows: number;
}

const count = (value: unknown) => typeof value === "number" && Number.isInteger(value) && value >= 0;

export async function resetInstance(confirm: string): Promise<ResetResult | null> {
  const res = await apiPost("/api/v1/instance/reset", { confirm });
  const data: unknown = await res.json().catch(() => null);
  // The reset has happened by now; an unreadable summary is reported as
  // "done" rather than as a failure the operator might retry.
  if (!data || typeof data !== "object") return null;
  const d = data as Record<string, unknown>;
  return count(d.monitors) && count(d.channels) && count(d.api_tokens) && count(d.maintenance_windows)
    ? (d as unknown as ResetResult)
    : null;
}
