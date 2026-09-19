// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorDetailRoot } from "../live/LiveMonitorDetail";
import type { EventSourceLike } from "../live/connection";

const source = (): EventSourceLike => ({ onopen: null, onerror: null, readyState: 0, addEventListener() {}, close() {} });
const monitor = {
  id: 7, name: "diagnostic monitor", type: "http", target: "https://example.com", enabled: true,
  capture_response: true, status: "down", interval_s: 60, timeout_s: 10,
  created_at: "2026-09-19T00:00:00Z", last_check: "2026-09-19T12:00:00Z", heartbeats: [],
};

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it("mounts raw captured responses in the actual live detail through the authenticated fetcher", async () => {
  const raw = '<img src=x onerror=alert(1)><script>alert(1)</script>';
  const fetch = vi.fn(async (url: string) => new Response(JSON.stringify(
    url.includes("/heartbeats?") ? { heartbeats: [
      { ts: "2026-09-19T12:00:00Z", ok: false, response_capture_reason: "flapping" },
      { ts: "2026-09-19T11:59:00Z", ok: false, response: { body: raw, truncated: true, headers: { "Content-Type": "text/html" } } },
    ] } : url.includes("/uptime") ? { windows: [] } : url.includes("/incidents") ? { incidents: [] } : { monitors: [monitor] },
  )));
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(<LiveMonitorDetailRoot client={client} id="7" beatWidth={400} createEventSource={source} />);
  const summary = await screen.findByText("Captured response");
  expect(container.querySelector("details")?.open).toBe(false);
  fireEvent.click(summary);
  expect(container.querySelector("pre")?.textContent).toBe(raw);
  expect(container.querySelector("pre script, pre img")).toBeNull();
  expect(screen.getByText(/Capture stopped while this monitor was flapping/)).toBeTruthy();
  expect(screen.getByText(/Truncated/)).toBeTruthy();
  await waitFor(() => expect(fetch).toHaveBeenCalledWith("/api/v1/monitors/7/heartbeats?limit=100", expect.objectContaining({ credentials: "same-origin" })));
});

it("shows capture-off history without a response disclosure", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => new Response(JSON.stringify(
    url.includes("/heartbeats?") ? { heartbeats: [{ ts: "2026-09-19T12:00:00Z", ok: false, response_capture_reason: "disabled" }] }
      : url.includes("/uptime") ? { windows: [] } : url.includes("/incidents") ? { incidents: [] } : { monitors: [{ ...monitor, capture_response: false }] },
  ))));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(<LiveMonitorDetailRoot client={client} id="7" beatWidth={400} createEventSource={source} />);
  expect(await screen.findByText("Capture was switched off for this check.")).toBeTruthy();
  expect(container.querySelector("details")).toBeNull();
});
