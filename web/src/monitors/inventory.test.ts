import { describe, expect, it } from "vitest";
import {
  canCheckNow,
  describeChannels,
  routesNowhere,
  describeInventory,
  filterByType,
  intervalOf,
  inventoryFromApi,
  sortInventory,
  inventoryFromPayload,
  typeLabel,
} from "./inventory";
import type { InventoryMonitor } from "./inventory";

const api = {
  id: 7,
  name: "auth",
  type: "http",
  target: "https://auth.example.com",
  interval_s: 60,
  timeout_s: 10,
  enabled: true,
  status: "up" as const,
  created_at: "2026-09-01T10:00:00Z",
};

function monitor(over: Partial<InventoryMonitor> = {}): InventoryMonitor {
  return { ...inventoryFromApi(api), ...over };
}

describe("inventoryFromApi", () => {
  it("carries the settings the dashboard model drops", () => {
    const m = inventoryFromApi(api);
    expect(m.type).toBe("http");
    expect(m.intervalS).toBe(60);
    expect(m.timeoutS).toBe(10);
    expect(m.enabled).toBe(true);
    expect(m.createdAt).toBe(Date.parse("2026-09-01T10:00:00Z"));
  });

  it("reports a push monitor as having no timeout, not a timeout of zero", () => {
    // Nothing is dialled, so the stored number describes nothing. Rendering it
    // would put a setting on screen that changing cannot affect.
    const m = inventoryFromApi({
      ...api,
      type: "push",
      target: "",
      timeout_s: 10,
      push_interval_s: 3600,
    });
    expect(m.timeoutS).toBeNull();
  });

  it("reads an absent TLS floor as no opinion, not as the current default", () => {
    // The server omits the field for a monitor with no floor. Substituting
    // "1.2" here would make the edit form offer to pin a floor nobody set,
    // and the monitor would then stop following the default if it moved.
    expect(inventoryFromApi(api).minTlsVersion).toBe("");
    expect(
      inventoryFromApi({ ...api, min_tls_version: "1.0" }).minTlsVersion,
    ).toBe("1.0");
  });

  it("reads the default only where it stands in for an empty channel list", () => {
    const ops = { id: 3, name: "Ops" };
    expect(
      inventoryFromApi({ ...api, channels: [], default_channel: ops }).channels,
    ).toEqual({ known: true, names: [], ids: [], fallback: "Ops", fallbackId: "3" });
    // Beside real attachments the default does not apply, whatever the wire says.
    expect(
      inventoryFromApi({ ...api, channels: [{ id: 1, name: "Pager" }], default_channel: ops })
        .channels,
    ).toEqual({ known: true, names: ["Pager"], ids: ["1"] });
    // With the attachments unknown, nothing is claimed about the default either.
    expect(inventoryFromApi({ ...api, default_channel: ops }).channels).toEqual({
      known: false,
    });
  });

  it("collapses a disabled monitor into the paused status", () => {
    const m = inventoryFromApi({ ...api, enabled: false });
    expect(m.enabled).toBe(false);
    expect(m.status).toBe("paused");
  });

  it("translates a whole payload", () => {
    expect(inventoryFromPayload({ monitors: [api, api] })).toHaveLength(2);
    expect(inventoryFromPayload(undefined)).toEqual([]);
  });
});

describe("typeLabel", () => {
  it("upper-cases the types the API actually has", () => {
    expect(typeLabel("http")).toBe("HTTP");
    expect(typeLabel("ssl")).toBe("SSL");
  });

  it("says UNKNOWN rather than echoing a type this build has no column for", () => {
    // A server newer than this build could send anything. Echoing it would
    // render an unexplained word in a column whose other values are a fixed
    // vocabulary.
    expect(typeLabel("gopher")).toBe("UNKNOWN");
  });
});

describe("routing rules on the channel state (SUB-147)", () => {
  const base = {
    id: 7, name: "auth", type: "http", target: "https://auth.example.com",
    interval_s: 60, timeout_s: 10, enabled: true, status: "up",
    created_at: "2026-09-01T10:00:00Z",
  };
  const rule = { rule_id: 1, tag_key: "env", tag_value: "prod", channels: [{ id: 4, name: "Pager" }] };

  it("names each rule-routed channel with its rule, after the monitor's own", () => {
    const m = inventoryFromApi({ ...base, channels: [{ id: 2, name: "Chat" }], rule_channels: [rule] } as Parameters<typeof inventoryFromApi>[0]);
    expect(describeChannels(m.channels)).toBe("Chat, Pager via env:prod");
    expect(routesNowhere(m.channels)).toBe(false);
  });

  it("does not believe a default sent beside a rule route", () => {
    const m = inventoryFromApi({ ...base, channels: [], rule_channels: [rule], default_channel: { id: 3, name: "Ops" } } as Parameters<typeof inventoryFromApi>[0]);
    expect(m.channels).toMatchObject({ known: true, names: [] });
    expect(m.channels.known && m.channels.fallback).toBeFalsy();
    expect(describeChannels(m.channels)).toBe("Pager via env:prod");
  });

  it("claims nothing when the rule list is malformed", () => {
    for (const bad of [null, [{ tag_key: "env" }], [{ ...rule, channels: [{ id: 0, name: "x" }] }]]) {
      const m = inventoryFromApi({ ...base, channels: [], rule_channels: bad } as unknown as Parameters<typeof inventoryFromApi>[0]);
      expect(m.channels).toEqual({ known: false });
    }
  });

  it("treats an absent rule list as no rules, so an older server still reads", () => {
    const m = inventoryFromApi({ ...base, channels: [] } as Parameters<typeof inventoryFromApi>[0]);
    expect(routesNowhere(m.channels)).toBe(true);
    expect(describeChannels(m.channels)).toBe("none");
  });
});

