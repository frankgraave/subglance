import { EmptyState } from "./EmptyState";
import { MonitorRow, ROW_BEAT_WIDTH } from "./MonitorRow";
import { partition } from "./model";
import type { Monitor } from "./types";

/**
 * The monitor list, as a real table.
 *
 * A genuine `<table>` with `<caption>`, `<th scope="col">` and a
 * `<th scope="row">` per row — not `role="grid"`, not a list of divs
 * (research note 2). The data is a grid of facts about a set of things, which
 * is what tables are for, and native table semantics give row/column
 * announcements, Ctrl-F and browser find-in-page for free. `role="grid"` would
 * instead promise spreadsheet keyboard navigation we do not implement.
 *
 * Note for anyone styling this: do **not** put `display: flex`, `grid` or
 * `contents` on any of these elements. Safari drops table semantics entirely
 * when you do, which silently undoes everything above. The layout uses
 * `table-layout: fixed` with a `<colgroup>` instead.
 */

export type MonitorTableProps = {
  monitors: readonly Monitor[];
  /** Non-empty when the list has been filtered, used only for empty-state copy. */
  query?: string;
  /** Total before filtering, so "no results" can be told from "no monitors". */
  totalCount?: number;
  /** Explicit heartbeat width; required in jsdom, which has no layout. */
  beatWidth?: number;
};

function Columns() {
  return (
    <colgroup>
      <col className="mon-col--led" />
      <col className="mon-col--name" />
      <col className="mon-col--beats" />
      <col className="mon-col--num" />
      <col className="mon-col--num" />
    </colgroup>
  );
}

function Head() {
  return (
    <thead>
      <tr>
        {/* The lamp column's header is text-only: the column holds a status,
            and a blank <th> leaves a screen reader announcing nothing. */}
        <th scope="col" className="mon-head">
          <span className="sr-only">Status</span>
        </th>
        <th scope="col" className="mon-head">
          Monitor
        </th>
        <th scope="col" className="mon-head">
          Last checks
        </th>
        <th scope="col" className="mon-head mon-head--num">
          Latency
        </th>
        <th scope="col" className="mon-head mon-head--num">
          24h
        </th>
      </tr>
    </thead>
  );
}

export function MonitorTable({
  monitors,
  query = "",
  totalCount,
  beatWidth = ROW_BEAT_WIDTH,
}: MonitorTableProps) {
  const total = totalCount ?? monitors.length;

  if (monitors.length === 0) {
    return <EmptyState query={query} totalCount={total} />;
  }

  const { attention, rest } = partition(monitors);
  const caption =
    attention.length > 0
      ? `${monitors.length} monitors. ${attention.length} needing attention are listed first, the rest alphabetically by name.`
      : `${monitors.length} monitors, alphabetically by name.`;

  const rows = (list: readonly Monitor[]) =>
    list.map((monitor) => (
      <MonitorRow key={monitor.id} monitor={monitor} beatWidth={beatWidth} />
    ));

  return (
    <div className="mon-board">
      <table className="mon-table">
        {/* Visually hidden, but the table's accessible name and the one place
            the ordering rule is stated for someone who cannot see it. */}
        <caption className="sr-only">{caption}</caption>
        <Columns />
        <Head />

        {/*
         * Two tbodies rather than two tables: one table means one set of
         * column headers and one consistent width, and a screen reader still
         * reports a single list of rows. Only this first section reorders as
         * status changes; the main list below stays put (research note 4).
         */}
        {attention.length > 0 && (
          <tbody className="mon-section mon-section--attention">
            <tr className="mon-section-head">
              <th scope="colgroup" colSpan={5} className="mon-section-title">
                Needs attention ({attention.length})
              </th>
            </tr>
            {rows(attention)}
          </tbody>
        )}

        <tbody className="mon-section">
          {attention.length > 0 && (
            <tr className="mon-section-head">
              <th scope="colgroup" colSpan={5} className="mon-section-title">
                All monitors ({rest.length})
              </th>
            </tr>
          )}
          {rows(rest)}
        </tbody>
      </table>
    </div>
  );
}
