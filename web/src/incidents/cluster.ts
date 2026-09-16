/**
 * Incidents that probably belong to one event.
 *
 * The list is chronological, newest first, and that is the right default: at
 * 03:00 the question is "what just happened", and any other order makes the
 * reader hunt for the newest row. But a shared dependency failing takes four
 * monitors down at once, and four chronological rows are then four views of
 * one event — a shape the reader has to reconstruct from timestamps.
 *
 * So the grouping is *additive*, never a mode. A cluster appears only where it
 * says something, it sits at the position its incidents already occupied in
 * the chronology, and opening it shows exactly the rows that would have been
 * there without it. Nothing is hidden and nothing is reordered; the only thing
 * that changes is that a pattern which was implicit becomes stated.
 *
 * ---
 *
 * **A cluster is a suspicion, not a finding, and the wording has to carry that
 * difference.**
 *
 * This is the constraint the design proposal put on grouping, and it is the
 * one that decides the whole shape: SubGlance has no dependency graph, no
 * topology and no causal link between monitors. All it has is that four things
 * started failing inside a minute. That is strong evidence and it is not
 * proof — two unrelated services can fail at the same moment, and a deploy
 * that restarts six containers produces exactly this signature whether or not
 * anything is actually wrong downstream.
 *
 * So the cluster never says "caused by" or "related". It says what it
 * measured: how many monitors, inside what span, and that the grouping is
 * inferred from timing. A reader who disagrees with the inference can open the
 * cluster and get the four rows, unchanged.
 */

import { formatDuration } from "../monitors/detail";
import type { Incident } from "../monitors/detail";

/**
 * How close together two outages have to start to be worth grouping.
 *
 * 60 seconds, the figure the design proposal names. It is short enough that
 * ordinary bad luck rarely fills it — a monitor checks on a 60s interval by
 * default, so two unrelated failures landing inside one window is roughly the
 * chance of two unrelated failures in one check cycle — and long enough to
 * cover the real case, where a dependency dies and each monitor notices it on
 * its own next probe rather than simultaneously.
 */
export const CLUSTER_WINDOW_MS = 60_000;

/**
 * How many *distinct monitors* make a cluster.
 *
 * Distinct, and that word is load-bearing. One monitor with three incidents in
 * a minute is a flapping monitor, which is a different fact with a different
 * sentence already written for it (`describeChurn`). Grouping those as "one
 * event" would say something true in a way that sends the reader looking for a
 * shared dependency that does not exist.
 */
export const CLUSTER_MIN_MONITORS = 2;

export type IncidentCluster = {
  kind: "cluster";
  /** Stable across renders: the earliest member's id. */
  key: string;
  /** Members, newest first, exactly as they would appear ungrouped. */
  items: Incident[];
  /** How many different monitors are involved. */
  monitorCount: number;
  /** Milliseconds between the first and last member starting. */
  spanMs: number;
  /** Where this sits in the chronology: its newest member's start. */
  at: number;
};

export type IncidentEntry =
  | { kind: "single"; key: string; incident: Incident; at: number }
  | IncidentCluster;

/**
 * The chronological list, with clusters folded in where they exist.
 *
 * Newest first throughout. A cluster takes the position of its newest member,
 * so the row order a reader sees is the row order they would have seen
 * ungrouped — the grouping changes the shape of the list, never its sequence.
 *
 * Incidents with no start time are never clustered: the grouping is entirely
 * an argument about timing, and an undated row cannot participate in one. They
 * stay in the list as singles rather than being dropped, because an incident
 * we cannot date is still an incident.
 */
