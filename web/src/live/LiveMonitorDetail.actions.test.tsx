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
  maintenance?: unknown[];
} = {}) {
  const monitors = options.monitors ?? [monitor()];
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const body = url.includes("/maintenance") ? { maintenance: options.maintenance ?? [] }
      : url.includes("/heartbeats") ? { heartbeats: [] }
      : url.includes("/uptime") ? { windows: [] }
      : url.includes("/incidents") ? { incidents: [] }
      : url.includes("/latency") ? { points: [] }
      : { monitors };
    return { ok: true, status: 200, json: async () => body };
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const pause = vi.fn(options.pause ?? (async () => undefined));
  const remove = vi.fn(options.remove ?? (async () => undefined));
  const tree = (id: string) => (
    <LiveMonitorDetailRoot
      client={client} id={id} beatWidth={400} canWrite={options.canWrite}
      pause={pause} remove={remove} onBack={options.onBack}
      createEventSource={source}
    />
  );
  const view = render(tree("1"));
  return { client, pause, remove, rerender: (id: string) => view.rerender(tree(id)) };
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

  it("schedules maintenance from the monitor's own page, narrowed to it", async () => {
    // A window for this monitor, one for a tag it carries, and one for a
    // monitor that is not this one: the drawer lists the two that cover it.
    renderDetail({
      monitors: [monitor({ tags: { env: "prod" } }), monitor({ id: 2, name: "db" })],
      maintenance: [
        { id: 1, name: "Deploy api", monitor_id: 1, active: false, starts_at: "2026-10-03T01:00:00Z", ends_at: "2026-10-03T02:00:00Z" },
        { id: 2, name: "Prod patching", tag_key: "env", tag_value: "prod", active: false, timezone: "UTC", weekdays: [0], local_time: "02:00", duration_minutes: 60 },
        { id: 3, name: "Database upgrade", monitor_id: 2, active: false, starts_at: "2026-10-03T01:00:00Z", ends_at: "2026-10-03T02:00:00Z" },
      ],
    });
    await openMore();
    fireEvent.click(screen.getByRole("menuitem", { name: /Schedule maintenance/ }));
    const dialog = await screen.findByRole("dialog", { name: "Maintenance for api" });
    await waitFor(() => expect(dialog.textContent).toContain("Deploy api"));
    expect(dialog.textContent).toContain("Prod patching");
    expect(dialog.textContent).not.toContain("Database upgrade");
    // The form starts on this monitor, and still offers the others.
    const choice = (await screen.findByRole("combobox", { name: "Monitor" })) as HTMLSelectElement;
    expect(choice.value).toBe("1");
    expect([...choice.options].map((o) => o.textContent)).toContain("db");
  });

  it("does not reopen maintenance on returning from another monitor", async () => {
    const { rerender } = renderDetail({ monitors: [monitor(), monitor({ id: 2, name: "db" })] });
    await openMore();
    fireEvent.click(screen.getByRole("menuitem", { name: /Schedule maintenance/ }));
    await screen.findByRole("dialog", { name: "Maintenance for api" });
    rerender("2");
    await screen.findByRole("article", { name: "db" });
    expect(screen.queryByRole("dialog")).toBeNull();
    rerender("1");
    await screen.findByRole("article", { name: "api" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("offers no menu to a viewer", async () => {
    renderDetail({ canWrite: false });
    await screen.findByRole("article", { name: "api" });
    expect(screen.queryByRole("button", { name: "More actions" })).toBeNull();
  });
});
