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
  etag?: string;
};

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
    ![body.total, body.changed, body.unchanged, body.collisions].every(
      (value) => Number.isSafeInteger(value) && value >= 0,
    ) ||
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
  return {
    total: body.total,
    changed: body.changed,
    unchanged: body.unchanged,
    collisions: body.collisions,
    ...(etag === undefined ? { etag: validator! } : {}),
  };
}
