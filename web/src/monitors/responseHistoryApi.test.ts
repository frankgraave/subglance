import { afterEach, expect, it, vi } from "vitest";
import { onUnauthorized } from "../api/http";
import { QueryClient } from "@tanstack/react-query";
import { fetchResponseHistory, responseHistoryQueryKey } from "./responseHistoryApi";
import { detailQueryKey } from "./detail";

it("refreshes response evidence when a recorded check invalidates that monitor's detail", async () => {
  const client = new QueryClient();
  client.setQueryData(responseHistoryQueryKey("7"), []);
  client.setQueryData(responseHistoryQueryKey("8"), []);
  await client.invalidateQueries({ queryKey: detailQueryKey("7"), refetchType: "none" });
  expect(client.getQueryState(responseHistoryQueryKey("7"))?.isInvalidated).toBe(true);
  expect(client.getQueryState(responseHistoryQueryKey("8"))?.isInvalidated).toBe(false);
  client.clear();
});

afterEach(() => vi.unstubAllGlobals());

it("fetches the real detail-only heartbeat endpoint with session auth and cancellation", async () => {
  const beats = [{ id: "9007199254740993", ts: "2026-09-19T12:00:00Z", ok: false, response: { body: "<script>text</script>" }, response_capture_reason: null }];
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ heartbeats: beats })));
  vi.stubGlobal("fetch", fetch);
  const signal = new AbortController().signal;
  expect(await fetchResponseHistory("7", signal)).toEqual(beats);
  expect(fetch).toHaveBeenCalledWith("/api/v1/monitors/7/heartbeats?limit=100", expect.objectContaining({ credentials: "same-origin", signal }));
});

it("announces a 401 to the existing session handler rather than treating it as empty history", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response('{"error":"session expired"}', { status: 401 })));
  const unauthorized = vi.fn();
  const stop = onUnauthorized(unauthorized);
  try {
    await expect(fetchResponseHistory("7")).rejects.toThrow("session expired");
    expect(unauthorized).toHaveBeenCalledOnce();
  } finally { stop(); }
});

it("rejects malformed successful payloads instead of claiming there were no failures", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response('{}')));
  await expect(fetchResponseHistory("7")).rejects.toThrow("Invalid heartbeat history response");
});

it.each([
  [undefined], [null], [1], [""], ["0"], ["-1"], ["01"], ["1.5"], ["1", "1"],
])("rejects absent, invalid or duplicate raw identities: %j", async (...ids) => {
  const heartbeats = ids.map((id) => ({ id, ts: "2026-09-19T12:00:00Z", ok: false }));
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ heartbeats }))));
  await expect(fetchResponseHistory("7")).rejects.toThrow("Invalid heartbeat history identity");
});
