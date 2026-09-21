// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { createQueryClient } from "../live/queryClient";
import { QueryObserver } from "@tanstack/react-query";
import { fetchMonitors, monitorsQueryKey } from "../live/api";
import { inventoryQueryKey } from "../monitors/inventoryApi";
import { CommandMenu } from "./CommandMenu";

const row = { id: 7, name: "Payment API", type: "http", target: "https://payments.example", enabled: true, status: "up", interval_s: 60, timeout_s: 10 };
let enabled = true;
let reply: (response: Response) => void;
const props = () => ({ client: createQueryClient(), canWrite: true, onClose: vi.fn(), onOpenMonitor: vi.fn(), onNavigate: vi.fn(), onAddMonitor: vi.fn(), onThemeChange: vi.fn() });
beforeEach(() => {
  enabled = true;
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute("open"); };
  vi.stubGlobal("fetch", vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "POST") return new Promise<Response>((resolve) => { reply = resolve; });
    return Promise.resolve(new Response(JSON.stringify({ monitors: [{ ...row, enabled }] }), { status: 200 }));
  }));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it("pauses and resumes via the real fetcher, locks duplicates and refreshes shared monitor caches", async () => {
  const p = props();
  const invalidate = vi.spyOn(p.client, "invalidateQueries");
  render(<CommandMenu {...p} />);
  const pause = await screen.findByRole("option", { name: "Pause Payment API" });
  act(() => { pause.click(); pause.click(); });
  expect(screen.getByRole("status").textContent).toContain("Pausing");
  const posts = () => vi.mocked(fetch).mock.calls.filter(([, init]) => init?.method === "POST");
  expect(posts()).toHaveLength(1);
  expect(posts()[0][0]).toBe("/api/v1/monitors/7/pause");
  enabled = false;
  await act(async () => reply(new Response(null, { status: 204 })));
  const resume = await screen.findByRole("option", { name: "Resume Payment API" });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["monitors"] });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["monitor-detail", "7"] });
  expect(p.client.getQueryData<Array<{enabled: boolean}>>(inventoryQueryKey)?.[0].enabled).toBe(false);
  fireEvent.click(resume);
  expect(posts()[1][0]).toBe("/api/v1/monitors/7/resume");
  enabled = true;
  await act(async () => reply(new Response(null, { status: 204 })));
  await screen.findByRole("option", { name: "Pause Payment API" });
});

it("keeps a failed write visible and retryable without inventing a paused state", async () => {
  render(<CommandMenu {...props()} />);
  fireEvent.click(await screen.findByRole("option", { name: "Pause Payment API" }));
  await act(async () => reply(new Response(JSON.stringify({ error: "scheduler unavailable" }), { status: 503 })));
  expect((await screen.findByRole("alert")).textContent).toContain("scheduler unavailable");
  expect(screen.queryByRole("option", { name: "Resume Payment API" })).toBeNull();
  fireEvent.click(screen.getByRole("option", { name: "Pause Payment API" }));
  enabled = false;
  await act(async () => reply(new Response(null, { status: 204 })));
  await screen.findByRole("option", { name: "Resume Payment API" });
  expect(screen.queryByRole("alert")).toBeNull();
});

it("never offers write commands to a read-only session", async () => {
  render(<CommandMenu {...props()} canWrite={false} />);
  await screen.findByRole("option", { name: /Open Payment API/ });
  expect(screen.queryByRole("option", { name: /Pause|Resume|Add monitor/ })).toBeNull();
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "pause" } });
  fireEvent.keyDown(screen.getByRole("combobox"), { key: "Enter" });
  expect(vi.mocked(fetch).mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
});

