import { describe, expect, it } from "vitest";
import {
  describeFilter,
  describeTransitions,
  describeTags,
  filterByStatus,
  filterByTags,
  tagFacets,
  UNTAGGED_LABEL,
  filterMonitors,
  groupByTag,
  partition,
  sectionsByTag,
  summarise,
} from "./model";
import type { Monitor, MonitorStatus } from "./types";

const monitor = (
  id: string,
  status: MonitorStatus,
  over: Partial<Monitor> = {},
): Monitor => ({
  id,
  name: id,
  status,
  tags: {},
  target: `https://${id}.example.com`,
  latencyMs: 100,
  uptime24h: 99.9,
  beats: [],
  lastCheck: 1_700_000_000_000,
  ...over,
});

describe("partition", () => {
  it("lifts down monitors into the attention section", () => {
    const { attention, rest } = partition([
      monitor("api", "up"),
      monitor("db", "down"),
      monitor("cache", "up"),
    ]);
    expect(attention.map((m) => m.id)).toEqual(["db"]);
    expect(rest.map((m) => m.id)).toEqual(["api", "cache"]);
  });

  it("leaves pending and paused in the main list", () => {
    const { attention, rest } = partition([
      monitor("a", "pending"),
      monitor("b", "paused"),
      monitor("c", "down"),
    ]);
    // Pending is the ordinary state of a monitor that has not finished its
    // first check; filling the attention section with it would train people
    // to ignore the section.
    expect(attention.map((m) => m.id)).toEqual(["c"]);
    expect(rest.map((m) => m.id)).toEqual(["a", "b"]);
  });

  it("sorts both halves alphabetically by name", () => {
    const { attention, rest } = partition([
      monitor("zulu", "up"),
      monitor("alpha", "up"),
      monitor("yankee", "down"),
      monitor("bravo", "down"),
    ]);
    expect(attention.map((m) => m.name)).toEqual(["bravo", "yankee"]);
    expect(rest.map((m) => m.name)).toEqual(["alpha", "zulu"]);
  });

  it("is stable: input order does not change output order", () => {
    const list = [
      monitor("api", "up"),
      monitor("db", "down"),
      monitor("cache", "up"),
      monitor("queue", "down"),
    ];
    const first = partition(list);
    const shuffled = partition([list[2], list[0], list[3], list[1]]);
    expect(shuffled.attention.map((m) => m.id)).toEqual(
      first.attention.map((m) => m.id),
    );
    expect(shuffled.rest.map((m) => m.id)).toEqual(first.rest.map((m) => m.id));
  });

  it("orders monitors sharing a name deterministically by id", () => {
    const a = monitor("m2", "up", { name: "same" });
    const b = monitor("m1", "up", { name: "same" });
    expect(partition([a, b]).rest.map((m) => m.id)).toEqual(["m1", "m2"]);
    expect(partition([b, a]).rest.map((m) => m.id)).toEqual(["m1", "m2"]);
  });

  it("sorts numbered names the way a human reads them", () => {
    const { rest } = partition([
      monitor("node-10", "up"),
      monitor("node-2", "up"),
    ]);
    expect(rest.map((m) => m.name)).toEqual(["node-2", "node-10"]);
  });

  it("does not mutate the input array", () => {
    const list = [monitor("zulu", "up"), monitor("alpha", "up")];
    partition(list);
    expect(list.map((m) => m.id)).toEqual(["zulu", "alpha"]);
  });
});

describe("filterMonitors", () => {
  const list = [
    monitor("api", "up", {
      name: "API gateway",
      target: "https://api.example.com",
    }),
    monitor("db", "up", { name: "Postgres", target: "db.internal:5432" }),
    monitor("cdn", "up", {
      name: "CDN edge",
      target: "https://cdn.example.com",
    }),
  ];

  it("matches a substring of the name, case-insensitively", () => {
    expect(filterMonitors(list, "gate").map((m) => m.id)).toEqual(["api"]);
    expect(filterMonitors(list, "POSTGRES").map((m) => m.id)).toEqual(["db"]);
  });

  it("matches a substring of the target too", () => {
    expect(filterMonitors(list, "5432").map((m) => m.id)).toEqual(["db"]);
    expect(filterMonitors(list, "example.com").map((m) => m.id)).toEqual([
      "api",
      "cdn",
    ]);
  });

  it("returns everything for an empty or whitespace-only query", () => {
    expect(filterMonitors(list, "")).toHaveLength(3);
    expect(filterMonitors(list, "   ")).toHaveLength(3);
  });

  it("returns nothing when nothing matches, rather than a best guess", () => {
    expect(filterMonitors(list, "kubernetes")).toEqual([]);
  });

  it("preserves input order, leaving ordering to partition", () => {
    expect(filterMonitors(list, "e").map((m) => m.id)).toEqual([
      "api",
      "db",
      "cdn",
    ]);
  });
});

