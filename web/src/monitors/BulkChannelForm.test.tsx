// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { BulkChannelForm } from "./BulkChannelForm";
import type { ChannelChange, ChannelResult } from "./bulkChannelsApi";
import { channelFromApi } from "../notifications/channels";
import type { ApiChannel, Channel } from "../notifications/channels";

/*
 * The selection's channel change: one channel added or removed, preview then
 * confirm. What is pinned: the request names one channel and never a set, a
 * preview goes stale when the choice changes, the routing consequences are
 * stated before the confirm, and nothing is sent until a channel is chosen.
 */

afterEach(cleanup);

function channel(over: Partial<ApiChannel> = {}): Channel {
  return channelFromApi({ id: 1, name: "Ops", type: "slack", config: {}, enabled: true, ...over } as ApiChannel);
}
const CHANNELS = [channel({ id: 4, name: "Ops" }), channel({ id: 7, name: "Old hook", type: "webhook", enabled: false })];
const load = () => Promise.resolve(CHANNELS);
const etag = `"channels-${"a".repeat(64)}"`;
const counts = (over: Partial<ChannelResult> = {}): ChannelResult =>
  ({ total: 2, changed: 2, unchanged: 0, leftWithoutOwn: 0, channelEnabled: true, etag, ...over });

function setup(change: ChannelChange, over: { onDefault?: number; selectedIds?: string[] } = {}) {
  const busy = vi.fn();
  const view = render(<BulkChannelForm selectedIds={over.selectedIds ?? ["1", "2"]} onDefault={over.onDefault ?? 0}
    onChange={change} onClose={vi.fn()} onBusyChange={busy} load={load} />);
  return { view, busy };
}
async function choose(id: string) {
  fireEvent.change(await screen.findByRole("combobox", { name: "Channel" }), { target: { value: id } });
}

it("adds one channel to the selection: preview, then confirm under the preview's validator", async () => {
  const change = vi.fn<ChannelChange>(async () => counts({ changed: 1, unchanged: 1 }));
  const { busy } = setup(change);
  expect((screen.getByRole("button", { name: "Preview change" }) as HTMLButtonElement).disabled).toBe(true);
  await choose("4");
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  const confirm = await screen.findByRole("button", { name: "Confirm channel change" });
  expect(change.mock.calls[0]).toEqual([{ action: "add", monitor_ids: [1, 2], channel_id: 4 }, undefined]);
  expect(screen.getByText("1 monitor will change; 1 already have it.")).toBeTruthy();
  await waitFor(() => expect(document.activeElement).toBe(confirm));
  fireEvent.click(confirm);
  await waitFor(() => expect(change).toHaveBeenCalledTimes(2));
  expect(change.mock.calls[1]).toEqual([{ action: "add", monitor_ids: [1, 2], channel_id: 4 }, etag]);
  await waitFor(() => expect(document.activeElement).toBe(screen.getByText("Added to 1 monitor; 1 unchanged.")));
  expect(busy.mock.calls.map(([b]) => b)).toEqual([true, false, true, false]);
});

it.each(["action", "channel", "selection"])("invalidates a completed preview when the %s changes", async (what) => {
  const change = vi.fn<ChannelChange>(async () => counts());
  const { view } = setup(change);
  await choose("4");
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  await screen.findByRole("button", { name: "Confirm channel change" });
  if (what === "action") fireEvent.change(screen.getByRole("combobox", { name: "Action" }), { target: { value: "remove" } });
  else if (what === "channel") await choose("7");
  else view.rerender(<BulkChannelForm selectedIds={["2"]} onDefault={0} onChange={change} onClose={vi.fn()}
    onBusyChange={vi.fn()} load={load} />);
  expect(screen.queryByRole("button", { name: "Confirm channel change" })).toBeNull();
});

it("says which monitors a remove leaves to the rules or the default", async () => {
  const change = vi.fn<ChannelChange>(async () => counts({ leftWithoutOwn: 1 }));
  setup(change);
  fireEvent.change(screen.getByRole("combobox", { name: "Action" }), { target: { value: "remove" } });
  await choose("4");
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  await screen.findByRole("button", { name: "Confirm channel change" });
  expect(change.mock.calls[0][0]).toEqual({ action: "remove", monitor_ids: [1, 2], channel_id: 4 });
  expect(screen.getByText(/1 monitor will have no channel of its own left, so alerts go to matching tag routing rules, or else the default channel\./)).toBeTruthy();
});

it("says an add ends the default for monitors on it, and that a disabled channel sends nothing", async () => {
  const change = vi.fn<ChannelChange>(async () => counts({ channelEnabled: false }));
  setup(change, { onDefault: 2 });
  await choose("7");
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  await screen.findByRole("button", { name: "Confirm channel change" });
  expect(screen.getByText(/2 monitors alert through the default channel today; with a channel of their own, the default no longer applies\./)).toBeTruthy();
  expect(screen.getByText(/Old hook is disabled: it sends nothing until it is switched back on, but it still keeps the default channel out\./)).toBeTruthy();
});

it("cannot confirm a preview that changes nothing, and shows a refusal without a success line", async () => {
  let fail = false;
  const change = vi.fn<ChannelChange>(async () => {
    if (fail) throw new Error("channels changed since the preview; review a new preview before saving");
    return counts({ changed: 0, unchanged: 2 });
  });
  setup(change);
  await choose("4");
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  const confirm = await screen.findByRole("button", { name: "Confirm channel change" });
  expect((confirm as HTMLButtonElement).disabled).toBe(true);
  await waitFor(() => expect(document.activeElement).toBe(screen.getByText("0 monitors will change; 2 already have it.")));
  fail = true;
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  expect((await screen.findByRole("alert")).textContent).toContain("channels changed since the preview");
  expect(screen.queryByText(/^Added to/)).toBeNull();
});

it("offers nothing to send while the channel list has not loaded or failed", async () => {
  const change = vi.fn<ChannelChange>();
  render(<BulkChannelForm selectedIds={["1"]} onDefault={0} onChange={change} onClose={vi.fn()}
    onBusyChange={vi.fn()} load={() => Promise.reject(new Error("HTTP 500"))} />);
  expect(await screen.findByText(/Could not load channels: HTTP 500\./)).toBeTruthy();
  expect(screen.queryByRole("combobox", { name: "Channel" })).toBeNull();
  expect((screen.getByRole("button", { name: "Preview change" }) as HTMLButtonElement).disabled).toBe(true);
  expect(change).not.toHaveBeenCalled();
});
