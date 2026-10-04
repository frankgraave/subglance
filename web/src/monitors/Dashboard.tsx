import { useId, useState } from "react";
import type { ReactNode } from "react";
import { useCompactViewport } from "../layout/useMediaQuery";
import { Card } from "../components/Card";
import { IconList } from "../components/icons";
import {
  DEFAULT_LAYOUT,
  effectiveLayout,
  type CardColumns,
  type LayoutId,
} from "../shell/preferences";
import { FilterField } from "../shell/FilterField";
import { DashboardFilter } from "./DashboardFilter";
import { DashboardView } from "./DashboardView";
import { MonitorCardList } from "./MonitorCardList";
import { MonitorCompactList } from "./MonitorCompactList";
import { MonitorTable } from "./MonitorTable";
import { ROW_BEAT_WIDTH } from "./MonitorRow";
import {
  describeFilter,
  filterByStatus,
  filterByTags,
  filterMonitors,
  liveTagSelection,
  sameTagSelection,
  summarise,
  tagFacets,
} from "./model";
import { StatusTabs } from "./StatusTabs";
import type { TagSelection } from "./model";
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
   * Changes the layout. Its presence is what puts the layout switcher in the
   * View panel: the workbench draws the dashboard with a switcher of its own,
   * and two switchers for one setting would be one too many.
   */
  onLayoutChange?: (next: LayoutId) => void;
  /** How many cards per row, in the Cards layout. See CardColumnsSwitcher. */
  cardColumns?: CardColumns;
  /** Omitted where the count is fixed, e.g. the workbench. */
  onCardColumnsChange?: (next: CardColumns) => void;
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
   * It sets one attribute on the root, and it reaches the list layout, which
   * puts every status word into the past tense. The draining of *colour* is
   * CSS (DESIGN.md §6); the change of *tense* cannot be, because a stylesheet
   * cannot rewrite text and in the rows and compact layouts the status word is
   * `sr-only` — draining the hue alone would fix the lie for sighted readers
   * and leave it intact for everyone using a screen reader (SUB-111).
   *
   * What does not happen is the status being rewritten or dropped: the last
   * known state is still the most useful thing on the screen, so it must stay
   * readable and stay in place. Nothing is hidden, nothing moves, nothing is
   * replaced by a skeleton — the display simply stops presenting itself as
   * current truth.
   *
   * It is a boolean rather than a `ConnectionStatus` for the same reason
   * `banner` is a slot: this component renders monitors and should not grow an
   * opinion about transports.
   */
  stale?: boolean;
  /**
   * Opens one monitor's detail view client-side.
   *
   * Optional, and absent in the workbench: the harness has no router, and a
   * link that navigates out of it would be a dead end. Without it the names
   * are still real links — see MonitorLink — they just cost a page load.
   */
  onOpenMonitor?: (id: string) => void;
  /**
   * Opens the add-monitor form from the empty dashboard.
   *
   * The empty dashboard is the onboarding (DESIGN.md §7.6), so its one next
   * step should be a control and not directions to one. Passed only for a
   * reader who may add a monitor; see EmptyState for what a viewer reads.
   */
  onAddMonitor?: () => void;
};