describe("summarise", () => {
  it("counts each status and the total", () => {
    expect(
      summarise([
        monitor("a", "up"),
        monitor("b", "up"),
        monitor("c", "down"),
        monitor("d", "pending"),
        monitor("e", "paused"),
        monitor("f", "waiting"),
      ]),
    ).toEqual({ up: 2, down: 1, pending: 1, paused: 1, waiting: 1, total: 6 });
  });

  it("returns zeroes for an empty list", () => {
    expect(summarise([])).toEqual({
      up: 0,
      down: 0,
      pending: 0,
      paused: 0,
      waiting: 0,
      total: 0,
    });
  });
});

describe("describeTransitions", () => {
  it("says nothing when no status changed", () => {
    const before = [monitor("api", "up"), monitor("db", "up")];
    // Same statuses, different latency and heartbeats: the routine tick that
    // must not produce an announcement.
    const after = [
      monitor("api", "up", { latencyMs: 900 }),
      monitor("db", "up", { latencyMs: 4 }),
    ];
    expect(describeTransitions(before, after)).toBeNull();
  });

  it("says nothing for an unchanged empty list", () => {
    expect(describeTransitions([], [])).toBeNull();
  });

  it("says nothing on first load, when there is no previous state", () => {
    // Every monitor is "new", not "changed"; announcing the whole list here
    // would talk over the page as it renders.
    expect(describeTransitions([], [monitor("api", "down")])).toBeNull();
  });

  it("names the monitors that are down and counts the rest", () => {
    const before = [
      monitor("api", "up"),
      monitor("db", "up"),
      monitor("cdn", "up"),
    ];
    const after = [
      monitor("api", "down"),
      monitor("db", "down"),
      monitor("cdn", "up"),
    ];
    expect(describeTransitions(before, after)).toBe(
      "2 monitors down: api, db. 1 up.",
    );
  });

  it("uses the singular for a single failure", () => {
    const before = [monitor("api", "up"), monitor("db", "up")];
    const after = [monitor("api", "down"), monitor("db", "up")];
    expect(describeTransitions(before, after)).toBe(
      "1 monitor down: api. 1 up.",
    );
  });

  it("announces recovery, because silence looks like a dead page", () => {
    const before = [monitor("api", "down"), monitor("db", "up")];
    const after = [monitor("api", "up"), monitor("db", "up")];
    expect(describeTransitions(before, after)).toBe("All 2 monitors up.");
  });

  it("mentions pending alongside the failures", () => {
    const before = [
      monitor("api", "up"),
      monitor("db", "up"),
      monitor("cdn", "up"),
    ];
    const after = [
      monitor("api", "down"),
      monitor("db", "pending"),
      monitor("cdn", "up"),
    ];
    expect(describeTransitions(before, after)).toBe(
      "1 monitor down: api. 1 up. 1 pending.",
    );
  });

  it("reports pending when nothing is down", () => {
    const before = [monitor("api", "up"), monitor("db", "up")];
    const after = [monitor("api", "up"), monitor("db", "pending")];
    expect(describeTransitions(before, after)).toBe(
      "No monitors down. 1 up, 1 pending.",
    );
  });

  it("caps the names it reads out and counts the remainder", () => {
    const ids = ["a", "b", "c", "d", "e", "f", "g"];
    const before = ids.map((id) => monitor(id, "up"));
    const after = ids.map((id) => monitor(id, "down"));
    // Reading 200 names is a filibuster, not an announcement.
    expect(describeTransitions(before, after)).toBe(
      "7 monitors down: a, b, c, d, e and 2 more. 0 up.",
    );
  });

  it("names down monitors alphabetically, independent of list order", () => {
    const before = [monitor("zulu", "up"), monitor("alpha", "up")];
    const after = [monitor("zulu", "down"), monitor("alpha", "down")];
    expect(describeTransitions(before, after)).toBe(
      "2 monitors down: alpha, zulu. 0 up.",
    );
  });

  it("announces a pause, since a paused monitor stops being watched", () => {
    const before = [monitor("api", "up")];
    const after = [monitor("api", "paused")];
    expect(describeTransitions(before, after)).toBe("All 1 monitor paused.");
  });
});

