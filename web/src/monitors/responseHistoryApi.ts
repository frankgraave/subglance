import { apiJSON } from "../api/http";
import type { ResponseHeartbeat } from "./responseHistory";

export const RESPONSE_HISTORY_LIMIT = 100;
export const responseHistoryQueryKey = (id: string) => ["response-history", id, RESPONSE_HISTORY_LIMIT] as const;

/** The raw endpoint is required: bulk monitor beats intentionally omit bodies. */
export async function fetchResponseHistory(id: string, signal?: AbortSignal): Promise<ResponseHeartbeat[]> {
  const body = await apiJSON<{ heartbeats?: ResponseHeartbeat[] }>(
    `/api/v1/monitors/${encodeURIComponent(id)}/heartbeats?limit=${RESPONSE_HISTORY_LIMIT}`,
    { signal },
  );
  if (!Array.isArray(body?.heartbeats)) throw new Error("Invalid heartbeat history response");
  return body.heartbeats;
}
