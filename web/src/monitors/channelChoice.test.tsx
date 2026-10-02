// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { AddMonitor } from "./AddMonitor";
import { EditMonitorForm } from "./EditMonitorForm";
import { inventoryFromApi } from "./inventory";
import { ApiError } from "./preview";
import type { PreviewResult } from "./preview";
import { channelFromApi } from "../notifications/channels";
import type { ApiChannel, Channel } from "../notifications/channels";
import { describeRouting } from "./channelChoice";

/*
 * A monitor's own channels, chosen in its add and edit forms (SUB-179).
 *
 * What is pinned: the boxes start from what is attached, the save sends the
 * set only when it changed, the default is named as the fallback when nothing
 * is ticked, a monitor that would alert nobody says so, and an unknown or
 * unloadable list never turns into "detach everything".
 */

afterEach(cleanup);

function channel(over: Partial<ApiChannel> = {}): Channel {
  return channelFromApi({ id: 1, name: "Ops", type: "slack", config: {}, enabled: true, ...over } as ApiChannel);
}

const CHANNELS = [
  channel({ id: 1, name: "Ops", type: "slack", is_default: true }),
  channel({ id: 2, name: "Pager", type: "sms" }),
  channel({ id: 3, name: "Old hook", type: "webhook", enabled: false }),
];
const loadAll = () => Promise.resolve(CHANNELS);

function make(over: Record<string, unknown> = {}) {
  return inventoryFromApi({
    id: 1, name: "auth", type: "http", target: "https://auth.example.com",
    interval_s: 60, timeout_s: 10, enabled: true, status: "up",
    created_at: "2026-09-01T10:00:00Z", repeat_after_s: 900,
    channels: [], rule_channels: [],
    ...over,
  } as Parameters<typeof inventoryFromApi>[0]);
}

const box = (name: RegExp) => screen.getByRole("checkbox", { name }) as HTMLInputElement;
const group = () => screen.getByRole("group", { name: "Channels" });

describe("describeRouting", () => {
  const [OPS, PAGER, OLD] = CHANNELS;
  const OFF_PAGER = channel({ id: 2, name: "Pager", type: "sms", enabled: false });
  const OFF_OPS = channel({ id: 1, name: "Ops", type: "slack", is_default: true, enabled: false });

  it("names the default as the fallback only while nothing is chosen", () => {
    expect(describeRouting([], [], CHANNELS)?.text).toBe("With none chosen, alerts go to the default channel, Ops.");
    expect(describeRouting([PAGER], [], CHANNELS)?.text).toMatch(/default channel, Ops, is not used while a channel is chosen/);
    expect(describeRouting([PAGER], [], [PAGER, OLD])).toBeNull();
  });

  it("warns when nobody would hear about the monitor", () => {
    expect(describeRouting([], [], [PAGER, OLD])).toEqual({
      text: "With none chosen and no default channel, nobody is alerted about this monitor.", warn: true,
    });
  });

  it("names rule-routed channels, which the default never replaces", () => {
    const rules = [{ tag: "env:prod", names: ["Pager"], ids: ["2"] }];
    expect(describeRouting([], rules, CHANNELS)).toEqual({ text: "With none chosen, alerts go to Pager via env:prod, from a tag routing rule.", warn: false });
    expect(describeRouting([OPS, OLD], rules, CHANNELS)?.text).toBe("Alerts also go to Pager via env:prod, from a tag routing rule.");
  });

  // The server counts a disabled channel toward a monitor's route, so it
  // keeps the default out, and then sends it nothing.
  it("does not count a disabled channel as somebody who hears", () => {
    expect(describeRouting([OLD], [], CHANNELS)).toEqual({
      text: "Every channel this monitor is routed to is disabled, so nobody is alerted about it.", warn: true,
    });
    const offRule = [{ tag: "env:prod", names: ["Pager"], ids: ["2"] }];
    expect(describeRouting([], offRule, [OPS, OFF_PAGER, OLD])).toEqual({
      text: "Every channel this monitor is routed to is disabled, so nobody is alerted about it.", warn: true,
    });
    expect(describeRouting([OLD], offRule, CHANNELS)).toEqual({
      text: "Nothing chosen here is enabled, so alerts go only to Pager via env:prod, from a tag routing rule.", warn: false,
    });
    expect(describeRouting([], [], [OFF_OPS, PAGER])).toEqual({
      text: "With none chosen, alerts go to the default channel, Ops, which is disabled, so nobody is alerted about this monitor.", warn: true,
    });
  });
});