describe("describeFilter", () => {
  it("says nothing when no filter is on", () => {
    expect(describeFilter(14, 14, null, "   ")).toBeNull();
  });

  it("names the status on its own", () => {
    expect(describeFilter(2, 14, "down", "")).toBe("2 of 14 monitors are down");
  });

  it("joins status and query into one sentence", () => {
    expect(describeFilter(1, 14, "down", " api ")).toBe(
      "1 of 14 monitors is down and matches \u201Capi\u201D",
    );
  });

  it("leaves the prose about an empty result to EmptyState", () => {
    expect(describeFilter(0, 14, "paused", "")).toBe(
      "0 of 14 monitors are paused",
    );
  });

  it("keeps the singular of the total intact", () => {
    expect(describeFilter(1, 1, "up", "")).toBe("1 of 1 monitor is up");
  });
});

describe("filterByStatus", () => {
  const list = [
    monitor("api", "up"),
    monitor("db", "down"),
    monitor("cdn", "up"),
  ];

  it("returns a copy, not the input, when nothing is selected", () => {
    const out = filterByStatus(list, null);
    expect(out).toEqual([...list]);
    expect(out).not.toBe(list);
  });

  it("keeps only the chosen status", () => {
    const out = filterByStatus(list, "down");
    expect(out.length).toBeGreaterThan(0);
    expect(out.every((m) => m.status === "down")).toBe(true);
  });
});

describe("tagFacets", () => {
  it("collects every key with its distinct values", () => {
    const list = [
      monitor("api", "up", { tags: { env: "prod", customer: "acme" } }),
      monitor("db", "up", { tags: { env: "staging" } }),
      monitor("cdn", "up", { tags: { env: "prod" } }),
    ];
    expect(tagFacets(list)).toEqual([
      { key: "customer", values: ["acme"] },
      { key: "env", values: ["prod", "staging"] },
    ]);
  });

  it("orders keys and values the same whichever order the monitors arrive in", () => {
    const a = monitor("a", "up", { tags: { env: "prod" } });
    const b = monitor("b", "up", { tags: { env: "edge" } });
    expect(tagFacets([a, b])).toEqual(tagFacets([b, a]));
  });

  it("keeps values that differ only in case, since they are different tags", () => {
    const list = [
      monitor("a", "up", { tags: { env: "Prod" } }),
      monitor("b", "up", { tags: { env: "prod" } }),
    ];
    expect(tagFacets(list)[0].values).toHaveLength(2);
  });

  it("offers nothing when no monitor carries a tag", () => {
    expect(tagFacets([monitor("api", "up")])).toEqual([]);
  });
});

describe("filterByTags", () => {
  const list = [
    monitor("api", "up", { tags: { env: "prod", customer: "acme" } }),
    monitor("db", "up", { tags: { env: "prod", customer: "globex" } }),
    monitor("cdn", "up", { tags: { env: "staging", customer: "acme" } }),
  ];

  it("returns a copy, not the input, when nothing is chosen", () => {
    const out = filterByTags(list, {});
    expect(out).toEqual([...list]);
    expect(out).not.toBe(list);
  });

  it("keeps only monitors carrying the chosen pair", () => {
    expect(filterByTags(list, { env: "prod" }).map((m) => m.id)).toEqual([
      "api",
      "db",
    ]);
  });

  it("ANDs across keys rather than widening the list", () => {
    expect(
      filterByTags(list, { env: "prod", customer: "acme" }).map((m) => m.id),
    ).toEqual(["api"]);
  });

  it("treats an empty value as no choice for that key", () => {
    expect(filterByTags(list, { env: "" })).toHaveLength(3);
  });

  it("drops monitors that lack the key entirely", () => {
    const untagged = monitor("mail", "up");
    expect(
      filterByTags([...list, untagged], { env: "prod" }).map((m) => m.id),
    ).toEqual(["api", "db"]);
  });
});

describe("describeTags", () => {
  it("renders chosen pairs in key order", () => {
    expect(describeTags({ env: "prod", customer: "acme" })).toEqual([
      "customer:acme",
      "env:prod",
    ]);
  });

  it("ignores keys set to no choice", () => {
    expect(describeTags({ env: "", customer: "acme" })).toEqual([
      "customer:acme",
    ]);
  });
});

describe("describeFilter with tags", () => {
  it("names the tags on their own", () => {
    expect(describeFilter(3, 14, null, "", { env: "prod" })).toBe(
      "3 of 14 monitors are tagged env:prod",
    );
  });

  it("shares one copula with the status", () => {
    expect(describeFilter(2, 14, "down", "", { env: "prod" })).toBe(
      "2 of 14 monitors are down and tagged env:prod",
    );
  });

  it("lists several tags inside one clause", () => {
    expect(
      describeFilter(1, 14, null, "", { env: "prod", customer: "acme" }),
    ).toBe("1 of 14 monitors is tagged customer:acme, env:prod");
  });

  it("still says nothing when every tag is set to no choice", () => {
    expect(describeFilter(14, 14, null, "", { env: "" })).toBeNull();
  });
});

