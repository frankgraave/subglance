import { apiJSON } from "../api/http";
import type { ResponseHeartbeat } from "./responseHistory";
import { detailQueryKey } from "./detail";

export const RESPONSE_HISTORY_LIMIT = 100;
// A recorded on-demand check already invalidates the monitor's detail family.
// Keep raw diagnostic evidence in that family so it refreshes with the result.
export const responseHistoryQueryKey = (id: string) => [...detailQueryKey(id), "responses", RESPONSE_HISTORY_LIMIT] as const;

/** The raw endpoint is required: bulk monitor beats intentionally omit bodies. */
export async function fetchResponseHistory(id: string, signal?: AbortSignal): Promise<ResponseHeartbeat[]> {
  const body = await apiJSON<{ heartbeats?: ResponseHeartbeat[] }>(
    `/api/v1/monitors/${encodeURIComponent(id)}/heartbeats?limit=${RESPONSE_HISTORY_LIMIT}`,
    { signal },
  );
  if (!Array.isArray(body?.heartbeats)) throw new Error("Invalid heartbeat history response");
  // Old servers without identity cannot safely reconcile native disclosures.
  // Reject the page, retaining any previous query data, rather than guessing
  // from timestamps, positions or potentially sensitive response contents.
  const ids = new Set<string>();
  for (const hb of body.heartbeats) {
    if (typeof hb?.id !== "string" || !/^[1-9]\d*$/.test(hb.id) || ids.has(hb.id)) {
      throw new Error("Invalid heartbeat history identity; reload after updating the server");
    }
    ids.add(hb.id);
  }
  return body.heartbeats;
}