describe("describeChannels", () => {
  it("separates 'we asked and there are none' from 'we never found out'", () => {
    // The whole reason the column exists: `none` is a finding — nobody will
    // hear about this monitor — and a failed request rendering as `none` would
    // manufacture that finding out of a 500.
    expect(describeChannels({ known: true, names: [] })).toBe("none");
    expect(describeChannels({ known: false })).toBe("not loaded");
  });

  it("names the default on a monitor that has no channels of its own", () => {
    // With a default set, an empty list is not the finding "none": somebody
    // does hear about this monitor, and the cell has to say who.
    expect(describeChannels({ known: true, names: [], fallback: "Ops" })).toBe(
      "Ops (default)",
    );
  });

  it("lists the channel names when it knows them", () => {
    expect(describeChannels({ known: true, names: ["slack", "email"] })).toBe(
      "slack, email",
    );
  });
});

describe("filterByType", () => {
  const list = [
    monitor({ id: "1", type: "http" }),
    monitor({ id: "2", type: "tcp" }),
  ];

  it("narrows to one type", () => {
    expect(filterByType(list, "tcp").map((m) => m.id)).toEqual(["2"]);
  });

  it("treats no choice as no filter", () => {
    expect(filterByType(list, null)).toHaveLength(2);
    expect(filterByType(list, "")).toHaveLength(2);
  });
});

describe("canCheckNow", () => {
  it("allows a check on a paused monitor", () => {
    // Verifying before resuming is the commonest reason to press it, and the
    // server runs the probe without recording the result.
    const paused = inventoryFromApi({ ...api, enabled: false });
    expect(canCheckNow(paused)).toBe(true);
  });

  it("refuses a push monitor, which SubGlance never calls", () => {
    const push = inventoryFromApi({
      ...api,
      type: "push",
      target: "",
      push_interval_s: 3600,
    });
    expect(canCheckNow(push)).toBe(false);
  });
});

describe("intervalOf", () => {
  it("uses the push window for a push monitor", () => {
    const push = inventoryFromApi({
      ...api,
      type: "push",
      target: "",
      interval_s: 60,
      push_interval_s: 3600,
    });
    expect(intervalOf(push)).toBe(3600);
  });

  it("uses the check interval otherwise", () => {
    expect(intervalOf(monitor())).toBe(60);
  });
});

describe("describeInventory", () => {
  it("states the paused count, because this page is where they are found", () => {
    const list = [monitor({ id: "1" }), monitor({ id: "2", enabled: false })];
    expect(describeInventory(list)).toBe("2 configured, 1 paused");
  });

  it("says nothing about pausing when nothing is paused", () => {
    expect(describeInventory([monitor()])).toBe("1 configured");
  });
});

describe("sortInventory", () => {
  const list = [
    monitor({ id: "1", name: "Marketing site", status: "up", type: "http", intervalS: 60 }),
    monitor({ id: "2", name: "API checkout", status: "down", type: "tcp", intervalS: 30 }),
    monitor({ id: "3", name: "docs", status: "paused", type: "ping", intervalS: 300 }),
    monitor({ id: "4", name: "Backups", status: "warning", type: "http", intervalS: 30 }),
  ];
  const names = (sorted: InventoryMonitor[]) => sorted.map((m) => m.name);

  it("orders by name, ignoring case, rather than by creation", () => {
    expect(names(sortInventory(list, "name"))).toEqual(["API checkout", "Backups", "docs", "Marketing site"]);
  });

  it("puts the worst status first, so the top of the list is what is wrong", () => {
    // Not the name order by coincidence: "Backups" (warning) and "docs"
    // (paused) are what the status rank puts there.
    const shuffled = [list[2], list[0], list[3], list[1]];
    expect(sortInventory(shuffled, "status").map((m) => m.status)).toEqual(["down", "warning", "paused", "up"]);
  });

  it("groups by type and breaks ties by name", () => {
    expect(names(sortInventory(list, "type"))).toEqual(["Backups", "Marketing site", "docs", "API checkout"]);
  });

  it("orders by interval, shortest first, and breaks ties by name", () => {
    expect(names(sortInventory(list, "interval"))).toEqual(["API checkout", "Backups", "Marketing site", "docs"]);
  });

  it("returns a copy and leaves the caller's array alone", () => {
    const before = names(list);
    expect(sortInventory(list, "name")).not.toBe(list);
    expect(names(list)).toEqual(before);
  });
});