describe("groupByTag", () => {
  const tagged = (id: string, tags: Record<string, string>) =>
    monitor(id, "up", { tags });

  it("makes one group per value of the key, sorted by value", () => {
    const groups = groupByTag(
      [
        tagged("a", { env: "prod" }),
        tagged("b", { env: "dev" }),
        tagged("c", { env: "prod" }),
      ],
      "env",
    );
    expect(groups.map((g) => g.label)).toEqual(["dev", "prod"]);
    expect(groups[1].monitors.map((m) => m.id)).toEqual(["a", "c"]);
  });

  it("keeps monitors without the key in a trailing Untagged group", () => {
    const groups = groupByTag(
      [
        tagged("a", { env: "prod" }),
        tagged("b", {}),
        tagged("c", { team: "ops" }),
      ],
      "env",
    );
    expect(groups.map((g) => g.label)).toEqual(["prod", UNTAGGED_LABEL]);
    expect(groups[1].value).toBeNull();
    expect(groups[1].monitors.map((m) => m.id)).toEqual(["b", "c"]);
  });

  it("loses no monitor and duplicates none", () => {
    const input = [
      tagged("a", { env: "prod" }),
      tagged("b", { env: "dev" }),
      tagged("c", {}),
      tagged("d", { env: "prod" }),
    ];
    const flat = groupByTag(input, "env").flatMap((g) =>
      g.monitors.map((m) => m.id),
    );
    expect([...flat].sort()).toEqual(["a", "b", "c", "d"]);
  });

  it("sorts monitors inside a group by name, not by input order", () => {
    const groups = groupByTag(
      [tagged("zeta", { env: "prod" }), tagged("alpha", { env: "prod" })],
      "env",
    );
    expect(groups[0].monitors.map((m) => m.id)).toEqual(["alpha", "zeta"]);
  });

  it("keeps values differing only in case apart, in a stable order", () => {
    const groups = groupByTag(
      [tagged("a", { env: "Prod" }), tagged("b", { env: "prod" })],
      "env",
    );
    expect(groups).toHaveLength(2);
    expect(groups.map((g) => g.label)).toEqual(["Prod", "prod"]);
  });

  it("returns no groups for an empty list", () => {
    expect(groupByTag([], "env")).toEqual([]);
  });

  it("puts everything under Untagged when no monitor carries the key", () => {
    const groups = groupByTag([tagged("a", { team: "ops" })], "env");
    expect(groups.map((g) => g.label)).toEqual([UNTAGGED_LABEL]);
  });
});

describe("sectionsByTag", () => {
  it("lifts down monitors into their own leading section, out of the groups", () => {
    const sections = sectionsByTag(
      [
        monitor("a", "up", { tags: { env: "prod" } }),
        monitor("b", "down", { tags: { env: "prod" } }),
        monitor("c", "up", { tags: { env: "dev" } }),
      ],
      "env",
    );
    expect(sections.map((s) => [s.label, s.monitors.map((m) => m.id)])).toEqual(
      [
        ["Needs attention", ["b"]],
        ["dev", ["c"]],
        ["prod", ["a"]],
      ],
    );
    expect(sections[0].attention).toBe(true);
  });

  it("omits the attention section when nothing is down", () => {
    const sections = sectionsByTag(
      [monitor("a", "up", { tags: { env: "prod" } })],
      "env",
    );
    expect(sections.map((s) => s.label)).toEqual(["prod"]);
  });

  it("gives every section a distinct key", () => {
    const sections = sectionsByTag(
      [
        monitor("a", "down", { tags: { env: "prod" } }),
        monitor("b", "up", { tags: { env: "prod" } }),
        monitor("c", "up", {}),
      ],
      "env",
    );
    expect(new Set(sections.map((s) => s.id)).size).toBe(sections.length);
  });

  it("gives every section an id usable as a DOM identifier", () => {
    const sections = sectionsByTag(
      [monitor("a", "up", { tags: { region: "US East" } })],
      "region",
    );
    // The id lands in `id`/`aria-labelledby`, where whitespace would split it
    // into two tokens that match nothing.
    for (const section of sections) {
      expect(section.id).not.toMatch(/\s/);
    }
    expect(sections.map((s) => s.id)).toEqual(["tag:US%20East"]);
  });

  it("renders every monitor exactly once across the sections", () => {
    const input = [
      monitor("a", "down", { tags: { env: "prod" } }),
      monitor("b", "up", { tags: { env: "prod" } }),
      monitor("c", "up", {}),
      monitor("d", "paused", { tags: { env: "dev" } }),
    ];
    const flat = sectionsByTag(input, "env").flatMap((s) =>
      s.monitors.map((m) => m.id),
    );
    expect([...flat].sort()).toEqual(["a", "b", "c", "d"]);
  });
});
