import { useId } from "react";
import type { ReactNode } from "react";
import { useCompactViewport } from "../layout/useMediaQuery";
import { Led } from "./Led";
import { MonitorCardList } from "./MonitorCardList";
import { MonitorTable } from "./MonitorTable";
import { ROW_BEAT_WIDTH } from "./MonitorRow";
import { filterMonitors, summarise } from "./model";
import type { Monitor, MonitorStatus } from "./types";

/**
 * The dashboard shell: counts, search, the live region and the table.
 *
 * Deliberately presentational. It fetches nothing and owns no timers — data
 * arrives as props and the search query is a controlled value, so the same
 * component renders a demo fixture, a server-rendered payload or a live SSE
 * stream (SUB-26) without changing a line of it.
 */

export type DashboardProps = {
  monitors: readonly Monitor[];
  /** Controlled search query. */
  query: string;
  onQueryChange: (query: string) => void;
  /**
   * The live-region sentence, or null for silence.
   *
   * Passed in rather than derived here because it depends on the *previous*
   * data, which only the data owner has. Debouncing belongs there too: this
   * component says what it is handed, when it is handed it.
   */
  announcement?: string | null;
  /** Explicit heartbeat width; required in jsdom, which has no layout. */
  beatWidth?: number;
  /**
   * Forces the row or card layout instead of asking the viewport.
   *
   * The layout is normally chosen by a media query. This escape hatch exists
   * for tests (jsdom has no `matchMedia`) and for the workbench, where both
   * layouts have to be judged side by side on one desktop screen.
   */
  compact?: boolean;
  /**
   * Chrome about the data itself, e.g. the connection badge.
   *
   * A slot rather than a `connectionStatus` prop: the dashboard renders
   * monitors and should not grow an opinion about transports. Whoever owns the
   * data owns the statement about it, and passes it in.
   */
  banner?: ReactNode;
};

const COUNTED: { status: MonitorStatus; label: string }[] = [
  { status: "down", label: "down" },
  { status: "pending", label: "pending" },
  { status: "paused", label: "paused" },
  { status: "up", label: "up" },
];

export function Dashboard({
  monitors,
  query,
  onQueryChange,
  announcement = null,
  beatWidth,
  compact,
  banner = null,
}: DashboardProps) {
  const searchId = useId();
  // Hooks cannot be skipped, so the query is always subscribed to and the
  // override wins afterwards.
  const narrow = useCompactViewport();
  const useCards = compact ?? narrow;
  // Derived during render, not mirrored into state: the filtered list is a
  // function of props and holding a copy would only create a way for the two
  // to disagree.
  const summary = summarise(monitors);
  const visible = filterMonitors(monitors, query);

  return (
    <section className="mon-dashboard" aria-labelledby={`${searchId}-title`}>
      {/* Above the counts, not below the list: a warning that the numbers are
          frozen has to be read *before* the numbers, not after scrolling past
          them. */}
      {banner}

      <header className="mon-topbar">
        <div>
          <h2 id={`${searchId}-title`} className="mon-title">
            Monitors
          </h2>
          <p className="mon-counts">
            {summary.total === 0 ? (
              "Nothing being watched yet"
            ) : (
              <>
                {COUNTED.filter(({ status }) => summary[status] > 0).map(({ status, label }) => (
                  <span key={status} className="mon-count">
                    <Led status={status} labelled={false} />
                    <b className="mon-count-value">{summary[status]}</b> {label}
                  </span>
                ))}
              </>
            )}
          </p>
        </div>

        <div className="mon-search">
          {/* A real <label>, hidden. Placeholder-as-label disappears the
              moment someone types, which is when they most need it. */}
          <label htmlFor={searchId} className="sr-only">
            Search monitors by name or target
          </label>
          <input
            id={searchId}
            type="search"
            className="mon-search-input"
            value={query}
            placeholder="Search monitors…"
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => onQueryChange(event.target.value)}
          />
        </div>
      </header>

      {/*
       * The single live region, and it lives *outside* the table
       * (research note 3). `aria-live` on the table itself would make a
       * screen reader re-read rows on every heartbeat tick, which is both
       * unusable and drowns out the one announcement that matters. This region
       * carries status transitions only; the caller decides when to fill it.
       */}
      <div role="status" aria-atomic="true" className="sr-only">
        {announcement ?? ""}
      </div>

      {/* Filtering is not announced through the live region: a result count
          that updates as you type belongs next to the input, where it does not
          interrupt. */}
      {query.trim() !== "" && monitors.length > 0 && (
        <p className="mon-result-count">
          {visible.length} of {monitors.length} monitors match “{query.trim()}”
        </p>
      )}

      {/*
       * Two components, one breakpoint. Rendering both and hiding one with CSS
       * would keep 200 rows *and* 200 cards in the DOM, double every heartbeat
       * bar's ResizeObserver, and hand a screen reader the same monitor twice.
       */}
      {useCards ? (
        <MonitorCardList
          monitors={visible}
          query={query}
          totalCount={monitors.length}
          beatWidth={beatWidth}
        />
      ) : (
        <MonitorTable
          monitors={visible}
          query={query}
          totalCount={monitors.length}
          beatWidth={beatWidth ?? ROW_BEAT_WIDTH}
        />
      )}
    </section>
  );
}
