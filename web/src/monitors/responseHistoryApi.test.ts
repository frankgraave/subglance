import { afterEach, expect, it, vi } from "vitest";
import { onUnauthorized } from "../api/http";
import { fetchResponseHistory } from "./responseHistoryApi";

afterEach(() => vi.unstubAllGlobals());

it("fetches the real detail-only heartbeat endpoint with session auth and cancellation", async () => {
  const beats = [{ ts: "2026-09-19T12:00:00Z", ok: false, response: { body: "<script>text</script>" }, response_capture_reason: null }];
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
