import { useId, useState } from "react";
import { IncidentStoryItem } from "./IncidentStoryItem";
import { describeCluster } from "./cluster";
import { formatClock } from "./story";
import { Value } from "../components/Value";
import type { IncidentCluster } from "./cluster";
import type { Incident } from "../monitors/detail";

/** Shared empty default: a new Set per render would break memoisation. */
const EMPTY_ACKING: ReadonlySet<string> = new Set();

/**
 * Several monitors that started failing at once, offered as one row.
 *
 * The list stays chronological — this sits exactly where its newest member
 * would have sat — and the group is additive rather than a mode: opening it
 * yields precisely the rows that would have been there ungrouped, in the same
 * order, with the same controls. Nothing is hidden, nothing is reordered, and
 * a reader who thinks the grouping is wrong loses nothing by ignoring it.
 *
 * **It states a suspicion, and it has to look like one.** SubGlance has no
 * dependency graph: all it knows is that four things broke inside a minute.
 * So the row is drawn as a `chip--state` — dashed, which in this product
 * already means *about the data rather than the data* (DESIGN.md §8.1) — and
 * the sentence says the inference is from timing alone. A solid status chip
 * here would make a guess wear the clothes of a measurement.
 *
 * **The count is readable without opening the group.** It is in the visible
 * summary and in the accessible name of the control, so neither a sighted
 * reader nor a screen reader has to expand a disclosure to find out how many
 * incidents it is standing in front of. A group whose size is a surprise is a
 * group that hides things.
 */

export type IncidentClusterItemProps = {
  cluster: IncidentCluster;
  now: number;
  names: Readonly<Record<string, string>>;
  onAck?: (id: string) => void;
  /** Incidents whose ack request is still in flight. */
  ackingIds?: ReadonlySet<string>;
  stale?: boolean;
  past?: boolean;
};

export function IncidentClusterItem({
  cluster,
  now,
  names,
  onAck,
  ackingIds = EMPTY_ACKING,
  stale = false,
  past = false,
}: IncidentClusterItemProps) {
  /*
   * Open by default while anything in the group is still broken.
   *
   * A collapsed group during a live incident would answer "what is happening"
   * with a summary and make the reader click for the facts — at exactly the
   * moment they can least afford it. Once everything in the group has
   * recovered it is history, and history may start folded.
   */
  const [open, setOpen] = useState(() =>
    cluster.items.some((incident) => !incident.resolved),
  );
  const detailId = useId();
  const count = cluster.items.length;
  const label = `${count} incidents, ${cluster.monitorCount} monitors`;

  return (
    <li className="inc-cluster" data-open={open ? "true" : "false"}>
      <button
        type="button"
        className="inc-line inc-cluster-line"
        aria-expanded={open}
        aria-controls={detailId}
        /*
         * The accessible name carries the count and the caveat, because the
         * visible summary below is `aria-hidden` to stop a screen reader
         * hearing the same sentence twice. Without this the control would
         * announce as an unnamed expander over an unknown number of rows.
         */
        aria-label={`${label} — ${describeCluster(cluster)}`}
        onClick={() => setOpen((was) => !was)}
      >
        <span className="inc-line-inner" aria-hidden="true">
          <span className="chip chip--state inc-cluster-badge">
            {count} incidents
          </span>
          <span className="inc-main">
            <span className="inc-name">
              {cluster.monitorCount} monitors started failing together
            </span>
            <span className="inc-sub inc-cluster-why">
              {describeCluster(cluster)}
            </span>
          </span>
          <Value className="inc-col inc-col-time">
            {formatClock(cluster.at) ?? "—"}
          </Value>
        </span>
      </button>

      {/*
       * Rendered only when open. An off-screen copy would keep its rows
       * focusable and its text readable to a screen reader, which is the usual
       * way a disclosure is got wrong — and here it would mean every incident
       * being announced twice.
       */}
      {open ? (
        <ul className="inc-list inc-cluster-items" id={detailId}>
          {cluster.items.map((incident: Incident) => (
            <IncidentStoryItem
              key={incident.id}
              incident={incident}
              now={now}
              subject={
                names[incident.monitorId] ?? `Monitor ${incident.monitorId}`
              }
              onAck={onAck}
              acking={ackingIds.has(incident.id)}
              stale={stale}
              past={past || incident.resolved}
            />
          ))}
        </ul>
      ) : null}
    </li>
  );
}
