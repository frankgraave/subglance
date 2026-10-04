import { Card, Panel } from "../components/Card";
import { IconAlert, IconClock } from "../components/icons";
import { IncidentStoryItem } from "./IncidentStoryItem";
import { IncidentClusterItem } from "./IncidentClusterItem";
import { useId, useState } from "react";
import { FilterField } from "../shell/FilterField";
import { useCompactViewport } from "../layout/useMediaQuery";
import { ChoiceControl } from "../monitors/ChoiceControl";
import { ListTabs } from "../monitors/ListTabs";
import { HISTORY_WINDOWS } from "./api";
import { clusterIncidents } from "./cluster";
import { describeChurn, incidentState } from "./story";
import { formatDuration } from "../monitors/detail";
import type { IncidentEntry } from "./cluster";
import type { Incident } from "../monitors/detail";

/** Shared empty default: a new Set per render would break memoisation. */
const EMPTY_ACKING: ReadonlySet<string> = new Set();

/**
 * Which of the two lists the reader is asking about.
 *
 * The scope filter SUB-131 named and SUB-136 left unbuilt. It is a filter over
 * what is already on screen rather than a second query: both lists are already
 * loaded, and the two questions this answers — "just show me what is still
 * broken" and "I am writing up last night" — are about attention, not about
 * data. Nothing here changes what was fetched, so switching back is instant
 * and cannot fail.
 */
export type IncidentScope = "all" | "open" | "resolved";

/**
 * Everything that is broken right now, and what broke recently.
 *
 * The dashboard answers "is anything wrong" and the detail view answers "what
 * is wrong with this one". Neither answers "what am I dealing with tonight",
 * which is the question somebody has when the phone wakes them — and until
 * this screen existed the only way to ask was to open monitors one at a time.
 * The sidebar has been promising it and `GET /api/v1/incidents` has been
 * answering it since the backend landed.
 *
 * The shape is the approved page proposal's: open above resolved. They are
 * two lists and not one filtered list because they are read in two different
 * moods. The top one is the 03:00 screen — railed, loud, every row carrying
 * an action. The bottom one is the morning-after screen, quieter by design,
 * where the number that matters is how long each outage lasted rather than
 * how long it has been going on. Since SUB-207 they are two sections of one
 * card, headed by the tabs that choose between them.
 *
 * Presentational, like `Dashboard` and `MonitorDetail`: it fetches nothing, so
 * it renders from a fixture in a test. `LiveIncidents` above it owns the data.
 */

export type IncidentsViewProps = {
  /** Open incidents, from GET /api/v1/incidents. */
  incidents: readonly Incident[];
  /**
   * Recently resolved incidents, for the history card.
   *
   * A separate prop rather than a filter over one list, because they come from
   * different endpoints: `GET /api/v1/incidents` is what is open now, and
   * `GET /api/v1/incidents/resolved` is what came back, paged and ordered by
   * resolution. An empty array means "none to show", which is why the card
   * states its own emptiness rather than disappearing.
   */
  resolved?: readonly Incident[];
  /**
   * How far back the history card reaches, in days.
   *
   * A prop rather than a constant read here, so the window belongs to the data
   * owner that fetches it. It is a control in the list's header rather than
   * a fixed 30 (SUB-136, SUB-207) — see `onHistoryDaysChange`.
   */
  historyDays?: number;
  /**
   * Moves the history window.
   *
   * Absent for a caller that renders a fixture and has nothing to refetch, and
   * then the control is not rendered at all rather than rendered dead. A
   * control that promises a function it does not have is worse than no
   * control, which is the whole reason SUB-136 left it out until the
   * endpoint behind it existed.
   */
  onHistoryDaysChange?: (days: number) => void;
  /**
   * True when older incidents remain inside the window, straight from the
   * API's `has_more`. Not a guess about a full page: the server reads one row
   * past the page to answer it.
   */
  historyHasMore?: boolean;
  /** Loads the next page of history. Absent when there is nothing to load. */
  onLoadMoreHistory?: () => void;
  /** True while the next page is in flight. */
  historyLoadingMore?: boolean;
  /** Why the history could not be loaded. One request now, so a failure is an
   *  error to report rather than a completeness flag to raise. */
  historyError?: Error | null;
  /**
   * True while the FIRST page of history is in flight.
   *
   * Separate from `loading`, which is the open-incident query: the two run
   * independently and the all-clear is a claim about both. Without this the
   * open query finishing first published "Nothing is broken right now" over a
   * history that was still arriving, and recovered incidents then appeared
   * under a sentence that had just said there were none.
   */
  historyLoading?: boolean;
  /** Monitor id to display name. A missing id falls back to the id itself. */
  names?: Readonly<Record<string, string>>;
  /** Now, in unix ms, for durations that are still running. */
  now: number;
  /**
   * How many monitors this instance watches.
   *
   * The empty state's proof. "No open incidents" alone is indistinguishable
   * from a poller that died; "across 6 monitors" is the evidence that the
   * silence was measured rather than merely observed.
   */
  monitorCount?: number;
  loading?: boolean;
  error?: Error | null;
  /** Acknowledges one incident. Absent for a viewer, who may not write. */
  onAck?: (id: string) => void;
  /** The id currently being acknowledged, if any. */
  /** Incidents whose ack request is still in flight. */
  ackingIds?: ReadonlySet<string>;
  /** The ack that failed, and why. Shown once, above the list. */
  ackError?: Error | null;
  /** True when the live stream is dead (DESIGN.md §6). */
  stale?: boolean;
};

