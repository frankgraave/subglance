import { useId, useState } from "react";
import type { ReactNode } from "react";
import { useCompactViewport } from "../layout/useMediaQuery";
import {
  DEFAULT_LAYOUT,
  effectiveLayout,
  type LayoutId,
} from "../shell/preferences";
import { Led } from "./Led";
import { MonitorCardList } from "./MonitorCardList";
import { MonitorCompactList } from "./MonitorCompactList";
import { MonitorTable } from "./MonitorTable";
import { ROW_BEAT_WIDTH } from "./MonitorRow";
import {
  describeFilter,
  filterByStatus,
  filterMonitors,
  summarise,
} from "./model";
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
   * Which of the three list layouts to render.
   *
   * A user setting handed down as a prop, not a breakpoint (DESIGN.md §7) —
   * but the viewport still gets a veto: `rows` and `compact` both put five
   * facts on one line, which is exactly what does not fit below 640px, so
   * `effectiveLayout` downgrades them to cards there. `wall` is not rendered
   * here; the shell swaps this whole component out for the wall.
   */
  layout?: LayoutId;
  /**
   * Chrome about the data itself, e.g. the connection badge.
   *
   * A slot rather than a `connectionStatus` prop: the dashboard renders
   * monitors and should not grow an opinion about transports. Whoever owns the
   * data owns the statement about it, and passes it in.
   */
  banner?: ReactNode;
  /**
   * True when the live stream is down and everything on screen is history.
   *
   * It sets one attribute on the root and nothing else; the draining of colour
   * is CSS (DESIGN.md §6). Doing it in CSS rather than by rewriting statuses
   * matters: the last known state is still the most useful thing on the
   * screen, so it must stay readable and stay in place. Nothing is hidden,
   * nothing moves, nothing is replaced by a skeleton — the display simply
   * stops presenting itself as current truth.
   *
   * It is a boolean rather than a `ConnectionStatus` for the same reason
   * `banner` is a slot: this component renders monitors and should not grow an
   * opinion about transports.
   */
  stale?: boolean;
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
  layout = DEFAULT_LAYOUT,
  banner = null,
  stale = false,
}: DashboardProps) {
  const searchId = useId();
  // Hooks cannot be skipped, so the query is always subscribed to and the
  // narrow-viewport veto is applied afterwards.
  const narrow = useCompactViewport();
  const shown = effectiveLayout(layout, narrow);
  // Derived during render, not mirrored into state: the filtered list is a
  // function of props and holding a copy would only create a way for the two
  // to disagree.
  const summary = summarise(monitors);
  // Local state, not a prop: unlike the search query, which the data owner
  // wants (it drives the empty-state copy and will drive the command palette),
  // the status chip is a momentary way of looking at the list on screen. It
  // deliberately does not survive a remount — coming back to a dashboard that
  // silently hides 198 of 200 monitors is how an outage gets missed.
  const [status, setStatus] = useState<MonitorStatus | null>(null);
  // Status first, then text, so the count in the sentence below is the size of
  // what is actually rendered rather than of an intermediate list.
  const visible = filterMonitors(filterByStatus(monitors, status), query);
  const filterNote = describeFilter(
    visible.length,
    monitors.length,
    status,
    query,
  );

  return (
    <section
      className="mon-dashboard"
      aria-labelledby={`${searchId}-title`}
      data-conn={stale ? "stale" : "live"}
    >
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
                {COUNTED
                  // A chip whose count drops to zero while it is the active
                  // filter has to stay: it is the only control that turns the
                  // now-empty list back into the full one.
                  .filter(
                    (counted) =>
                      summary[counted.status] > 0 || counted.status === status,
                  )
                  .map((counted) => (
                    <button
                      key={counted.status}
                      type="button"
                      className="mon-count"
                      // A toggle, not a radio group: pressing the chip that is
                      // already on is the obvious way back to the full list, and
                      // it is the same target the user just hit.
                      aria-pressed={status === counted.status}
                      onClick={() =>
                        setStatus((current) =>
                          current === counted.status ? null : counted.status,
                        )
                      }
                    >
                      <Led status={counted.status} labelled={false} />
                      <b className="mon-count-value">
                        {summary[counted.status]}
                      </b>{" "}
                      {counted.label}
                    </button>
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
      {filterNote !== null && monitors.length > 0 && (
        <p className="mon-result-count">{filterNote}</p>
      )}

      {/*
       * Two components, one breakpoint. Rendering both and hiding one with CSS
       * would keep 200 rows *and* 200 cards in the DOM, double every heartbeat
       * bar's ResizeObserver, and hand a screen reader the same monitor twice.
       */}
      {shown === "cards" ? (
        <MonitorCardList
          monitors={visible}
          query={query}
          totalCount={monitors.length}
          beatWidth={beatWidth}
        />
      ) : shown === "compact" ? (
        <MonitorCompactList
          monitors={visible}
          query={query}
          totalCount={monitors.length}
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
