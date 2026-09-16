import { describe, expect, it } from "vitest";
import {
  CLUSTER_WINDOW_MS,
  clusterIncidents,
  describeCluster,
} from "./cluster";
import type { IncidentCluster } from "./cluster";
import type { Incident } from "../monitors/detail";

/**
 * Grouping that must not tell a lie.
 *
 * Frank asked for both truths: the chronological list stays the default, and a
 * grouping appears on top of it only where it means something. The hard
 * constraint the design proposal put on that — and the one this file exists to
 * enforce — is that **a cluster is a suspicion drawn from timing, not a
 * finding about cause**, and the individual incidents must always remain
 * reachable.
 *
 * So the assertions come in three kinds:
 *
 * 1. The chronology survives grouping. Flattening the entries gives back the
 *    same incidents in the same order.
 * 2. The heuristic only fires where it is defensible: several *distinct*
 *    monitors, close together.
 * 3. The wording states the inference as an inference, and never claims a
 *    shared cause.
 */

const T0 = 1_700_000_000_000;

const incident = (
  id: string,
  monitorId: string,
  startedAt: number | null,
  over: Partial<Incident> = {},
): Incident => ({
  id,
  monitorId,
  startedAt,
  confirmedAt: startedAt === null ? null : startedAt + 60_000,
  resolvedAt: null,
  ackedAt: null,
  confirmed: true,
  resolved: false,
  acked: false,
  durationS: 600,
  ...over,
});

/** Every incident in the entries, in the order they are rendered. */
function flatten(entries: ReturnType<typeof clusterIncidents>): Incident[] {
  return entries.flatMap((entry) =>
    entry.kind === "cluster" ? entry.items : [entry.incident],
  );
}

describe("the chronology survives the grouping", () => {
  it("returns every incident, newest first, grouped or not", () => {
    const list = [
      incident("a", "1", T0),
      incident("b", "2", T0 - 10_000),
      incident("c", "3", T0 - 20_000),
      incident("d", "4", T0 - 10 * 60_000),
    ];
    const flat = flatten(clusterIncidents(list));
    expect(flat.map((i) => i.id)).toEqual(["a", "b", "c", "d"]);
  });

  it("loses nothing when there is nothing to group", () => {
    const list = [
      incident("a", "1", T0),
      incident("b", "2", T0 - 10 * 60_000),
      incident("c", "3", T0 - 20 * 60_000),
    ];
    const entries = clusterIncidents(list);
    expect(entries.every((e) => e.kind === "single")).toBe(true);
    expect(flatten(entries)).toHaveLength(3);
  });

  it("keeps an undated incident in the list rather than dropping it", () => {
    // Grouping is entirely an argument about timing, so an undated row cannot
    // take part in one — but it is still an incident, and a list that silently
    // omits incidents is the worst possible bug on this screen.
    const entries = clusterIncidents([
      incident("a", "1", T0),
      incident("nodate", "2", null),
    ]);
    expect(flatten(entries).map((i) => i.id)).toContain("nodate");
  });
});

describe("the heuristic fires only where it is defensible", () => {
  it("groups several monitors that failed within the window", () => {
    const entries = clusterIncidents([
      incident("a", "1", T0),
      incident("b", "2", T0 - 20_000),
      incident("c", "3", T0 - 40_000),
    ]);
    expect(entries).toHaveLength(1);
    expect(entries[0].kind).toBe("cluster");
    const cluster = entries[0] as IncidentCluster;
    expect(cluster.monitorCount).toBe(3);
    expect(cluster.items).toHaveLength(3);
  });

  it("refuses to group one monitor's own repeated outages", () => {
    /*
     * The assertion that keeps grouping honest about flapping.
     *
     * One monitor failing three times in a minute is a flapping monitor, which
     * has its own sentence already (`describeChurn`). Calling it "3 monitors
     * failed together" would be false; calling it "one event" would send the
     * reader hunting for a shared dependency that does not exist.
     */
    const entries = clusterIncidents([
      incident("a", "7", T0),
      incident("b", "7", T0 - 20_000),
      incident("c", "7", T0 - 40_000),
    ]);
    expect(entries.every((e) => e.kind === "single")).toBe(true);
  });

  it("does not group incidents that are far apart", () => {
    const entries = clusterIncidents([
      incident("a", "1", T0),
      incident("b", "2", T0 - 5 * CLUSTER_WINDOW_MS),
    ]);
    expect(entries.every((e) => e.kind === "single")).toBe(true);
  });

  it("chains through a staggered failure rather than cutting it in half", () => {
    /*
     * Six monitors on a 30s interval notice a dead dependency as each one next
     * probes, so the event can span two minutes end to end while no two
     * adjacent failures are more than 30s apart. Anchoring the window on the
     * first member would split one event into two clusters at an arbitrary
     * point.
     */
    const entries = clusterIncidents([
      incident("a", "1", T0),
      incident("b", "2", T0 - 40_000),
      incident("c", "3", T0 - 80_000),
      incident("d", "4", T0 - 120_000),
    ]);
    expect(entries).toHaveLength(1);
    expect((entries[0] as IncidentCluster).items).toHaveLength(4);
  });

  it("reports the span, so an over-eager chain announces itself", () => {
    // The cost of chaining is that a slow drip could weld into one enormous
    // group. The span is what lets the reader see that and discount it, which
    // is why it is measured rather than merely used internally.
    const entries = clusterIncidents([
      incident("a", "1", T0),
      incident("b", "2", T0 - 40_000),
      incident("c", "3", T0 - 80_000),
    ]);
    expect((entries[0] as IncidentCluster).spanMs).toBe(80_000);
  });

  it("puts the cluster where its newest member was", () => {
    // The grouping changes the shape of the list, never its sequence.
    const entries = clusterIncidents([
      incident("new", "1", T0),
      incident("pair1", "2", T0 - 10 * 60_000),
      incident("pair2", "3", T0 - 10 * 60_000 - 20_000),
    ]);
    expect(entries[0].kind).toBe("single");
    expect(entries[1].kind).toBe("cluster");
  });
});

