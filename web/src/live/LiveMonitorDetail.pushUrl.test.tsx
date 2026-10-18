// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorDetailRoot } from "./LiveMonitorDetail";
import type { EventSourceLike } from "./connection";

/**
 * A new push URL from the monitor's page: the leaked or lost URL is retired
 * without deleting the monitor and its history, and the new one is shown once.
 */

const pushMonitor = (over: Record<string, unknown> = {}) => ({
  id: 1, name: "nightly backup", type: "push", target: "",
  interval_s: 60, timeout_s: 10, enabled: true, status: "up",
  push_interval_s: 3600, push_grace_s: 300, push_token_prefix: "sgu_old123",
  last_check: "2026-09-11T08:00:00Z", latency_ms: null,
  created_at: "2026-09-01T00:00:00Z", heartbeats: [], ...over,
});

const NEW_URL = "https://status.example.com/api/v1/push/sgu_new456secret";
const source = () => ({ readyState: 1, onopen: null, onerror: null, addEventListener() {}, close() {} }) as EventSourceLike;

function renderDetail(options: { monitors?: unknown[]; canWrite?: boolean; replacePush?: (id: string) => Promise<string> } = {}) {
  const monitors = options.monitors ?? [pushMonitor()];
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const body = url.includes("/uptime") ? { windows: [] }
      : url.includes("/incidents") ? { incidents: [] }
      : { monitors };
    return { ok: true, status: 200, json: async () => body };
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const replacePush = vi.fn(options.replacePush ?? (async () => NEW_URL));
  render(
    <LiveMonitorDetailRoot
      client={client} id="1" beatWidth={400} canWrite={options.canWrite}
      replacePush={replacePush} createEventSource={source}
    />,
  );
  return { client, replacePush };
}

async function chooseNewPushUrl() {
  fireEvent.click(await screen.findByRole("button", { name: "More actions" }));
  const item = screen.getByRole("menuitem", { name: /New push URL/ });
  // The item says what it costs before it is pressed.
  expect(item.textContent).toContain("Stops the current URL working");
  fireEvent.click(item);
  const dialog = await screen.findByRole("dialog", { name: "New push URL" });
  // The question arrives with its chunk; wait for it rather than the frame.
  await screen.findByRole("button", { name: "Replace the URL" });
  return dialog;
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("a new push URL from the monitor's page", () => {
  it("asks first, then shows the new URL once and refetches the monitor", async () => {
    const { client, replacePush } = renderDetail();
    const invalidate = vi.spyOn(client, "invalidateQueries");
    const dialog = await chooseNewPushUrl();
    expect(dialog.textContent).toContain("stops working immediately");
    expect(dialog.textContent).toContain("Every job that uses it has to be updated");
    // Nothing is sent by opening the question.
    expect(replacePush).not.toHaveBeenCalled();

    await act(async () => { fireEvent.click(await screen.findByRole("button", { name: "Replace the URL" })); });
    await waitFor(() => expect(replacePush).toHaveBeenCalledWith("1"));
    const url = (await screen.findByLabelText("Push URL")) as HTMLInputElement;
    expect(url.value).toBe(NEW_URL);
    expect(screen.getByRole("heading", { name: "nightly backup has a new push URL" })).toBeTruthy();
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["monitors"] });

    fireEvent.click(screen.getByRole("button", { name: "I have saved it" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("changes nothing when the question is closed with Escape", async () => {
    const { replacePush } = renderDetail();
    const dialog = await chooseNewPushUrl();
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(replacePush).not.toHaveBeenCalled();
  });

  it("asks before Escape closes over the only copy of the new URL", async () => {
    renderDetail();
    const dialog = await chooseNewPushUrl();
    await act(async () => { fireEvent.click(await screen.findByRole("button", { name: "Replace the URL" })); });
    await screen.findByLabelText("Push URL");
    const ask = vi.spyOn(window, "confirm").mockReturnValue(false);
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(ask).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("dialog", { name: "New push URL" })).toBeTruthy();
  });

  it("stays on the question and says why when the server refuses", async () => {
    renderDetail({ replacePush: async () => { throw new Error("database is locked"); } });
    await chooseNewPushUrl();
    await act(async () => { fireEvent.click(await screen.findByRole("button", { name: "Replace the URL" })); });
    expect(await screen.findByText("Could not replace the push URL: database is locked")).toBeTruthy();
    expect(screen.queryByLabelText("Push URL")).toBeNull();
    // The question is still answerable: the failure did not close it.
    expect((screen.getByRole("button", { name: "Replace the URL" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("is offered only on a push monitor, and only to a writer", async () => {
    renderDetail({ monitors: [pushMonitor({ type: "http", target: "https://api.example.com", push_interval_s: undefined, push_grace_s: undefined, push_token_prefix: undefined })] });
    fireEvent.click(await screen.findByRole("button", { name: "More actions" }));
    expect(screen.queryByRole("menuitem", { name: /New push URL/ })).toBeNull();
    cleanup();
    renderDetail({ canWrite: false });
    await screen.findByRole("article", { name: "nightly backup" });
    expect(screen.queryByRole("button", { name: "More actions" })).toBeNull();
  });
});