export function IncidentsView({
  incidents,
  resolved = [],
  historyDays = 30,
  onHistoryDaysChange,
  historyHasMore = false,
  onLoadMoreHistory,
  historyLoadingMore = false,
  historyError = null,
  historyLoading = false,
  names = {},
  now,
  monitorCount,
  loading = false,
  error = null,
  onAck,
  ackingIds = EMPTY_ACKING,
  ackError = null,
  stale = false,
}: IncidentsViewProps) {
  const [query, setQuery] = useState("");
  const [scope, setScope] = useState<IncidentScope>("all");
  const narrow = useCompactViewport();
  const openSectionId = useId();
  const resolvedSectionId = useId();
  /*
   * Chronological, newest first, with clusters folded in where they exist.
   *
   * Newest first is what you want at 03:00: the question is "what just
   * happened", and any other order makes the reader hunt for the newest row.
   *
   * The grouping is additive rather than a second mode. `clusterIncidents`
   * leaves the sequence exactly as it was and only changes the shape where
   * several monitors started failing inside a minute — a cluster sits at the
   * position its newest member already held, and opening it yields precisely
   * the rows that would have been there ungrouped. So the two truths the
   * design proposal asked about are both on screen and neither hides the
   * other: the timeline is intact, and the pattern in it is stated.
   *
   * Computed here rather than in the data owner so the guarantee holds for
   * every caller, including a test rendering a fixture in whatever order it
   * wrote it.
   */
  /*
   * The filter is applied before clustering, not after.
   *
   * Clustering answers "did several monitors fail together", and a cluster
   * built from the full list and then filtered would keep saying "4 monitors"
   * while showing one row. Filtering first means the sentence and the rows it
   * sits above are computed from the same set.
   */
  const needle = query.trim().toLowerCase();
  const matches = (incident: Incident) =>
    needle === "" ||
    (names[incident.monitorId] ?? `Monitor ${incident.monitorId}`)
      .toLowerCase()
      .includes(needle);
  /*
   * Scope hides a list; it does not filter rows inside one.
   *
   * The two lists answer two different questions, so "open only" means the
   * history is not the answer to anything right now — not that it should be
   * shown empty. An empty Resolved list under a scope that excludes it would
   * read as "nothing resolved", which is a claim about the instance and not
   * about the filter.
   */
  const showOpen = scope !== "resolved";
  const showResolved = scope !== "open";
  const openMatches = incidents.filter(matches);
  const resolvedMatches = resolved.filter(matches);
  const shown = showOpen ? openMatches : [];
  const shownResolved = showResolved ? resolvedMatches : [];
  const entries = clusterIncidents(shown);
  const ackedCount = shown.filter((i) => incidentState(i) === "acked").length;

  /*
   * Churn is computed per monitor, not over the whole list.
   *
   * Five monitors with one incident each is a bad afternoon; one monitor with
   * five is a monitor that is bouncing, and the engine has quietly stopped
   * alerting on it. Those are different sentences and only the per-monitor
   * count can tell them apart. Both lists feed it: a flapping monitor's
   * incidents keep resolving, so counting only the open ones would miss
   * exactly the pattern this is for.
   *
   * Built from the filtered lists rather than the raw ones, for the same
   * reason clustering is: a churn notice is a sentence about the rows on
   * screen. Counting the unfiltered arrays left "api is flapping" sitting
   * above a list that had been searched down to one unrelated monitor.
   */
  const byMonitor = new Map<string, Incident[]>();
  for (const incident of [...shown, ...shownResolved]) {
    const list = byMonitor.get(incident.monitorId);
    if (list === undefined) byMonitor.set(incident.monitorId, [incident]);
    else list.push(incident);
  }
  const churn: { monitorId: string; note: string }[] = [];
  for (const [monitorId, list] of byMonitor) {
    const note = describeChurn(list, now);
    if (note !== null) churn.push({ monitorId, note });
  }

  const days = groupByDay(shownResolved, now);
  /*
   * "Nothing is broken right now" is a claim about the instance, not about
   * the search box.
   *
   * It is keyed on the source arrays rather than the filtered ones because
   * the sentence beside it counts monitors and says "zero confirmed
   * outages" — which, typed over a search for a monitor that has never
   * failed, tells an operator their outage is gone while it is still open
   * two rows up. A query that matches nothing gets its own line instead.
   */
  const searching = needle !== "";
  /*
   * Both requests must have finished, and both must have succeeded.
   *
   * The all-clear is a claim about the whole instance, so it may not be made
   * from half the evidence: the open-incident query resolving first while the
   * history is still in flight, or has failed, is not the same fact as
   * "nothing is broken". `historyError` is included for the same reason the
   * card below keeps itself on screen for a failed request — an unread history
   * is not an empty one.
   */
  const nothingAtAll =
    !loading &&
    !historyLoading &&
    error === null &&
    historyError === null &&
    incidents.length === 0 &&
    resolved.length === 0;
  /*
   * "Nothing matched" is only said when the *search* is what emptied the
   * screen.
   *
   * Scope can empty it too — "open only" on an instance where nothing is
   * broken — and that is good news rather than a failed search. Telling
   * somebody to clear a filter that is not hiding anything sends them looking
   * for an outage that does not exist, so the two cases are compared against
   * the scoped lists before the search is applied.
   */
  const inScope =
    (showOpen ? incidents.length : 0) + (showResolved ? resolved.length : 0);
  const noMatches =
    !loading &&
    error === null &&
    !nothingAtAll &&
    searching &&
    inScope > 0 &&
    shown.length === 0 &&
    shownResolved.length === 0;

  /*
   * The tabs' counts are what each tab would show under the current filter,
   * not what is on screen now: the dashboard's All 14 · Down 3 does not drop
   * Down to zero when Up is chosen, and neither does this. A count the
   * screen has not measured — the open list or the history still loading,
   * or a history that failed — is left off rather than drawn as a zero.
   */
  const openKnown = !loading && error === null;
  const resolvedKnown = !historyLoading && historyError === null;
  const tabs = [
    {
      key: "all" as const,
      label: "All",
      count: openKnown && resolvedKnown ? openMatches.length + resolvedMatches.length : undefined,
    },
    { key: "open" as const, label: "Open", count: openKnown ? openMatches.length : undefined },
    { key: "resolved" as const, label: "Resolved", count: resolvedKnown ? resolvedMatches.length : undefined },
  ];
  /* What a screen reader hears as the list narrows: the old toolbar count. */
  const announced = loading
    ? "Loading incidents…"
    : showResolved && historyLoading
      ? `${shown.length} open · loading resolved…`
      : showResolved && historyError !== null
        ? `${shown.length} open · resolved unavailable`
        : `${shown.length} open · ${shownResolved.length} resolved`;
  /*
   * The header narrows the lists only while there is something to narrow —
   * or while a choice is still narrowing them. With nothing at all to show,
   * the tabs and the filter go; but a scope or a query chosen before the
   * lists emptied keeps its control on screen, or it would go on narrowing
   * the lists when incidents come back, with nothing left that shows it or
   * takes it away.
   */
  const headed = !nothingAtAll || scope !== "all" || query !== "";
  const historyControl =
    onHistoryDaysChange === undefined ? null : (
      /*
       * Rendered only when somebody is listening to it. A screen rendered
       * from a fixture has no refetch to trigger, and a control that silently
       * does nothing is exactly the dead control SUB-136 refused to ship.
       *
       * The window is a refetch, and it is only offered because
       * `GET /api/v1/incidents/resolved` made a longer window cost the same as
       * a shorter one. The clock: a window of time, the glyph the Resolved
       * list used to wear for the same subject.
       */
      <ChoiceControl
        label="Resolved history window"
        name="History"
        legend="Resolved in the last"
        icon={<IconClock />}
        options={HISTORY_WINDOWS.map((window) => ({ value: window.days, label: window.label }))}
        value={historyDays}
        onChange={onHistoryDaysChange}
        narrow={narrow}
      />
    );

  return (
    /*
     * It wears the detail page's column classes rather than a pair of its own.
     *
     * It is the same shape — one column, one hard measure, read top to bottom
     * — and declaring that twice is how two screens drift apart at the next
     * spacing change. `data-conn` comes with it: connection.css already keys
     * the dead-stream withdrawal off `.mon-detail[data-conn="stale"]`, so this
     * screen inherits the treatment instead of re-implementing it.
     */
    <section
      className="mon-detail inc-screen"
      data-conn={stale ? "stale" : "live"}
      aria-label="Incidents"
    >
      {/*
       * One card, headed by the controls that act on it (SUB-207, AGENTS.md
       * "Where a control belongs"): what you are looking at on the left, as
       * All · Open · Resolved with their counts, and how you are looking on
       * the right — the monitor filter, then the history window. Nothing
       * stands between the masthead and this card.
       *
       * One card rather than the two it used to be, because the scope is a
       * choice between the two lists and a choice belongs at the head of the
       * thing it chooses in. The two moods the cards stood for are kept as
       * two sections: the 03:00 one on top, railed and loud, every row
       * carrying an action, and the morning-after one under it, quieter,
       * grouped by the day things came back.
       *
       * All is first and selected by default: this screen's job is to show
       * everything that happened, and a list that starts narrowed hides rows
       * the reader never asked to hide. Pressing the selected tab again goes
       * back to All, as it does on the dashboard.
       *
       * The filter matches monitor names, which is the question this screen
       * is read with: "did api go down again". It does not search incident
       * text, because an incident has none — inventing a field to search
       * would be a control that looks like it does more than it does.
       *
       * With nothing at all to show there is nothing to narrow, so the header
       * is the card's plain title and, when it can refetch, the window: a
       * quiet month is the moment somebody asks about the last ninety days.
       */}
      <Card
        className={headed ? "inc-board inc-board--listed" : "inc-board"}
        title="Open and resolved incidents"
        icon={<IconAlert />}
        headingLevel={2}
        lead={
          !headed ? undefined : (
            <ListTabs
              label="Filter by state"
              tabs={tabs}
              value={scope}
              onPress={(key) => setScope(key === scope ? "all" : key)}
            />
          )
        }
        action={
          !headed ? (
            historyControl ?? undefined
          ) : (
            <>
              <FilterField
                className="inc-head-filter"
                label="Filter incidents by monitor"
                placeholder="Filter by monitor…"
                value={query}
                onChange={setQuery}
              />
              {historyControl}
            </>
          )
        }
      >
        <p className="sr-only" role="status">
          {announced}
        </p>

        {churn.map(({ monitorId, note }) => (
          /*
           * Flapping suppresses the notifications, not the record.
           *
           * Without this the screen is at its most misleading exactly when a
           * service is at its worst: the alerts have gone quiet because the
           * monitor is oscillating, and a quiet phone above a list of rows
           * reads as a problem that settled down. The note says which monitor,
           * how many, and why nobody is being paged. Under the header, inside
           * the card, so nothing stands between the masthead and the list's
           * controls.
           */
          <p key={monitorId} className="inc-churn" role="status">
            <strong>{names[monitorId] ?? `Monitor ${monitorId}`}</strong>: {note}
          </p>
        ))}

        {noMatches ? (
          /*
           * A search that matched nothing, which is a different fact.
           *
           * It says what was searched and how to get back, and it deliberately
           * does not count monitors: the instance's health is not what the
           * reader just asked about, and stating it here is how "zero confirmed
           * outages" ended up on a screen with an open incident sitting behind
           * the filter.
           */
          <Panel>
            <p className="mon-detail-empty">
              {showResolved && historyHasMore ? "No loaded incidents" : "No incidents"} match “{query.trim()}”
            </p>
            <p className="mon-detail-note">
              The filter matches monitor names. Clear it to see the loaded
              incidents{showResolved && historyHasMore
                ? ", or load older history to search more of the window."
                : "."}
            </p>
          </Panel>
        ) : nothingAtAll ? (
          /*
           * The whole screen is the good news.
           *
           * Headline weight, and no call to action, because there is nothing for
           * the operator to do — a monitoring tool with nothing to report is the
           * tool working. The helper line quantifies it: the monitor count is
           * what proves the silence was measured rather than the result of a
           * poller that stopped.
           */
          <Panel>
            <p className="mon-detail-empty">Nothing is broken right now</p>
            <p className="mon-detail-note">
              {monitorCount === undefined
                ? "No open incidents, and none resolved recently."
                : `${monitorCount} ${
                    monitorCount === 1 ? "monitor" : "monitors"
                  } watched, zero confirmed outages.`}
            </p>
          </Panel>
        ) : !showOpen ? null : (
          <section className="inc-section" aria-labelledby={openSectionId}>
            <div className="inc-section-head">
              <h3 id={openSectionId} className="inc-section-title">
                Open incidents
              </h3>
              {/*
               * The count lives beside the list it is the length of.
               *
               * DESIGN.md §12 records why the sidebar's fabricated "2 incidents"
               * badge was removed: on a monitoring tool an invented number is
               * indistinguishable from a real alert. Here the number and the
               * list cannot disagree, because one is the length of the other.
               * The acked half is stated separately rather than subtracted —
               * an acked incident is still open, and folding it into a single
               * number would be the same conflation the rows fight.
               */}
              <span className="mon-detail-note">
                {loading
                  ? "Loading…"
                  : `${shown.length} open · ${ackedCount} muted`}
              </span>
            </div>
            {/*
             * No Panel around this list (SUB-133).
             *
             * `.inc-row` already draws a border, a radius, a fill and a raised
             * shadow: a row here IS a panel. Wrapping a list of panels in
             * another panel is what PR #42 deleted under the name "two
             * surfaces, not three", and it came back. The card is the surface,
             * the row is what rests on it. Two.
             */}
            <div className="inc-body">
              {ackError !== null ? (
                <p role="alert" className="inc-notice">
                  Could not mute repeat alerts: {ackError.message}
                </p>
              ) : null}
              {error !== null ? (
                <p
                  role="alert"
                  className="mon-detail-empty mon-detail-empty--quiet"
                >
                  Could not load incidents: {error.message}
                </p>
              ) : loading ? (
                <p className="mon-detail-empty mon-detail-empty--quiet">
                  Loading incidents…
                </p>
              ) : entries.length === 0 ? (
                <p className="mon-detail-empty">Everything is up</p>
              ) : (
                <ul className="inc-list">
                  {entries.map((entry: IncidentEntry) =>
                    entry.kind === "cluster" ? (
                      <IncidentClusterItem
                        key={entry.key}
                        cluster={entry}
                        now={now}
                        names={names}
                        onAck={onAck}
                        ackingIds={ackingIds}
                        stale={stale}
                      />
                    ) : (
                      <IncidentStoryItem
                        key={entry.key}
                        incident={entry.incident}
                        now={now}
                        subject={
                          names[entry.incident.monitorId] ??
                          `Monitor ${entry.incident.monitorId}`
                        }
                        onAck={onAck}
                        acking={ackingIds.has(entry.incident.id)}
                        stale={stale}
                      />
                    ),
                  )}
                </ul>
              )}
            </div>
          </section>
        )}

        {/*
         * The history renders whenever there is history to speak about, even
         * when nothing groups into a day.
         *
         * `days.length === 0` used to remove it outright, which took the
         * truncation notice with it: an instance whose resolved incidents all
         * failed to load, or arrived without a start date, showed no Resolved
         * list and therefore no hint that anything was missing. Absence reads
         * as "nothing happened", and that is the one thing this screen may
         * never imply by accident. The same reasoning keeps it on screen for a
         * failed request and for a scope that asked for it explicitly.
         */}
        {!showResolved ||
        (days.length === 0 &&
          shownResolved.length === 0 &&
          scope !== "resolved" &&
          !historyHasMore &&
          !historyLoading &&
          historyError === null) ? null : (
          <section className="inc-section" aria-labelledby={resolvedSectionId}>
            <div className="inc-section-head">
              <h3 id={resolvedSectionId} className="inc-section-title">
                Resolved
              </h3>
              <span className="mon-detail-note">
                Last {historyDays} {historyDays === 1 ? "day" : "days"} · grouped
                by day
              </span>
            </div>
            {/* Same two-surface count as the open list above (SUB-133): the
                day heading is a heading ON the card, and the rows carry their
                own surface. */}
            <div className="inc-body">
              {historyError !== null ? (
                /*
                 * A failed history is an error, not a completeness flag. One
                 * request means a failure is total: there is no partial month
                 * to caveat, so saying "this could not be loaded" is both the
                 * honest and the simpler sentence.
                 */
                <p className="inc-notice" role="alert">
                  Could not load resolved history: {historyError.message}
                </p>
              ) : null}
              {historyLoading && historyError === null ? (
                <p className="inc-notice" role="status">
                  Loading resolved history…
                </p>
              ) : null}
              {days.length === 0 && historyError === null && !historyLoading ? (
                /*
                 * An explicit empty state, because an empty list is ambiguous:
                 * it could mean "a quiet month" or "we failed to load".
                 * Suppressed when there IS an error, because the error above
                 * already says which of the two it is. The undated case is
                 * stated separately rather than silently dropped —
                 * `groupByDay` cannot place an incident with no resolution
                 * time on any day, and a reader is owed the count rather than
                 * a shorter list.
                 */
                <p className="inc-notice" role="status">
                  {shownResolved.length === 0
                    ? resolved.length > 0 || historyHasMore
                      ? searching
                        ? "No loaded resolved incidents match this filter."
                        : "No resolved incidents loaded yet."
                      : `Nothing resolved in the last ${historyDays} ${
                          historyDays === 1 ? "day" : "days"
                        }.`
                    : `${shownResolved.length} resolved ${
                        shownResolved.length === 1 ? "incident" : "incidents"
                      } could not be placed on a day — no resolution time was recorded.`}
                </p>
              ) : null}
              {days.map((day) => (
                <section key={day.key} className="inc-day">
                  {/*
                   * The day heading carries its own totals.
                   *
                   * "2 incidents · 24m total" is the sentence someone writes in
                   * a status update the next morning, and having to add up the
                   * rows to produce it is work the screen can do. An `h4`,
                   * under the section's `h3`.
                   */}
                  <h4 className="inc-day-head">
                    <span className="inc-label">{day.label}</span>
                    <span className="mon-detail-note">
                      {day.items.length}{" "}
                      {day.items.length === 1 ? "incident" : "incidents"} ·{" "}
                      {formatDuration(day.totalS)} total
                    </span>
                  </h4>
                  <ul className="inc-list">
                    {day.items.map((incident) => (
                      <IncidentStoryItem
                        key={incident.id}
                        incident={incident}
                        now={now}
                        subject={
                          names[incident.monitorId] ??
                          `Monitor ${incident.monitorId}`
                        }
                        stale={stale}
                        past
                      />
                    ))}
                  </ul>
                </section>
              ))}
              {/*
               * What the screen shows when there genuinely is more: `has_more`
               * comes with `next_cursor`, so the honest statement has a control
               * attached to it — the reader learns the list is short *and* can
               * lengthen it. Rendered only when the owner passed a loader.
               */}
              {historyHasMore && onLoadMoreHistory !== undefined ? (
                <p className="inc-more">
                  <button
                    type="button"
                    className="button"
                    onClick={onLoadMoreHistory}
                    disabled={historyLoadingMore}
                  >
                    {historyLoadingMore ? "Loading…" : "Load older incidents"}
                  </button>
                  <span className="mon-detail-note" role="status">
                    More resolved incidents lie inside this window.
                  </span>
                </p>
              ) : null}
            </div>
          </section>
        )}
      </Card>
    </section>
  );
}

