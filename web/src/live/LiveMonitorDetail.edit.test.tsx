// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorDetailRoot } from "./LiveMonitorDetail";
import type { EventSourceLike } from "./connection";

const base = { id: 1, name: "Latest settings", type: "http", target: "https://example.com", interval_s: 60, timeout_s: 10, enabled: true, status: "up", created_at: "2026-09-01T00:00:00Z", repeat_after_s: 731 };
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
function fixture(etag: string | null = 'W/"1"', canWrite = true) {
  let stored = { ...base }; let version = etag; const writes: RequestInit[] = [];
  const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: version ? { ETag: version } : {} });
  const fetch = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/v1/monitors/1") {
      if (init?.method === "PATCH") {
        writes.push(init);
        if ((init.headers as Record<string, string>)["If-Match"] !== version) return json({ error: "monitor was modified" }, 412);
        stored = { ...stored, ...JSON.parse(init.body as string) }; version = 'W/"3"';
      }
      return json(stored);
    }
    if (url.includes("/uptime")) return json({ windows: [] });
    if (url.includes("/incidents")) return json({ incidents: [] });
    return json({ monitors: [{ ...stored, name: "Stale list name" }] });
  });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const props = { id: "1", client, canWrite, beatWidth: 390, createEventSource: () => ({ readyState: 1, onopen: null, onerror: null, addEventListener() {}, close() {} }) as EventSourceLike };
  const view = render(<StrictMode><LiveMonitorDetailRoot {...props} /></StrictMode>);
  return { writes, fetch, view, props, client, stored: () => stored, concurrent: () => { stored = { ...stored, name: "Another editor", repeat_after_s: 877 }; version = 'W/"2"'; } };
}

it("edits from detail using paired settings/ETag and refreshes all affected reads", async () => {
  const f = fixture(); const invalidate = vi.spyOn(f.client, "invalidateQueries");
  fireEvent.click(await screen.findByRole("button", { name: "Edit monitor" }));
  const name = await screen.findByLabelText("Name") as HTMLInputElement;
  expect(name.value).toBe("Latest settings");
  fireEvent.change(name, { target: { value: "Renamed" } });
  fireEvent.change(screen.getByLabelText("Repeat alerts"), { target: { value: "off" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(f.writes).toHaveLength(1);
  expect((f.writes[0].headers as Record<string, string>)["If-Match"]).toBe('W/"1"');
  expect(JSON.parse(f.writes[0].body as string)).toEqual({ name: "Renamed", repeat_after_s: 0 });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["monitor-detail", "1"] });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["incidents", "open"] });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["monitors"] });
  fireEvent.click(screen.getByRole("button", { name: "Edit monitor" }));
  await waitFor(() => expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Renamed"));
  expect((screen.getByLabelText("Repeat alerts") as HTMLSelectElement).value).toBe("off");
});

it("never overwrites a concurrent edit and reloads only by deliberate action", async () => {
  const f = fixture();
  fireEvent.click(await screen.findByRole("button", { name: "Edit monitor" }));
  fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "Draft" } });
  f.concurrent();
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await screen.findByText(/nothing was overwritten/i);
  expect(f.stored().name).toBe("Another editor"); expect(f.writes).toHaveLength(1);
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Draft");
  fireEvent.click(screen.getByRole("button", { name: "Reload latest settings" }));
  await waitFor(() => expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Another editor"));
  expect((screen.getByLabelText("Repeat alert base (seconds)") as HTMLInputElement).value).toBe("877");
  expect(f.writes).toHaveLength(1);
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Rebased draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(f.writes).toHaveLength(2));
  expect((f.writes[1].headers as Record<string, string>)["If-Match"]).toBe('W/"2"');
});

it.each([null, "", "*", "junk", 'W/""'])("cannot save without a usable paired validator %s", async (etag) => {
  const f = fixture(etag);
  fireEvent.click(await screen.findByRole("button", { name: "Edit monitor" }));
  await screen.findByText(/version.*reload/i);
  expect(screen.queryByRole("button", { name: "Save changes" })).toBeNull();
  expect(f.writes).toHaveLength(0);
});

it("does not expose editing or read request secrets to viewers", async () => {
  const f = fixture('W/"1"', false);
  await screen.findByText("Stale list name");
  expect(screen.queryByRole("button", { name: "Edit monitor" })).toBeNull();
  expect(f.fetch.mock.calls.some(([url]) => url === "/api/v1/monitors/1")).toBe(false);
});

it("ignores a canceled settings read after another monitor opens", async () => {
  const f = fixture();
  let release!: (value: Response) => void;
  f.fetch.mockImplementation(async (url: string) => {
    if (url === "/api/v1/monitors/1") return new Promise<Response>((r) => { release = r; });
    return new Response(JSON.stringify(url.includes("incidents") ? { incidents: [] } : url.includes("uptime") ? { windows: [] } : { monitors: [base, { ...base, id: 2, name: "Other" }] }));
  });
  fireEvent.click(await screen.findByRole("button", { name: "Edit monitor" }));
  await waitFor(() => expect(release).toBeTypeOf("function"));
  f.view.rerender(<LiveMonitorDetailRoot {...f.props} id="2" />);
  await act(async () => release(new Response(JSON.stringify(base), { headers: { ETag: 'W/"1"' } })));
  expect(screen.queryByLabelText("Name")).toBeNull();
});