export function clusterIncidents(
  incidents: readonly Incident[],
  windowMs: number = CLUSTER_WINDOW_MS,
): IncidentEntry[] {
  const dated = [...incidents]
    .filter((incident) => incident.startedAt !== null)
    .sort((a, b) => (b.startedAt ?? 0) - (a.startedAt ?? 0));
  const undated = incidents.filter((incident) => incident.startedAt === null);

  const entries: IncidentEntry[] = [];
  let index = 0;
  while (index < dated.length) {
    /*
     * Walk forward while each next incident is within the window of the last
     * *kept* one — not of the group's first member, and not of a repeat.
     *
     * Chained rather than anchored on purpose. A dependency failing takes its
     * monitors down as each one next probes, so six monitors on a 30s interval
     * can span well over a minute end to end while no two adjacent failures
     * are more than a few seconds apart. Anchoring on the first member would
     * cut that event in half at an arbitrary point.
     *
     * A repeat from a monitor already in the run is collected but does **not**
     * advance the chain. Letting it advance built a bridge: A at 0s, B at 50s,
     * B again at 100s and C at 150s formed one run, and after the repeat was
     * dropped the row claimed A, B and C "started failing together" — three
     * monitors spanning 150 seconds inside a 60-second window. One monitor's
     * flapping was carrying two unrelated monitors into the same sentence,
     * which is precisely the inference this window exists to bound.
     *
     * The remaining risk of chaining is the opposite error — a slow drip of
     * unrelated failures, each just inside the window of the last, welding
     * into one enormous "cluster". That is what `spanMs` is for: it is stated
     * on the row, so a cluster spanning twenty minutes announces itself as one
     * and the reader can judge the inference rather than being handed a
     * verdict.
     */
    const firstPerMonitor: Incident[] = [dated[index]];
    const repeats: Incident[] = [];
    const seen = new Set<string>([dated[index].monitorId]);
    let lastKept = dated[index].startedAt ?? 0;
    let cursor = index + 1;

    while (
      cursor < dated.length &&
      lastKept - (dated[cursor].startedAt ?? 0) <= windowMs
    ) {
      const candidate = dated[cursor];
      if (seen.has(candidate.monitorId)) {
        repeats.push(candidate);
      } else {
        seen.add(candidate.monitorId);
        firstPerMonitor.push(candidate);
        lastKept = candidate.startedAt ?? 0;
      }
      cursor += 1;
    }

    /*
     * One incident per monitor in the cluster; a monitor's repeats stay
     * singles.
     *
     * DESIGN.md states the rule: clustering is limited to distinct monitors,
     * and repeated outages from one monitor are never clustered. Keeping the
     * first (newest) incident per monitor is the honest representative — it is
     * the one the reader would open — and the repeats are emitted alongside,
     * where `describeChurn` explains them in the words that actually fit.
     */
    if (firstPerMonitor.length >= CLUSTER_MIN_MONITORS) {
      const newest = firstPerMonitor[0].startedAt ?? 0;
      const oldest = firstPerMonitor[firstPerMonitor.length - 1].startedAt ?? 0;
      entries.push({
        kind: "cluster",
        key: `cluster-${firstPerMonitor[firstPerMonitor.length - 1].id}`,
        items: firstPerMonitor,
        monitorCount: firstPerMonitor.length,
        spanMs: newest - oldest,
        at: newest,
      });
      for (const incident of repeats) {
        entries.push({
          kind: "single",
          key: incident.id,
          incident,
          at: incident.startedAt ?? 0,
        });
      }
    } else {
      /*
       * A run of one, or a run that is all the same monitor. Emitted as
       * singles rather than as a one-row cluster: a cluster row that wraps a
       * single incident adds a disclosure to click through for no information,
       * and a run of one monitor's own incidents is flapping, which
       * `describeChurn` already explains in the right words.
       */
      for (const incident of [...firstPerMonitor, ...repeats].sort(
        (a, b) => (b.startedAt ?? 0) - (a.startedAt ?? 0),
      )) {
        entries.push({
          kind: "single",
          key: incident.id,
          incident,
          at: incident.startedAt ?? 0,
        });
      }
    }
    index = cursor;
  }

  for (const incident of undated) {
    entries.push({ kind: "single", key: incident.id, incident, at: 0 });
  }

  return entries;
}

/**
 * What a cluster claims, in words that show it is an inference.
 *
 * Every part of this sentence is something that was measured: the number of
 * monitors, the span, and nothing else. "Started failing within 55s of each
 * other" is a fact. "Often one shared cause" is flagged as the guess it is,
 * and the invitation to open the group is what makes the guess refutable —
 * the reader gets the evidence, not just the conclusion.
 *
 * Deliberately never "related", "caused by" or "one outage". SubGlance has no
 * dependency graph; it has timestamps, and a sentence that implies otherwise
 * is the sort of confident fiction this product exists to avoid.
 */
export function describeCluster(cluster: IncidentCluster): string {
  const span =
    cluster.spanMs < 1000 ? "the same second" : formatDuration(cluster.spanMs / 1000);
  return (
    `${cluster.monitorCount} monitors started failing within ${span} of ` +
    "each other — often one shared cause, but SubGlance is inferring that " +
    "from the timing alone. Open the group to judge for yourself."
  );
}