it("distinguishes loading, failure, retry, empty inventory and no search matches", async () => {
  const p = props();
  p.client.setDefaultOptions({ queries: { retry: false } });
  let resolve!: (response: Response) => void;
  vi.mocked(fetch).mockImplementation(() => new Promise((done) => { resolve = done; }));
  render(<CommandMenu {...p} />);
  expect(screen.getByRole("status").textContent).toContain("Loading monitors");
  await act(async () => resolve(new Response("{}", { status: 503 })));
  expect((await screen.findByRole("alert")).textContent).toContain("503");
  expect(screen.getByRole("option", { name: "Go to Dashboard" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry loading monitors" }));
  await act(async () => resolve(new Response(JSON.stringify({ monitors: [] }), { status: 200 })));
  await screen.findByText("No monitors yet. Navigate or add your first monitor.");
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "unfindable" } });
  expect(screen.getByRole("status").textContent).toContain("No matching commands or monitors");
});

it("handles arrows directly, traps Tab and ignores IME, repeats and modified action keys", async () => {
  const p = props();
  render(<CommandMenu {...p} />);
  await screen.findByRole("option", { name: /Open Payment API/ });
  const input = screen.getByRole("combobox");
  fireEvent.change(input, { target: { value: "theme" } });
  const active = () => document.getElementById(input.getAttribute("aria-activedescendant")!)?.textContent;
  expect(active()).toBe("Use light theme");
  fireEvent.keyDown(input, { key: "ArrowUp" });
  expect(active()).toBe("Use system theme");
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(active()).toBe("Use light theme");
  for (const extra of [{ isComposing: true }, { keyCode: 229 }, { repeat: true }, { ctrlKey: true }, { metaKey: true }, { altKey: true }, { shiftKey: true }]) {
    fireEvent.keyDown(input, { key: "Enter", ...extra });
    fireEvent.keyDown(input, { key: "Escape", ...extra });
  }
  expect(p.onThemeChange).not.toHaveBeenCalled();
  expect(p.onClose).not.toHaveBeenCalled();
  fireEvent.keyDown(input, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Close command menu" }));
  fireEvent.keyDown(document.activeElement!, { key: "Tab" });
  expect(document.activeElement).toBe(input);
  fireEvent.keyDown(input, { key: "Enter" });
  expect(p.onThemeChange).toHaveBeenCalledWith("light");
});

it("aborts pending work on unmount and never repopulates a cleared session cache", async () => {
  const p = props();
  const invalidate = vi.spyOn(p.client, "invalidateQueries");

  const mounted = render(<CommandMenu {...p} />);
  fireEvent.click(await screen.findByRole("option", { name: "Pause Payment API" }));
  const signal = vi.mocked(fetch).mock.calls.find(([, init]) => init?.method === "POST")![1]!.signal;
  mounted.unmount();
  p.client.clear();
  expect(signal?.aborted).toBe(true);
  await act(async () => reply(new Response(null, { status: 204 })));
  expect(invalidate).not.toHaveBeenCalled();
  expect(p.client.getQueryCache().getAll()).toHaveLength(0);

});

it("returns pointer write focus to the combobox before a result can disappear", async () => {
  render(<CommandMenu {...props()} />);
  const pause = await screen.findByRole("option", { name: "Pause Payment API" });
  pause.focus();
  fireEvent.click(pause);
  expect(document.activeElement).toBe(screen.getByRole("combobox"));
});

it("finishes a committed write after ordinary dismissal and reconciles the active dashboard", async () => {
  const p = props();
  const observer = new QueryObserver(p.client, { queryKey: monitorsQueryKey, queryFn: ({ signal }) => fetchMonitors(signal) });
  const unsubscribe = observer.subscribe(() => {});
  const mounted = render(<CommandMenu {...p} open />);
  fireEvent.click(await screen.findByRole("option", { name: "Pause Payment API" }));
  mounted.rerender(<CommandMenu {...p} open={false} />);
  expect(screen.queryByRole("dialog")).toBeNull();
  const signal = vi.mocked(fetch).mock.calls.find(([, init]) => init?.method === "POST")![1]!.signal;
  expect(signal?.aborted).toBe(false);
  enabled = false;
  await act(async () => reply(new Response(null, { status: 204 })));
  await vi.waitFor(() => expect(observer.getCurrentResult().data?.[0].status).toBe("paused"));
  mounted.rerender(<CommandMenu {...p} open />);
  await screen.findByRole("option", { name: "Resume Payment API" });
  unsubscribe();
});
