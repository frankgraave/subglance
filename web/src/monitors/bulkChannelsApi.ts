import { apiPost } from "../api/http";

/**
 * Adds one channel to, or removes it from, a selection of monitors.
 *
 * Never a replacement set: the server applies the change to the links it
 * holds at write time, so a monitor whose channels this screen could not read
 * cannot lose one it did not know about.
 */
export type ChannelOperation = {
  action: "add" | "remove";
  monitor_ids: number[];
  channel_id: number;
};

export type ChannelResult = {
  total: number;
  changed: number;
  unchanged: number;
  /** Changed monitors a remove leaves with no channel of their own. */
  leftWithoutOwn: number;
  /** A disabled channel still keeps the default out, but sends nothing. */
  channelEnabled: boolean;
  etag?: string;
};

export type ChannelChange = (operation: ChannelOperation, etag?: string) => Promise<ChannelResult>;

const validETag = (value: string | null | undefined): value is string =>
  typeof value === "string" && /^"channels-[a-f0-9]{64}"$/.test(value);

/** Absence of a validator means preview, never an unconditional write. */
export async function changeChannels(operation: ChannelOperation, etag?: string): Promise<ChannelResult> {
  if (etag !== undefined && !validETag(etag))
    throw new Error("Missing or invalid channel preview validator; preview again.");
  const ids = operation.monitor_ids;
  if (ids.length < 1 || ids.length > 10000 || new Set(ids).size !== ids.length ||
    ids.some((id) => !Number.isSafeInteger(id) || id <= 0)) {
    throw new Error("The selection must contain 1–10000 unique monitor IDs.");
  }
  const res = await apiPost(`/api/v1/monitors/channels${etag === undefined ? "/preview" : ""}`, operation, {
    headers: etag === undefined ? {} : { "If-Match": etag },
  });
  const body = (await res.json()) as Record<string, unknown> | null;
  const counts = [body?.total, body?.changed, body?.unchanged, body?.left_without_own];
  if (!body || !counts.every((n) => Number.isSafeInteger(n) && (n as number) >= 0) ||
    typeof body.channel_enabled !== "boolean") {
    throw new Error("The server returned invalid channel change counts; refresh before trying again.");
  }
  const [total, changed, unchanged, leftWithoutOwn] = counts as number[];
  if (changed + unchanged !== total || leftWithoutOwn > changed) {
    throw new Error("The server returned invalid channel change counts; refresh before trying again.");
  }
  const validator = res.headers.get("ETag");
  if (etag === undefined && !validETag(validator))
    throw new Error("Missing or invalid channel preview validator; preview again.");
  return {
    total, changed, unchanged, leftWithoutOwn,
    channelEnabled: body.channel_enabled,
    ...(etag === undefined ? { etag: validator! } : {}),
  };
}
