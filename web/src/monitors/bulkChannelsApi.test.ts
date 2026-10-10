import { afterEach, expect, it, vi } from "vitest";
import { changeChannels, type ChannelOperation } from "./bulkChannelsApi";

const op: ChannelOperation = { action: "add", monitor_ids: [1, 2], channel_id: 4 };
const etag = `"channels-${"a".repeat(64)}"`;
const counts = { total: 2, changed: 1, unchanged: 1, left_without_own: 0, channel_enabled: true };
afterEach(() => vi.restoreAllMocks());

it("previews without a validator and commits with one, on the bulk routes", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json(counts, { headers: { ETag: etag } }));
  expect(await changeChannels(op)).toEqual({ total: 2, changed: 1, unchanged: 1, leftWithoutOwn: 0, channelEnabled: true, etag });
  const [previewUrl, previewInit] = fetch.mock.calls[0];
  expect(String(previewUrl)).toMatch(/\/api\/v1\/monitors\/channels\/preview$/);
  expect(new Headers(previewInit?.headers).get("If-Match")).toBeNull();
  expect(JSON.parse(String(previewInit?.body))).toEqual(op);
  fetch.mockResolvedValue(Response.json(counts));
  expect((await changeChannels(op, etag)).etag).toBeUndefined();
  const [commitUrl, commitInit] = fetch.mock.calls[1];
  expect(String(commitUrl)).toMatch(/\/api\/v1\/monitors\/channels$/);
  expect(new Headers(commitInit?.headers).get("If-Match")).toBe(etag);
});

it.each([null, "", "*", `"tags-${"a".repeat(64)}"`])("refuses an unpaired preview validator %s", async (validator) => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    Response.json(counts, { headers: validator === null ? {} : { ETag: validator } }),
  );
  await expect(changeChannels(op)).rejects.toThrow(/validator/);
});

it.each([
  null,
  {},
  { ...counts, changed: -1 },
  { ...counts, unchanged: 2 },
  { ...counts, left_without_own: 2 },
  { ...counts, channel_enabled: "yes" },
])("refuses malformed result %j rather than a false success", async (body) => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json(body, { headers: { ETag: etag } }));
  await expect(changeChannels(op)).rejects.toThrow(/counts/);
});

it.each([[[]], [[1, 1]], [[0]], [[1.5]]])("sends no request for selection %j", async (ids) => {
  const fetch = vi.spyOn(globalThis, "fetch");
  await expect(changeChannels({ ...op, monitor_ids: ids })).rejects.toThrow(/selection/);
  expect(fetch).not.toHaveBeenCalled();
});
