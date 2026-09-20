import { afterEach, expect, it, vi } from "vitest";
import { changeTags, type TagOperation } from "./bulkTagsApi";
import { onUnauthorized } from "../api/http";
const op: TagOperation = {
  action: "apply",
  monitor_ids: [1],
  key: "env",
  value: "prod",
};
const etag = `"tags-${"a".repeat(64)}"`;
const counts = { total: 1, changed: 1, unchanged: 0, collisions: 0 };
afterEach(() => vi.restoreAllMocks());
it.each([null, "", "*", 'W/"old"', '"other"'])(
  "refuses an unpaired preview validator %s",
  async (validator) => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(counts), {
        headers: validator === null ? {} : { ETag: validator },
      }),
    );
    await expect(changeTags(op)).rejects.toThrow(/validator/);
  },
);
it.each([
  null,
  {},
  [],
  { ...counts, changed: -1 },
  { ...counts, unchanged: 2 },
  { ...counts, changed: 0.5 },
  { ...counts, collisions: 3 },
])("refuses malformed result %j rather than false success", async (body) => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify(body), { headers: { ETag: etag } }),
  );
  await expect(changeTags(op)).rejects.toThrow(/counts/);
});
it.each(["", "*", '"bad"'])(
  "never sends an invalid commit validator %s",
  async (validator) => {
    const request = vi.spyOn(globalThis, "fetch");
    await expect(changeTags(op, validator)).rejects.toThrow(/validator/);
    expect(request).not.toHaveBeenCalled();
  },
);
it.each(
  [
    [],
    [0],
    [-1],
    [1, 1],
    [1.5],
    [Number.MAX_SAFE_INTEGER + 1],
    Array.from({ length: 10001 }, (_, i) => i + 1),
  ].map((ids) => [ids]),
)("rejects unsafe selections without a write", async (ids) => {
  const request = vi.spyOn(globalThis, "fetch");
  await expect(
    changeTags({ ...op, monitor_ids: ids } as TagOperation),
  ).rejects.toThrow(/selection/);
  expect(request).not.toHaveBeenCalled();
});
it("announces expiry and preserves the API's own rejection", async () => {
  const expired = vi.fn();
  const stop = onUnauthorized(expired);
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response('{"error":"session expired"}', { status: 401 }),
  );
  try {
    await expect(changeTags(op)).rejects.toThrow("session expired");
    expect(expired).toHaveBeenCalledOnce();
  } finally {
    stop();
  }
});
it("posts exactly one operation with same-origin credentials and paired ETag", async () => {
  const request = vi
    .spyOn(globalThis, "fetch")
    .mockResolvedValue(new Response(JSON.stringify(counts)));
  await expect(changeTags(op, etag)).resolves.toEqual(counts);
  expect(request).toHaveBeenCalledOnce();
  expect(request.mock.calls[0][1]).toMatchObject({
    credentials: "same-origin",
    method: "POST",
    body: JSON.stringify(op),
  });
  expect(new Headers(request.mock.calls[0][1]?.headers).get("If-Match")).toBe(
    etag,
  );
});