describe("the wording states an inference, not a finding", () => {
  const cluster = clusterIncidents([
    incident("a", "1", T0),
    incident("b", "2", T0 - 20_000),
    incident("c", "3", T0 - 40_000),
  ])[0] as IncidentCluster;

  it("says what was measured: how many monitors, over what span", () => {
    const note = describeCluster(cluster);
    expect(note).toContain("3 monitors");
    expect(note).toMatch(/within/);
  });

  it("names the guess as a guess", () => {
    /*
     * The constraint the design proposal set, asserted rather than trusted.
     * SubGlance has no dependency graph — it has timestamps — and a sentence
     * implying otherwise is exactly the confident fiction this product exists
     * to avoid.
     */
    const note = describeCluster(cluster);
    expect(note).toMatch(/inferring|inferred/i);
    expect(note).toMatch(/timing/i);
  });

  it("never claims a shared cause outright", () => {
    const note = describeCluster(cluster);
    expect(note).not.toMatch(/caused by/i);
    expect(note).not.toMatch(/\brelated\b/i);
    // "one outage" would merge four incidents into a single event by fiat.
    expect(note).not.toMatch(/one outage/i);
  });

  it("invites the reader to check the evidence", () => {
    // An inference the reader cannot refute is just an assertion in a quieter
    // voice. The individual rows are the evidence, and the sentence points at
    // them.
    expect(describeCluster(cluster)).toMatch(/open the group/i);
  });
});

describe("a flapping monitor inside a mixed run", () => {
  /*
   * Found by review, reproduced before it was fixed. The old guard only
   * rejected a run where *every* incident was the same monitor, so a mixed
   * run kept the repeats: monitor 7 failing three times inside a minute
   * beside one failure of monitor 8 produced a four-item cluster announcing
   * "2 monitors started failing together".
   *
   * Two lies in one row. The count disagreed with the contents, and one
   * flapping monitor was offered as evidence of a shared cause while
   * `describeChurn` was simultaneously explaining it as churn — the screen
   * telling two incompatible stories about the same three incidents.
   */
  const base = T0;
  const list = [
    incident("a", "7", base),
    incident("b", "7", base - 20_000),
    incident("c", "8", base - 30_000),
    incident("d", "7", base - 40_000),
  ];

  it("holds one incident per monitor, so the count matches the contents", () => {
    const clusters = clusterIncidents(list).filter((e) => e.kind === "cluster");
    for (const group of clusters) {
      if (group.kind !== "cluster") continue;
      const monitors = group.items.map((i) => i.monitorId);
      expect(new Set(monitors).size).toBe(monitors.length);
      expect(group.monitorCount).toBe(group.items.length);
    }
  });

  it("emits the repeats as singles rather than dropping them", () => {
    const entries = clusterIncidents(list);
    const ids = entries.flatMap((e) =>
      e.kind === "cluster" ? e.items.map((i) => i.id) : [e.incident.id],
    );
    // Nothing invented, nothing lost: the same four incidents come back.
    expect(ids.sort()).toEqual(["a", "b", "c", "d"]);
  });
});

describe("a repeat must not bridge two unrelated monitors", () => {
  /*
   * Found by review, as a follow-on defect of the fix above: repeats were
   * removed *after* the chain was built, so they still advanced it.
   *
   * A at 0s, B at 50s, B again at 100s, C at 150s formed one run. Dropping
   * the repeat then produced a row claiming A, B and C "started failing
   * together" — three monitors spanning 150 seconds inside a 60-second
   * window. One monitor's flapping was carrying two unrelated monitors into
   * the same sentence, which is exactly the inference the window is there to
   * bound.
   */
  const list = [
    incident("C", "3", T0 + 150_000),
    incident("B2", "2", T0 + 100_000),
    incident("B1", "2", T0 + 50_000),
    incident("A", "1", T0),
  ];

  it("keeps every cluster inside the window it claims", () => {
    for (const entry of clusterIncidents(list)) {
      if (entry.kind !== "cluster") continue;
      const times = entry.items
        .map((i) => i.startedAt ?? 0)
        .sort((a, b) => a - b);
      const span = times[times.length - 1] - times[0];
      expect(span).toBeLessThanOrEqual(CLUSTER_WINDOW_MS);
      // And spanMs must be the truth about that group, not a leftover.
      expect(entry.spanMs).toBe(span);
    }
  });

  it("does not put A and C in one group", () => {
    const groups = clusterIncidents(list)
      .filter((e) => e.kind === "cluster")
      .map((e) => (e.kind === "cluster" ? e.items.map((i) => i.id) : []));
    for (const ids of groups) {
      expect(ids.includes("A") && ids.includes("C")).toBe(false);
    }
  });

  it("still loses nothing", () => {
    const ids = clusterIncidents(list).flatMap((e) =>
      e.kind === "cluster" ? e.items.map((i) => i.id) : [e.incident.id],
    );
    expect(ids.sort()).toEqual(["A", "B1", "B2", "C"]);
  });
});
