import { describe, expect, it } from "vitest";
import { inventoryFromApi } from "../monitors/inventory";
import type { InventoryMonitor } from "../monitors/inventory";
import { channelFromApi } from "./channels";
import {
  coverageList,
  coverageOf,
  describeCoverage,
  silentCount,
  unconfirmedCount,
} from "./coverage";

/*
 * "Who hears about this monitor" is the notifier's rule restated: own
 * channels win, an empty list falls back to the default, and a disabled
 * channel delivers nothing. Each case below is one way that answer can be
 * nobody, or one way it could be wrongly reported as nobody.
 */

const ops = channelFromApi({ id: 3, name: "Ops", type: "email", enabled: true });
const pager = channelFromApi({ id: 1, name: "Pager", type: "slack", enabled: true });
const off = channelFromApi({ id: 2, name: "Old", type: "slack", enabled: false });

function monitor(over: Record<string, unknown> = {}): InventoryMonitor {
  return inventoryFromApi({
    id: 7,
    name: "auth",
    type: "http",
    target: "https://auth.example.com",
    interval_s: 60,
    timeout_s: 10,
    enabled: true,
    status: "up",
    created_at: "2026-09-01T10:00:00Z",
    channels: [],
    ...over,
  } as Parameters<typeof inventoryFromApi>[0]);
}

describe("coverageOf", () => {
  it("names a monitor's own channels", () => {
    const c = coverageOf(monitor({ channels: [{ id: 1, name: "Pager" }] }), [pager, ops]);
    expect(c.route).toBe("own");
    expect(c.silent).toBe(false);
    expect(describeCoverage(c)).toBe("Pager");
  });

  it("falls back to the default when a monitor has none of its own", () => {
    const c = coverageOf(monitor({ default_channel: { id: 3, name: "Ops" } }), [ops]);
    expect(c.route).toBe("default");
    expect(c.silent).toBe(false);
    expect(describeCoverage(c)).toBe("Ops (default)");
  });

  it("says nobody when there is neither an own channel nor a default", () => {
    const c = coverageOf(monitor(), [ops]);
    expect(c.silent).toBe(true);
    expect(describeCoverage(c)).toBe(
      "nobody: no channels of its own and no default",
    );
  });

  it("counts a disabled channel as nobody, because the notifier skips it", () => {
    const own = coverageOf(monitor({ channels: [{ id: 2, name: "Old" }] }), [off]);
    expect(own.silent).toBe(true);
    expect(describeCoverage(own)).toBe("nobody: Old (disabled)");

    const viaDefault = coverageOf(
      monitor({ default_channel: { id: 2, name: "Old" } }),
      [off],
    );
    expect(viaDefault.silent).toBe(true);
    expect(describeCoverage(viaDefault)).toBe(
      "nobody: the default, Old, is disabled",
    );
  });

  it("is not silent while one of several channels still delivers", () => {
    const c = coverageOf(
      monitor({ channels: [{ id: 2, name: "Old" }, { id: 1, name: "Pager" }] }),
      [off, pager],
    );
    expect(c.silent).toBe(false);
    expect(describeCoverage(c)).toBe("Old (disabled), Pager");
  });

  it("matches channels by id, not by name", () => {
    // Two channels may share a name; only the id says which one is off.
    const twin = channelFromApi({ id: 9, name: "Old", type: "email", enabled: true });
    const c = coverageOf(monitor({ channels: [{ id: 9, name: "Old" }] }), [off, twin]);
    expect(c.silent).toBe(false);
  });

  it("never reports nobody for a channel the channel list does not know", () => {
    // The two lists are polled separately; a stale one is not a finding.
    const c = coverageOf(monitor({ channels: [{ id: 42, name: "New" }] }), [ops]);
    expect(c.recipients).toEqual([{ name: "New", enabled: null }]);
    expect(c.silent).toBe(false);
  });

  it("claims nothing while the channel list knows a default the inventory does not", () => {
    // Polled separately: a default set a moment ago is on the channel list
    // before the inventory carries it, and "nobody" would be a false finding.
    const def = channelFromApi({ id: 3, name: "Ops", type: "email", enabled: true, is_default: true });
    const c = coverageOf(monitor(), [def]);
    expect(c.route).toBe("unknown");
    expect(c.silent).toBe(false);
    expect(describeCoverage(c)).toBe("not loaded");
  });

  it("claims nothing when the monitor's attachments could not be read", () => {
    const c = coverageOf(monitor({ channels: undefined }), [ops]);
    expect(c.route).toBe("unknown");
    expect(c.silent).toBe(false);
    expect(describeCoverage(c)).toBe("not loaded");
  });
});

describe("coverageList", () => {
  it("puts silent active monitors first and leaves paused ones out of the count", () => {
    const list = coverageList(
      [
        monitor({ id: 1, name: "a-covered", channels: [{ id: 1, name: "Pager" }] }),
        monitor({ id: 2, name: "b-paused", enabled: false }),
        monitor({ id: 3, name: "z-silent" }),
      ],
      [pager],
    );
    expect(list.map((c) => c.name)).toEqual(["z-silent", "a-covered", "b-paused"]);
    expect(silentCount(list)).toBe(1);
  });
});

describe("unconfirmedCount", () => {
  it("counts the active monitors that are neither silent nor confirmed to reach anyone", () => {
    const def = channelFromApi({ id: 3, name: "Ops", type: "email", enabled: true, is_default: true });
    const list = coverageList(
      [
        monitor({ id: 1, name: "covered", channels: [{ id: 3, name: "Ops" }] }),
        // The two lists disagree on the default: route unknown.
        monitor({ id: 2, name: "disagree" }),
        // Attached to a channel the channel list does not know yet.
        monitor({ id: 3, name: "stale", channels: [{ id: 42, name: "New" }] }),
        // Unconfirmed too, but paused: pausing is not a gap in coverage.
        monitor({ id: 4, name: "paused", enabled: false, channels: undefined }),
      ],
      [def],
    );
    expect(silentCount(list)).toBe(0);
    expect(unconfirmedCount(list)).toBe(2);
  });
});