type Day = {
  key: string;
  label: string;
  items: Incident[];
  totalS: number;
};

/**
 * History grouped by the day it recovered, newest day first.
 *
 * Newest first here and oldest first above, and the reversal is deliberate:
 * the open list is a work queue where the longest-running outage is the most
 * urgent, and history is a record where the most recent day is the one being
 * asked about.
 *
 * "Today" and "Yesterday" are spelled out rather than dated, because that is
 * how the reader thinks about them — and every other day keeps its date, since
 * "3 days ago" stops being countable almost immediately.
 *
 * Keyed on `resolvedAt`, not `startedAt`, because that is the question this
 * card answers and the order the server already sorted by.
 *
 * Grouping on the start instead put a row in two kinds of wrong place at once.
 * An outage that began Monday night and recovered Tuesday morning was filed
 * under Monday — under a heading a reader scans to ask "what recovered
 * yesterday", answering with something that recovered today. And because the
 * endpoint pages by `resolvedAt` while this re-sorted by `startedAt`, a long
 * outage resolved minutes ago could be drawn *below* an older resolution: the
 * screen contradicting the cursor that fetched it. One sort key per list, and
 * for a list of recoveries it is the recovery.
 */
function groupByDay(incidents: readonly Incident[], now: number): Day[] {
  const days = new Map<string, Day>();
  const sorted = [...incidents].sort(
    (a, b) => (b.resolvedAt ?? 0) - (a.resolvedAt ?? 0),
  );
  for (const incident of sorted) {
    if (incident.resolvedAt === null) continue;
    const date = new Date(incident.resolvedAt);
    const key = `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
    let day = days.get(key);
    if (day === undefined) {
      day = { key, label: dayLabel(date, now), items: [], totalS: 0 };
      days.set(key, day);
    }
    day.items.push(incident);
    day.totalS += Number.isFinite(incident.durationS) ? incident.durationS : 0;
  }
  return [...days.values()];
}

/*
 * `now` is threaded through rather than read from the wall clock.
 *
 * The component already receives `now` — it is how every duration on the
 * screen stays consistent and how tests pin time. `dayLabel` reaching for
 * `new Date()` meant a render with an injected `now` could head its groups
 * "Today" against the real date instead of the one the rest of the screen was
 * describing: two clocks in one view, disagreeing.
 */
function dayLabel(date: Date, now: number): string {
  const today = new Date(now);
  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate();
  if (sameDay(date, today)) return "Today";
  const yesterday = new Date(today.getTime() - 86_400_000);
  if (sameDay(date, yesterday)) return "Yesterday";
  return date.toLocaleDateString(undefined, { day: "numeric", month: "short" });
}
