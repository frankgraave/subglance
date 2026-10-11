import { apiPost } from "../api/http";

export type TagOperation =
  | {
      action: "apply" | "remove";
      monitor_ids: number[];
      key: string;
      value: string;
    }
  | { action: "rename_key"; key: string; new_key: string }
  | { action: "rename_value"; key: string; value: string; new_value: string };
export type TagResult = {
  total: number;
  changed: number;
  unchanged: number;
  collisions: number;
  /** Configuration naming the old pair that a global rename moves with it. */
  routing_rules: number;
  maintenance_windows: number;
  status_pages: number;
  etag?: string;
};

const COUNTS = [
  "total",
  "changed",
  "unchanged",
  "collisions",
  "routing_rules",
  "maintenance_windows",
  "status_pages",
] as const;

/** Absence of a validator means preview, never an unconditional write. */
export async function changeTags(
  operation: TagOperation,
  etag?: string,
): Promise<TagResult> {
  const validETag = (value: string | null | undefined): value is string =>
    typeof value === "string" && /^"tags-[a-f0-9]{64}"$/.test(value);
  if (etag !== undefined && !validETag(etag))
    throw new Error("Missing or invalid tag preview validator; preview again.");
  if ("monitor_ids" in operation) {
    const ids = operation.monitor_ids;
    if (
      ids.length < 1 ||
      ids.length > 10000 ||
      new Set(ids).size !== ids.length ||
      ids.some((id) => !Number.isSafeInteger(id) || id <= 0)
    ) {
      throw new Error("The selection must contain 1–10000 unique monitor IDs.");
    }
  }
  const res = await apiPost(
    `/api/v1/monitors/tags${etag === undefined ? "/preview" : ""}`,
    operation,
    {
      headers: etag === undefined ? {} : { "If-Match": etag },
    },
  );
  const body = (await res.json()) as TagResult | null;
  if (
    !body ||
    !COUNTS.every((k) => Number.isSafeInteger(body[k]) && body[k] >= 0) ||
    body.changed + body.unchanged !== body.total ||
    body.collisions > body.changed
  ) {
    throw new Error(
      "The server returned invalid tag change counts; refresh before trying again.",
    );
  }
  const validator = res.headers.get("ETag");
  if (etag === undefined && !validETag(validator))
    throw new Error("Missing or invalid tag preview validator; preview again.");
  // Only the known counts are passed on, never whatever else the body held.
  const result = Object.fromEntries(
    COUNTS.map((k) => [k, body[k]]),
  ) as unknown as TagResult;
  if (etag === undefined) result.etag = validator!;
  return result;
}
