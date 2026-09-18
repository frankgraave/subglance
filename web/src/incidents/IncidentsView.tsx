import { Card, Panel } from "../components/Card";
import { IconAlert, IconClock } from "../components/icons";
import { IncidentStoryItem } from "./IncidentStoryItem";
import { IncidentClusterItem } from "./IncidentClusterItem";
import { useState } from "react";
import { SearchIcon } from "../shell/icons";
import { ToolbarTools, TopbarTools } from "../shell/TopbarTools";
import { HISTORY_WINDOWS } from "./api";
import { clusterIncidents } from "./cluster";
import { describeChurn, incidentState } from "./story";
import { formatDuration } from "../monitors/detail";
import type { IncidentEntry } from "./cluster";
import type { Incident } from "../monitors/detail";

/** Shared empty default: a new Set per render would break memoisation. */
const EMPTY_ACKING: ReadonlySet<string> = new Set();

/**
 * Which of the two cards the reader is asking about.
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
 * The shape is the approved page proposal's: two cards, open above resolved.
 * They are two cards and not one filtered list because they are read in two
 * different moods. The top one is the 03:00 screen — railed, loud, every row
 * carrying an action. The bottom one is the morning-after screen, quieter by
 * design, where the number that matters is how long each outage lasted rather
 * than how long it has been going on.
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
   * owner that fetches it. It is now a control in the toolbar rather than a
   * fixed 30 (SUB-136) — see `onHistoryDaysChange`.
   */
  historyDays?: number;
  /**
   * Moves the history window.
   *
   * Absent for a caller that renders a fixture and has nothing to refetch, and
   * then the control is not rendered at all rather than rendered dead. A
   * control that promises a function it does not have is worse than an empty
   * toolbar, which is the whole reason SUB-136 left this slot empty until the
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
   * Scope hides a card; it does not filter rows inside one.
   *
   * The two cards answer two different questions, so "open only" means the
   * history card is not the answer to anything right now — not that it should
   * be shown empty. An empty Resolved card under a scope that excludes it
   * would read as "nothing resolved", which is a claim about the instance and
   * not about the filter.
   */
  const showOpen = scope !== "resolved";
  const showResolved = scope !== "open";
  const shown = showOpen ? incidents.filter(matches) : [];
  const shownResolved = showResolved ? resolved.filter(matches) : [];
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
  const nothingAtAll =
    !loading &&
    error === null &&
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
       * Search, in the masthead like every other list screen (SUB-138).
       *
       * It filters by monitor name, which is the question this screen is
       * actually read with: "did api go down again". It does not search
       * incident text, because an incident has none — inventing a field to
       * search would be a control that looks like it does more than it does.
       */}
      <TopbarTools>
        <label className="shell-search">
          <span className="sr-only">Filter incidents by monitor</span>
          <SearchIcon />
          <input
            type="search"
            className="shell-search-input"
            value={query}
            placeholder="Filter by monitor…"
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
      </TopbarTools>

      {/*
       * The page toolbar's own controls — the slot SUB-131 opened and SUB-136
       * left empty, deliberately, because the scope filter it named had
       * nothing behind it and a control that promises a function it does not
       * have is worse than an empty bar.
       *
       * Both are real now. Scope is a filter over what is already fetched, so
       * it cannot fail and cannot be slow. The window is a refetch, and it is
       * only shipped because `GET /api/v1/incidents/resolved` made a longer
       * window cost the same as a shorter one — under the per-monitor fan-out
       * this replaced, offering "90 days" would have been offering to send
       * several hundred requests.
       *
       * The window control is rendered only when somebody is listening to it.
       * A screen rendered from a fixture has no refetch to trigger, and a
       * select that silently does nothing is exactly the dead control this
       * ticket refused to ship.
       */}
      <ToolbarTools>
        <div className="tb-group">
          <label className="tb-field">
            <span className="tb-label">Show</span>
            <select
              className="tb-select"
              value={scope}
              onChange={(event) => setScope(event.target.value as IncidentScope)}
            >
              {/* "All" first and selected by default: this screen's job is to
                  show everything that happened, and a filter that starts
                  narrowed hides rows the reader never asked to hide. */}
              <option value="all">Open and resolved</option>
              <option value="open">Open only</option>
              <option value="resolved">Resolved only</option>
            </select>
          </label>

          {onHistoryDaysChange === undefined ? null : (
            <label className="tb-field">
              <span className="tb-label">History</span>
              <select
                className="tb-select"
                value={historyDays}
                onChange={(event) =>
                  onHistoryDaysChange(Number(event.target.value))
                }
              >
                {HISTORY_WINDOWS.map((window) => (
                  <option key={window.days} value={window.days}>
                    {window.label}
                  </option>
                ))}
              </select>
            </label>
          )}

          <p className="tb-count" role="status">
            {loading
              ? "Loading incidents…"
              : `${shown.length} open · ${shownResolved.length} resolved`}
          </p>
        </div>
      </ToolbarTools>

      {/*
       * No visible page heading, for the reason Monitors has none (SUB-138):
       * the sidebar says Incidents and the cards below say "Open incidents"
       * and "Resolved". The `h1` survives, visually hidden, so heading
       * navigation still has a level-1 landmark.
       */}
      <h1 className="sr-only">Incidents</h1>

      {churn.map(({ monitorId, note }) => (
        /*
         * Flapping suppresses the notifications, not the record.
         *
         * Without this the screen is at its most misleading exactly when a
         * service is at its worst: the alerts have gone quiet because the
         * monitor is oscillating, and a quiet phone above a list of rows
         * reads as a problem that settled down. The note says which monitor,
         * how many, and why nobody is being paged.
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
        <Card title="Incidents" icon={<IconAlert />} headingLevel={2}>
          <Panel>
            <p className="mon-detail-empty">
              No incidents match “{query.trim()}”
            </p>
            <p className="mon-detail-note">
              The filter matches monitor names. Clear it to see every incident
              on this instance.
            </p>
          </Panel>
        </Card>
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
        <Card title="Incidents" icon={<IconAlert />} headingLevel={2}>
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
        </Card>
      ) : !showOpen ? null : (
        <Card
          title="Open incidents"
          icon={<IconAlert />}
          headingLevel={2}
          action={
            /*
             * The count lives beside the list it is the length of.
             *
             * DESIGN.md §12 records why the sidebar's fabricated "2 incidents"
             * badge was removed: on a monitoring tool an invented number is
             * indistinguishable from a real alert. Here the number and the
             * list cannot disagree, because one is the length of the other.
             * The acked half is stated separately rather than subtracted —
             * an acked incident is still open, and folding it into a single
             * number would be the same conflation the rows fight.
             */
            <span className="mon-detail-note">
              {loading
                ? "Loading…"
                : `${shown.length} open · ${ackedCount} acknowledged`}
            </span>
          }
        >
          {/*
           * No Panel around this list (SUB-133).
           *
           * `.inc-row` already draws a border, a radius, a fill and a raised
           * shadow: a row here IS a panel. Wrapping a list of panels in
           * another panel is what PR #42 deleted under the name "two
           * surfaces, not three", and it came back. What it produced was card
           * fill, panel fill, then a row lifting itself off the panel — three
           * nested boxes to say one thing, and rows that looked like they
           * were sitting in a tray inside a tray.
           *
           * The card is the surface, the row is what rests on it. Two.
           */}
          <div className="inc-body">
            {ackError !== null ? (
              <p role="alert" className="inc-notice">
                Could not acknowledge: {ackError.message}
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
        </Card>
      )}

      {/*
       * The card renders whenever there is history to speak about, even when
       * nothing groups into a day.
       *
       * `days.length === 0` used to remove it outright, which took the
       * truncation notice with it: an instance whose resolved incidents all
       * failed to load, or arrived without a start date, showed no Resolved
       * card and therefore no hint that anything was missing. Absence of a
       * card reads as "nothing happened", and that is the one thing this
       * screen may never imply by accident. The same reasoning keeps it on
       * screen for a failed request and for a scope that asked for it
       * explicitly.
       */}
      {!showResolved ||
      (days.length === 0 &&
        shownResolved.length === 0 &&
        scope !== "resolved" &&
        !historyHasMore &&
        historyError === null) ? null : (
        <Card
          title="Resolved"
          icon={<IconClock />}
          headingLevel={2}
          action={
            <span className="mon-detail-note">
              Last {historyDays} {historyDays === 1 ? "day" : "days"} · grouped
              by day
            </span>
          }
        >
          {/* Same two-surface count as the open card above (SUB-133): the day
              heading is a heading ON the card, and the rows carry their own
              surface. A Panel here made the day section read as a card of its
              own inside the card. */}
          <div className="inc-body">
            {historyError !== null ? (
              /*
               * A failed history is an error now, not a completeness flag.
               *
               * It used to be one of three causes folded into `truncated`,
               * because under the fan-out a single monitor's 500 left the rest
               * of the card usable and only slightly short. One request means
               * a failure is total: there is no partial month to caveat, so
               * saying "this could not be loaded" is both the honest and the
               * simpler sentence.
               */
              <p className="inc-notice" role="alert">
                Could not load resolved history: {historyError.message}
              </p>
            ) : null}
            {days.length === 0 && historyError === null ? (
              /*
               * An explicit empty state, because a card with nothing in it is
               * ambiguous: it could mean "a quiet month" or "we failed to
               * load". Suppressed when there IS an error, because the error
               * above already says which of the two it is and "nothing
               * resolved in the last 30 days" under it would be the card
               * asserting exactly the good news it does not have. The undated
               * case is stated separately rather than silently dropped —
               * `groupByDay` cannot place an incident with no start date on
               * any day, and a reader is owed the count rather than a shorter
               * list.
               */
              <p className="inc-notice" role="status">
                {shownResolved.length === 0
                  ? `Nothing resolved in the last ${historyDays} ${
                      historyDays === 1 ? "day" : "days"
                    }.`
                  : `${shownResolved.length} resolved ${
                      shownResolved.length === 1 ? "incident" : "incidents"
                    } could not be placed on a day — no start time was recorded.`}
              </p>
            ) : null}
            {days.map((day) => (
              <section key={day.key} className="inc-day">
                {/*
                 * The day heading carries its own totals.
                 *
                 * "2 incidents · 24m total" is the sentence someone writes in
                 * a status update the next morning, and having to add up the
                 * rows to produce it is work the screen can do.
                 */}
                <h3 className="inc-day-head">
                  <span className="inc-label">{day.label}</span>
                  <span className="mon-detail-note">
                    {day.items.length}{" "}
                    {day.items.length === 1 ? "incident" : "incidents"} ·{" "}
                    {formatDuration(day.totalS)} total
                  </span>
                </h3>
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
             * What the screen shows when there genuinely is more.
             *
             * The old card could only confess: it said "this is incomplete"
             * and left the reader with nowhere to go, because the API had no
             * way to ask for the rest. `has_more` comes with `next_cursor`, so
             * the honest statement now has a control attached to it — the
             * reader learns the list is short *and* can lengthen it.
             *
             * Rendered only when the owner passed a loader. A button that says
             * "load older" and does nothing would be the dead control this
             * ticket exists to avoid, one notch louder than an empty toolbar.
             */}
            {historyHasMore && onLoadMoreHistory !== undefined ? (
              <p className="inc-more">
                <button
                  type="button"
                  className="add-button"
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
        </Card>
      )}
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
 * History grouped by the day it happened, newest day first.
 *
 * Newest first here and oldest first above, and the reversal is deliberate:
 * the open list is a work queue where the longest-running outage is the most
 * urgent, and history is a record where the most recent day is the one being
 * asked about.
 *
 * "Today" and "Yesterday" are spelled out rather than dated, because that is
 * how the reader thinks about them — and every other day keeps its date, since
 * "3 days ago" stops being countable almost immediately.
 */
function groupByDay(incidents: readonly Incident[], now: number): Day[] {
  const days = new Map<string, Day>();
  const sorted = [...incidents].sort(
    (a, b) => (b.startedAt ?? 0) - (a.startedAt ?? 0),
  );
  for (const incident of sorted) {
    if (incident.startedAt === null) continue;
    const date = new Date(incident.startedAt);
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