export function Dashboard({
  monitors,
  query,
  onQueryChange,
  announcement = null,
  beatWidth,
  layout = DEFAULT_LAYOUT,
  onLayoutChange,
  cardColumns = "1",
  onCardColumnsChange,
  banner = null,
  stale = false,
  onOpenMonitor,
  onAddMonitor,
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
  // Tag choices are local for the same reason, and for one more: the facets
  // themselves come from the data, so a selection kept across a reload could
  // name a key that no monitor carries any more.
  const [tags, setTags] = useState<TagSelection>({});
  // Grouping is one axis at a time: "by environment" and "by customer" are two
  // arrangements of the same rows, not two that can be layered. Local and not
  // persisted, like the other view controls on this screen.
  const [groupKey, setGroupKey] = useState<string | null>(null);
  // Facets are derived from the unfiltered list, never from the visible one.
  // Narrowing the options as you choose would make the second dropdown lose
  // the values the first one just excluded, and there would be no way back.
  const facets = tagFacets(monitors);
  // A selection whose key *or value* has since vanished from the data would
  // silently empty the list with no control left to clear it: the select can
  // only offer values that still exist, so a stale one is unreachable. Both
  // halves of a pair therefore have to be live for it to keep filtering.
  const liveTags = liveTagSelection(facets, tags);
  // Masking alone is not enough: kept in state, a dropped pair would come back
  // into force on its own the moment its value reappeared, narrowing the list
  // with nobody having chosen it again. So the stored selection is pruned to
  // the live one, during render like any state derived from a prop change.
  if (!sameTagSelection(tags, liveTags)) setTags(liveTags);
  // A grouping key whose tag has vanished from the data would leave the list
  // headed by a key nothing carries, so it falls back to the flat order for
  // the same reason a stale tag selection is dropped.
  const liveGroupKey =
    groupKey !== null && facets.some((facet) => facet.key === groupKey)
      ? groupKey
      : null;
  // Status, then tags, then text, so the count in the sentence below is the
  // size of what is actually rendered rather than of an intermediate list.
  const visible = filterMonitors(
    filterByTags(filterByStatus(monitors, status), liveTags),
    query,
  );
  // Only the non-query narrowing: `EmptyState` already words the query case.
  const narrowed = status !== null || Object.keys(liveTags).length > 0;
  const filterNote = describeFilter(
    visible.length,
    monitors.length,
    status,
    query,
    liveTags,
  );

  // Every narrowing at once, for the empty state's one way back. Not the
  // grouping or the layout: those arrange the list and hide nothing.
  const clearFilters = () => {
    setStatus(null);
    setTags({});
    onQueryChange("");
  };
  // The chips under the header: one per chosen tag, and on a phone the text
  // filter too, whose field is in the filter sheet there and out of sight.
  const chips: { key: string; label: string; value: string; clear: () => void }[] = [
    ...Object.entries(liveTags).map(([key, value]) => ({
      key: `tag:${key}`,
      label: key,
      value,
      clear: () =>
        setTags((current) =>
          Object.fromEntries(
            Object.entries(current).filter(([other]) => other !== key),
          ),
        ),
    })),
    ...(narrow && query.trim() !== ""
      ? [{ key: "query", label: "name", value: query.trim(), clear: () => onQueryChange("") }]
      : []),
  ];
  const listProps = {
    monitors: visible,
    query,
    totalCount: monitors.length,
    filtered: narrowed,
    groupKey: liveGroupKey,
    onOpen: onOpenMonitor,
    onAddMonitor,
    onClearFilters: clearFilters,
    stale,
  };

  return (
    <section
      className="mon-dashboard"
      data-conn={stale ? "stale" : "live"}
    >
      {/* Above the list, not below it: a warning that the numbers are frozen
          has to be read *before* the numbers, not after scrolling past them. */}
      {banner}

      {/*
       * The single live region, and it lives *outside* the list
       * (research note 3). `aria-live` on the table itself would make a
       * screen reader re-read rows on every heartbeat tick, which is both
       * unusable and drowns out the one announcement that matters. This region
       * carries status transitions only; the caller decides when to fill it.
       */}
      <div role="status" aria-atomic="true" className="sr-only">
        {announcement ?? ""}
      </div>

      {/*
       * One card around the list, whichever layout draws it, headed by the
       * controls that act on it (SUB-183, AGENTS.md "Where a control
       * belongs"): what you are looking at on the left, as status tabs with
       * their counts, and how you are looking on the right — the text
       * filter, the tag filter and the view. Nothing stands between the
       * masthead and this card. The heading is "Monitors", kept for a screen
       * reader and not printed: All carries the count it used to print.
       */}
      <Card
        className="mon-board"
        title="Monitors"
        icon={<IconList />}
        lead={
          summary.total > 0 ? (
            <StatusTabs summary={summary} status={status} onChange={setStatus} />
          ) : undefined
        }
        action={
          <>
            {narrow || summary.total === 0 ? null : (
              <FilterField
                id={searchId}
                className="mon-head-filter"
                label="Filter monitors by name or address"
                placeholder="Name or address"
                value={query}
                onChange={onQueryChange}
              />
            )}
            {summary.total === 0 || (facets.length === 0 && !narrow) ? null : (
              <DashboardFilter
                monitors={monitors}
                facets={facets}
                selected={liveTags}
                onSelect={(key, value) =>
                  setTags((current) => {
                    const next: Record<string, string> = { ...current };
                    if (value === "") delete next[key];
                    else next[key] = value;
                    return next;
                  })
                }
                onClearTags={() => setTags({})}
                status={status}
                query={query}
                onQueryChange={onQueryChange}
                narrow={narrow}
                visible={visible.length}
              />
            )}
            <DashboardView
              shown={shown}
              onLayoutChange={onLayoutChange}
              cardColumns={cardColumns}
              onCardColumnsChange={onCardColumnsChange}
              grouping={{ facets, value: liveGroupKey, onChange: setGroupKey }}
            />
          </>
        }
      >
        {/* Active filters, inside the card and under its header, each with a
            way to drop it; and the sentence that says what is left. Filtering
            is not announced through the live region: a result count that
            updates as you type belongs next to the input, where it does not
            interrupt. */}
        {(chips.length > 0 || (filterNote !== null && monitors.length > 0)) && (
          <div className="mon-filter-row">
            {chips.map((chip) => (
              <button
                key={chip.key}
                type="button"
                className="chip chip--meta mon-filter-chip"
                aria-label={`Remove filter ${chip.label}: ${chip.value}`}
                onClick={chip.clear}
              >
                <span className="chip-label">{chip.label}</span>
                <span className="chip-value">{chip.value}</span>
                <span className="mon-filter-chip-x" aria-hidden="true">
                  ×
                </span>
              </button>
            ))}
            {chips.length > 0 ? (
              <button
                type="button"
                className="button button--quiet button--compact"
                onClick={() => {
                  setTags({});
                  if (narrow) onQueryChange("");
                }}
              >
                Clear all
              </button>
            ) : null}
            {filterNote !== null && monitors.length > 0 ? (
              <p className="mon-result-count">{filterNote}</p>
            ) : null}
          </div>
        )}

        {/*
         * Two components, one breakpoint. Rendering both and hiding one with
         * CSS would keep 200 rows *and* 200 cards in the DOM, double every
         * heartbeat bar's ResizeObserver, and hand a screen reader the same
         * monitor twice.
         */}
        {shown === "cards" ? (
          <MonitorCardList
            {...listProps}
            beatWidth={beatWidth}
            columns={cardColumns}
          />
        ) : shown === "compact" ? (
          <MonitorCompactList {...listProps} />
        ) : (
          <MonitorTable {...listProps} beatWidth={beatWidth ?? ROW_BEAT_WIDTH} />
        )}
      </Card>
    </section>
  );
}
