import { useId, useState } from "react";
import type { ReactNode } from "react";
import { useCompactViewport } from "../layout/useMediaQuery";
import { IconGroup, IconTag } from "../components/icons";
import {
  DEFAULT_LAYOUT,
  effectiveLayout,
  type CardColumns,
  type LayoutId,
} from "../shell/preferences";
import { LED_STATE } from "./ledState";
import { CardColumnsSwitcher } from "../shell/CardColumnsSwitcher";
import { SearchIcon } from "../shell/icons";
import { ToolbarTools, TopbarTools } from "../shell/TopbarTools";
import { MonitorCardList } from "./MonitorCardList";
import { MonitorCompactList } from "./MonitorCompactList";
import { MonitorTable } from "./MonitorTable";
import { ROW_BEAT_WIDTH } from "./MonitorRow";
import {
  describeFilter,
  filterByStatus,
  filterByTags,
  filterMonitors,
  summarise,
  tagFacets,
} from "./model";
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
};

const COUNTED: { status: MonitorStatus; label: string }[] = [
  { status: "down", label: "down" },
  { status: "pending", label: "pending" },
  { status: "waiting", label: "waiting" },
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
  cardColumns = "1",
  onCardColumnsChange,
  banner = null,
  stale = false,
  onOpenMonitor,
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
  const liveTags: TagSelection = Object.fromEntries(
    facets
      .map((facet) => [facet.key, tags[facet.key] ?? ""] as const)
      .filter(([key, value]) => {
        if (value === "") return false;
        const facet = facets.find((candidate) => candidate.key === key);
        return facet !== undefined && facet.values.includes(value);
      }),
  );
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

      {/*
       * Search goes to the masthead; everything that narrows the list goes to
       * the page toolbar (SUB-138).
       *
       * This screen used to carry its own bar, and the monitors page carried
       * a different one, so two screens that both search a list of monitors
       * put the field in two places. The split is by what a control *is*:
       * search is true on every list screen and holds one position; a status
       * filter is this screen's alone and is expected to change with the
       * route.
       */}
      {/*
       * `h1`, visually hidden (SUB-100): the detail view uses `h1` for the
       * monitor's name, and the two screens disagreeing about where the
       * outline starts leaves heading navigation with no level-1 landmark on
       * the busier of the two. Hidden because the card below carries the
       * visible title, and printing the same noun twice is what this
       * rearrangement exists to stop.
       */}
      <h1 id={`${searchId}-title`} className="sr-only">
        Monitors
      </h1>

      <TopbarTools>
        <label className="shell-search">
          {/* A real <label>, hidden. Placeholder-as-label disappears the
              moment someone types, which is when they most need it. */}
          <span className="sr-only">Search monitors by name or target</span>
          <SearchIcon />
          <input
            id={searchId}
            type="search"
            className="shell-search-input"
            value={query}
            placeholder="Search monitors…"
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => onQueryChange(event.target.value)}
          />
          {/* The shortcut is now the global Topbar launcher. This field
              still filters this page only; opening commands preserves it. */}
        </label>
      </TopbarTools>

      <ToolbarTools>
        <div className="tb-group">
          {/*
           * The status filter.
           *
           * `role="group"` and `aria-pressed`, never a radio group: these are
           * independent toggles and "none selected" is a real state. The
           * frame is a visual family, not a promise of one-of-N; pressing the
           * active chip is the way back to the full list.
           */}
          {summary.total > 0 && (
            <div className="mon-filter" role="group" aria-label="Filter by status">
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
                    aria-pressed={status === counted.status}
                    onClick={() =>
                      setStatus((current) =>
                        current === counted.status ? null : counted.status,
                      )
                    }
                  >
                    {/*
                     * A round lamp, not the 20x7 bar.
                     *
                     * §3 fixes the bar's size so a wall of them stays
                     * scannable, and that argument is about lamps reporting a
                     * monitor's state. This is a filter chip: the dot is a key
                     * to the colour, at the scale of the text beside it, and
                     * the word next to it is what carries the meaning
                     * (§2.3) — which is why it is safe for it to be small.
                     */}
                    <span
                      className="mon-count-dot"
                      data-state={LED_STATE[counted.status]}
                      aria-hidden="true"
                    />
                    <b className="mon-count-value">
                      {summary[counted.status]}
                    </b>{" "}
                    {counted.label}
                  </button>
                ))}
            </div>
          )}

          {/*
           * One native <select> per tag key, and native on purpose: a custom
           * listbox would have to re-earn keyboard support, screen-reader
           * semantics and the OS picker on a phone, and these lists are a
           * handful of values long — the case where a native select is simply
           * better. The key is the visible label, so the control reads
           * "env: prod" without a separate legend.
           */}
          {facets.map((facet) => (
            // The key is also the text of an option in the Group by control,
            // so an explicit attribute — not the visible text — is what
            // identifies a facet unambiguously.
            <label
              key={facet.key}
              className="tb-field tb-field--framed"
              data-facet-key={facet.key}
            >
              {/* The glyph, and it is decorative: the <label> around the
                  select is already the control's accessible name, so an icon
                  that announced itself would make a screen reader say the
                  filter twice. */}
              <IconTag />
              <span className="tb-label">{facet.key}</span>
              <select
                className="tb-select mon-facet-select"
                value={tags[facet.key] ?? ""}
                onChange={(event) =>
                  setTags((current) => ({
                    ...current,
                    [facet.key]: event.target.value,
                  }))
                }
              >
                {/* "Any" rather than a blank first option: an empty entry in a
                    filter reads as a value someone forgot to name. */}
                <option value="">Any</option>
                {facet.values.map((value) => (
                  <option key={value} value={value}>
                    {value}
                  </option>
                ))}
              </select>
            </label>
          ))}

          {/*
           * Grouping sits with the filters because it answers a neighbouring
           * question about the same tags, but it is labelled "Group by" rather
           * than given a key of its own: it does not narrow the list, and a
           * control that looks like a filter while changing nothing about what
           * is visible is the kind of thing people press twice.
           */}
          {facets.length > 0 && (
            <label className="tb-field tb-field--framed">
              {/* A different glyph from the facets, because it is a different
                  kind of control: rows gathered under headings, not a filter.
                  Decorative — the <label> names the select. */}
              <IconGroup />
              <span className="tb-label">Group by</span>
              {/* Its own class, not `mon-facet-select`: it looks the same but
                  it is not a facet, and one selector must not match both. */}
              <select
                className="tb-select mon-group-select"
                value={groupKey ?? ""}
                onChange={(event) => setGroupKey(event.target.value || null)}
              >
                <option value="">None</option>
                {facets.map((facet) => (
                  <option key={facet.key} value={facet.key}>
                    {facet.key}
                  </option>
                ))}
              </select>
            </label>
          )}

          {/*
           * View tools, empty for three of the four layouts.
           *
           * Keyed off the layout actually on screen rather than the stored
           * preference — on a narrow viewport the preference may be Rows while
           * Cards is what renders, and the control has to follow what the user
           * can see. It sits at the end of this bar rather than in the
           * masthead: appearing and disappearing costs nothing here, and in
           * the masthead it slid the control you had just pressed sideways.
           */}
          {shown === "cards" && onCardColumnsChange !== undefined && (
            <CardColumnsSwitcher
              value={cardColumns}
              onChange={onCardColumnsChange}
            />
          )}
        </div>
      </ToolbarTools>

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
          filtered={narrowed}
          groupKey={liveGroupKey}
          beatWidth={beatWidth}
          columns={cardColumns}
          onOpen={onOpenMonitor}
          stale={stale}
        />
      ) : shown === "compact" ? (
        <MonitorCompactList
          monitors={visible}
          query={query}
          totalCount={monitors.length}
          filtered={narrowed}
          groupKey={liveGroupKey}
          onOpen={onOpenMonitor}
          stale={stale}
        />
      ) : (
        <MonitorTable
          monitors={visible}
          query={query}
          totalCount={monitors.length}
          filtered={narrowed}
          groupKey={liveGroupKey}
          beatWidth={beatWidth ?? ROW_BEAT_WIDTH}
          onOpen={onOpenMonitor}
          stale={stale}
        />
      )}
    </section>
  );
}