describe("EditMonitorForm channels", () => {
  it("starts from the attached set and sends a changed set with the edit", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={make({ channels: [{ id: 2, name: "Pager" }] })} onSave={onSave} loadChannels={loadAll} />);
    await waitFor(() => expect(box(/^Pager/).checked).toBe(true));
    expect(box(/^Ops/).checked).toBe(false);
    // The type and state ride on the label, so two channels with one name
    // can be told apart and a disabled one is not mistaken for a live route.
    expect(box(/^Ops/).closest("label")!.textContent).toBe("Ops · Slack · default");
    expect(box(/^Old hook/).closest("label")!.textContent).toBe("Old hook · Webhook · disabled");

    fireEvent.click(box(/^Ops/));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ channel_ids: [1, 2] });
  });

  it("sends no channels when only something else changed", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={make({ channels: [{ id: 2, name: "Pager" }] })} onSave={onSave} loadChannels={loadAll} />);
    await waitFor(() => expect(box(/^Pager/).checked).toBe(true));
    // Ticked and unticked again is no change.
    fireEvent.click(box(/^Ops/));
    fireEvent.click(box(/^Ops/));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "auth-eu" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ name: "auth-eu" });
  });

  it("clears the set with an empty list and says the default takes over", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={make({ channels: [{ id: 2, name: "Pager" }] })} onSave={onSave} loadChannels={loadAll} />);
    await waitFor(() => expect(box(/^Pager/).checked).toBe(true));
    expect(within(group()).queryByText(/alerts go to the default channel/)).toBeNull();
    fireEvent.click(box(/^Pager/));
    expect(within(group()).getByText("With none chosen, alerts go to the default channel, Ops.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ channel_ids: [] });
  });

  it("drops a channel deleted since the monitor was read instead of sending it back", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={make({ channels: [{ id: 2, name: "Pager" }, { id: 9, name: "Gone" }] })} onSave={onSave} loadChannels={loadAll} />);
    await waitFor(() => expect(box(/^Pager/).checked).toBe(true));
    fireEvent.click(box(/^Ops/));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ channel_ids: [1, 2] });
  });

  it("offers no channel control when the read did not say what is attached", async () => {
    // Unknown shown as none would detach every channel on the first save.
    const onSave = vi.fn().mockResolvedValue(undefined);
    const load = vi.fn(loadAll);
    render(<EditMonitorForm monitor={make({ channels: undefined, rule_channels: undefined })} onSave={onSave} loadChannels={load} />);
    expect(screen.queryByRole("group", { name: "Channels" })).toBeNull();
    expect(screen.getByText(/channels could not be read, so they cannot be changed here/)).toBeTruthy();
    expect(load).not.toHaveBeenCalled();
  });

  it("keeps the rest of the form saveable when the channel list fails", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={make({ channels: [{ id: 2, name: "Pager" }] })} onSave={onSave}
      loadChannels={() => Promise.reject(new Error("HTTP 500"))} />);
    await within(group()).findByText(/Could not load channels: HTTP 500/);
    expect(screen.queryByRole("checkbox", { name: /^Pager/ })).toBeNull();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "auth-eu" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ name: "auth-eu" });
  });

  it("names the rule that routes the monitor beside its own channels", async () => {
    render(<EditMonitorForm monitor={make({ channels: [], tags: { env: "prod" },
      rule_channels: [{ rule_id: 4, tag_key: "env", tag_value: "prod", channels: [{ id: 2, name: "Pager" }] }] })}
      onSave={vi.fn()} loadChannels={loadAll} />);
    expect(await within(group()).findByText("With none chosen, alerts go to Pager via env:prod, from a tag routing rule.")).toBeTruthy();
  });

  it("puts a rejection about channel_ids under the channels", async () => {
    const onSave = vi.fn().mockRejectedValue(new ApiError(400, "channel 2 does not exist; it may have been deleted", null, "channel_ids"));
    render(<EditMonitorForm monitor={make({ channels: [] })} onSave={onSave} loadChannels={loadAll} />);
    await waitFor(() => expect(box(/^Pager/)).toBeTruthy());
    fireEvent.click(box(/^Pager/));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    const alert = await within(group()).findByRole("alert");
    expect(alert.textContent).toMatch(/channel 2 does not exist/);
    expect(box(/^Pager/).getAttribute("aria-invalid")).toBe("true");
    await waitFor(() => expect(document.activeElement).toBe(box(/^Ops/)));
  });
});

describe("AddMonitor channels", () => {
  const ok: PreviewResult = { ok: true, checked_at: "2026-01-01T12:00:00Z", latency_ms: 1, type: "http", target: "https://example.com", status_code: 200 } as PreviewResult;

  it("creates the monitor with the channels ticked", async () => {
    const create = vi.fn().mockResolvedValue({ id: "7" });
    render(<AddMonitor api={{ preview: vi.fn().mockResolvedValue(ok), create, channels: loadAll }} />);
    fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
    await waitFor(() => expect(box(/^Pager/)).toBeTruthy());
    expect(within(group()).getByText("With none chosen, alerts go to the default channel, Ops.")).toBeTruthy();
    fireEvent.click(box(/^Pager/));
    fireEvent.click(screen.getByRole("button", { name: /test it/i }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toMatch(/answered/i));
    fireEvent.click(screen.getByRole("button", { name: /save monitor/i }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0][0]).toMatchObject({ channel_ids: [2] });
  });

  it("sends no channel_ids when none is ticked", async () => {
    const create = vi.fn().mockResolvedValue({ id: "7" });
    render(<AddMonitor api={{ preview: vi.fn().mockResolvedValue(ok), create, channels: loadAll }} />);
    fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
    fireEvent.click(screen.getByRole("button", { name: /test it/i }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toMatch(/answered/i));
    fireEvent.click(screen.getByRole("button", { name: /save monitor/i }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0][0]).not.toHaveProperty("channel_ids");
  });

  it("warns before saving a monitor nobody would hear about", async () => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn(), channels: () => Promise.resolve([channel({ id: 5, name: "Team", is_default: false })]) }} />);
    expect(await within(group()).findByText("With none chosen and no default channel, nobody is alerted about this monitor.")).toBeTruthy();
  });

  it("says so when there are no channels at all", async () => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn(), channels: () => Promise.resolve([]) }} />);
    expect(await within(group()).findByText(/No notification channels exist yet, so nobody is alerted about this monitor/)).toBeTruthy();
  });
});
