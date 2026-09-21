// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorDetailRoot } from "./LiveMonitorDetail";
import type { EventSourceLike } from "./connection";

const base = { id: 1, name: "Latest settings", type: "http", target: "https://example.com", interval_s: 60, timeout_s: 10, enabled: true, status: "up", created_at: "2026-09-01T00:00:00Z", repeat_after_s: 731 };
const other = { ...base, id: 2, name: "Other monitor" };
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
    if (url === "/api/v1/monitors/2") return json(other);
    if (url.includes("/uptime")) return json({ windows: [] });
    if (url.includes("/incidents")) return json({ incidents: [] });
    if (url.includes("/heartbeats")) return json({ heartbeats: [] });
    return json({ monitors: [{ ...stored, name: "Stale list name" }, other] });
  });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const props = { id: "1", client, canWrite, beatWidth: 390, createEventSource: () => ({ readyState: 1, onopen: null, onerror: null, addEventListener() {}, close() {} }) as EventSourceLike };
  const tree = (id: string) => <StrictMode><LiveMonitorDetailRoot {...props} id={id} /></StrictMode>;
  const view = render(tree(props.id));
  return { writes, fetch, rerender: (id: string) => view.rerender(tree(id)), client, stored: () => stored, concurrent: () => { stored = { ...stored, name: "Another editor", repeat_after_s: 877 }; version = 'W/"2"'; } };
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

it.each(["draft", "conflict", "missing destination"])("ends a %s edit session across A → B → A without reopening the drawer", async (state) => {
  const f = fixture();
  const opener = await screen.findByRole("button", { name: "Edit monitor" });
  opener.focus();
  fireEvent.click(opener);
  const name = await screen.findByLabelText<HTMLInputElement>("Name");
  fireEvent.change(name, { target: { value: "Abandoned draft" } });
  if (state === "conflict") {
    f.concurrent();
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await screen.findByText(/nothing was overwritten/i);
  }
  const settingsReads = () => f.fetch.mock.calls.filter(([url, init]) => url === "/api/v1/monitors/1" && init?.method !== "PATCH").length;
  const reads = settingsReads();
  // An ordinary render of A must keep the same form, draft and conflict.
  f.rerender("1");
  expect(screen.getByLabelText("Name")).toBe(name);
  expect(name.value).toBe("Abandoned draft");
  if (state === "conflict") expect(screen.getByText(/nothing was overwritten/i)).toBeTruthy();

  const detail = document.querySelector(".mon-detail");
  f.rerender(state === "missing destination" ? "99" : "2");
  if (state === "missing destination") await screen.findByText(/does not exist/i);
  else {
    await screen.findByText("Other monitor");
    // Keep the component mounted; replacing StrictMode would hide this bug.
    expect(document.querySelector(".mon-detail")).toBe(detail);
  }
  expect(screen.queryByRole("dialog")).toBeNull();
  const back = screen.getByRole("button", { name: /All monitors/ });
  back.focus();
  f.rerender("1");
  await screen.findByText("Stale list name");
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(settingsReads()).toBe(reads);
  if (state !== "missing destination") await waitFor(() => expect(document.activeElement).toBe(back));

  fireEvent.click(screen.getByRole("button", { name: "Edit monitor" }));
  const fresh = await screen.findByLabelText<HTMLInputElement>("Name");
  expect(fresh).not.toBe(name);
  expect(fresh.value).toBe(f.stored().name);
  expect(screen.queryByText(/nothing was overwritten/i)).toBeNull();
});

it("ends a loading edit across A → B → A and ignores its canceled settings read", async () => {
  const f = fixture();
  const pending: { release: (value: Response) => void; signal: AbortSignal }[] = [];
  const fetch = f.fetch.getMockImplementation()!;
  f.fetch.mockImplementation(async (url: string, init?: RequestInit) => {
    if (url === "/api/v1/monitors/1") return new Promise<Response>((release) => { pending.push({ release, signal: init!.signal! }); });
    return fetch(url, init);
  });
  fireEvent.click(await screen.findByRole("button", { name: "Edit monitor" }));
  await screen.findByText("Loading current settings…");
  await waitFor(() => expect(pending.length).toBeGreaterThan(0));
  const reads = pending.length;
  f.rerender("1");
  expect(pending).toHaveLength(reads);
  expect(screen.getByText("Loading current settings…")).toBeTruthy();
  f.rerender("2");
  await screen.findByText("Other monitor");
  expect(pending.every(({ signal }) => signal.aborted)).toBe(true);
  f.rerender("1");
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(pending).toHaveLength(reads);
  f.fetch.mockImplementation(fetch);
  await act(async () => {
    for (const { release } of pending) release(new Response(JSON.stringify(base), { headers: { ETag: 'W/"1"' } }));
  });
  expect(screen.queryByLabelText("Name")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Edit monitor" }));
  expect((await screen.findByLabelText<HTMLInputElement>("Name")).value).toBe("Latest settings");
});
