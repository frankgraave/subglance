// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorDetailRoot } from "./LiveMonitorDetail";
import type { EventSourceLike } from "./connection";

/**
 * Pause, resume and delete from the monitor detail screen (SUB-119): the
 * screen an alert link lands on can stop checking the thing it is about
 * without a detour through the inventory.
 */

const monitor = (over: Record<string, unknown> = {}) => ({
  id: 1, name: "api", type: "http", target: "https://api.example.com",
  interval_s: 60, timeout_s: 10, enabled: true, status: "up",
  last_check: "2026-09-11T08:00:00Z", latency_ms: 30,
  created_at: "2026-09-01T00:00:00Z", heartbeats: [], ...over,
});

const source = () => ({ readyState: 1, onopen: null, onerror: null, addEventListener() {}, close() {} }) as EventSourceLike;

function renderDetail(options: {
  monitors?: unknown[];
  canWrite?: boolean;
  pause?: (id: string, paused: boolean) => Promise<void>;
  remove?: (id: string) => Promise<void>;
  onBack?: () => void;
} = {}) {
  const monitors = options.monitors ?? [monitor()];
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const body = url.includes("/heartbeats") ? { heartbeats: [] }
      : url.includes("/uptime") ? { windows: [] }
      : url.includes("/incidents") ? { incidents: [] }
      : url.includes("/latency") ? { points: [] }
      : { monitors };
    return { ok: true, status: 200, json: async () => body };
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const pause = vi.fn(options.pause ?? (async () => undefined));
  const remove = vi.fn(options.remove ?? (async () => undefined));
  render(
    <LiveMonitorDetailRoot
      client={client} id="1" beatWidth={400} canWrite={options.canWrite}
      pause={pause} remove={remove} onBack={options.onBack}
      createEventSource={source}
    />,
  );
  return { client, pause, remove };
}

async function openMore() {
  fireEvent.click(await screen.findByRole("button", { name: "More actions" }));
  return screen.getByRole("menu", { name: "Actions for api" });
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("LiveMonitorDetail actions", () => {
  it("pauses from the menu and refetches instead of flipping the status locally", async () => {
    const { client, pause } = renderDetail();
    const invalidate = vi.spyOn(client, "invalidateQueries");
    await openMore();
    const item = screen.getByRole("menuitem", { name: /Pause/ });
    // A bare verb makes the reader guess what stops; the item says it.
    expect(item.textContent).toContain("History is kept");
    fireEvent.click(item);
    await waitFor(() => expect(pause).toHaveBeenCalledWith("1", true));
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["monitors"] }));
    // Nothing on screen claims "paused" until the server's list says so.
    expect(document.querySelector(".mon-detail")?.getAttribute("data-status")).toBe("up");
  });

  it("offers resume, not pause, on a paused monitor", async () => {
    const { pause } = renderDetail({ monitors: [monitor({ enabled: false })] });
    await openMore();
    expect(screen.queryByRole("menuitem", { name: /Pause/ })).toBeNull();
    fireEvent.click(screen.getByRole("menuitem", { name: /Resume/ }));
    await waitFor(() => expect(pause).toHaveBeenCalledWith("1", false));
  });

  it("reports a failed pause in place", async () => {
    renderDetail({ pause: async () => { throw new Error("server said no"); } });
    await openMore();
    fireEvent.click(screen.getByRole("menuitem", { name: /Pause/ }));
    expect(await screen.findByText("Could not pause: server said no")).toBeTruthy();
    expect(screen.getByText("Could not pause: server said no").closest("[role=alert]")).not.toBeNull();
  });

  it("deletes only after the name is retyped, then leaves the page", async () => {
    const onBack = vi.fn();
    const { client, remove } = renderDetail({ onBack });
    // A resolved-history page cached by the incidents screen before the delete.
    client.setQueryData(["incidents", "resolved", 7], { incidents: [{ id: 9, monitor_id: 1 }] });
    await openMore();
    fireEvent.click(screen.getByRole("menuitem", { name: /Delete/ }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).toContain("pause it instead");
    const confirm = screen.getByRole("button", { name: "Delete api" }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "api" } });
    expect(confirm.disabled).toBe(false);
    await act(async () => { fireEvent.click(confirm); });
    await waitFor(() => expect(remove).toHaveBeenCalledWith("1"));
    await waitFor(() => expect(onBack).toHaveBeenCalledTimes(1));
    // Returning to the incidents page must not show the deleted monitor's history.
    expect(client.getQueryData(["incidents", "resolved", 7])).toBeUndefined();
  });

  it("stays on the page and says why when a delete fails", async () => {
    const onBack = vi.fn();
    renderDetail({ onBack, remove: async () => { throw new Error("database is locked"); } });
    await openMore();
    fireEvent.click(screen.getByRole("menuitem", { name: /Delete/ }));
    fireEvent.change(await screen.findByRole("textbox"), { target: { value: "api" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Delete api" })); });
    expect((await screen.findByText("Could not delete: database is locked")).closest("[role=alert]")).not.toBeNull();
    expect(onBack).not.toHaveBeenCalled();
  });

  it("offers no menu to a viewer", async () => {
    renderDetail({ canWrite: false });
    await screen.findByRole("heading", { name: "api" });
    expect(screen.queryByRole("button", { name: "More actions" })).toBeNull();
  });
});
