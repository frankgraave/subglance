import { Card, Panel } from "../components/Card";
import { IconAlert, IconClock } from "../components/icons";
import { IncidentStoryItem } from "./IncidentStoryItem";
import { IncidentClusterItem } from "./IncidentClusterItem";
import { clusterIncidents } from "./cluster";
import { describeChurn, incidentState } from "./story";
import { formatDuration } from "../monitors/detail";
import type { IncidentEntry } from "./cluster";
import type { Incident } from "../monitors/detail";

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
 * different moods. The top one is the 03:00 screen — tinted, loud, every row
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
   * different endpoints: the open list is instance-wide, and resolved history
   * is per monitor. An empty array means "none to show", which is why the card
   * states its own emptiness rather than disappearing.
   */
  resolved?: readonly Incident[];
  /**
   * How far back the history card reaches, in days.
   *
   * A prop rather than a constant read here, so a longer window or a date
   * picker changes the data owner and not this component. Nothing is staged
   * for that today — a disabled control shipped ahead of its feature is a
   * promise with the wiring cut.
   */
  historyDays?: number;
  /** True when the history fan-out was capped; the card says so rather than
   *  presenting a partial month as a complete one. */
  historyTruncated?: boolean;
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
  ackingId?: string | null;
  /** The ack that failed, and why. Shown once, above the list. */
  ackError?: Error | null;
  /** True when the live stream is dead (DESIGN.md §6). */
  stale?: boolean;
};

export function IncidentsView({
  incidents,
  resolved = [],
  historyDays = 30,
  historyTruncated = false,
  names = {},
  now,
  monitorCount,
  loading = false,
  error = null,
  onAck,
  ackingId = null,
  ackError = null,
  stale = false,
}: IncidentsViewProps) {
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
  const entries = clusterIncidents(incidents);
  const ackedCount = incidents.filter(
    (i) => incidentState(i) === "acked",
  ).length;

  /*
   * Churn is computed per monitor, not over the whole list.
   *
   * Five monitors with one incident each is a bad afternoon; one monitor with
   * five is a monitor that is bouncing, and the engine has quietly stopped
   * alerting on it. Those are different sentences and only the per-monitor
   * count can tell them apart. Both lists feed it: a flapping monitor's
   * incidents keep resolving, so counting only the open ones would miss
   * exactly the pattern this is for.
   */
  const byMonitor = new Map<string, Incident[]>();
  for (const incident of [...incidents, ...resolved]) {
    const list = byMonitor.get(incident.monitorId);
    if (list === undefined) byMonitor.set(incident.monitorId, [incident]);
    else list.push(incident);
  }
  const churn: { monitorId: string; note: string }[] = [];
  for (const [monitorId, list] of byMonitor) {
    const note = describeChurn(list, now);
    if (note !== null) churn.push({ monitorId, note });
  }

  const days = groupByDay(resolved);
  const nothingAtAll =
    !loading &&
    error === null &&
    incidents.length === 0 &&
    resolved.length === 0;

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
      <header className="mon-detail-head">
        <h1 className="mon-detail-name">Incidents</h1>
      </header>

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

      {nothingAtAll ? (
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
      ) : (
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
                : `${incidents.length} open · ${ackedCount} acknowledged`}
            </span>
          }
        >
          <Panel padded={false}>
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
                      ackingId={ackingId}
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
                      acking={ackingId === entry.incident.id}
                      stale={stale}
                    />
                  ),
                )}
              </ul>
            )}
          </Panel>
        </Card>
      )}

      {days.length === 0 ? null : (
        <Card
          title="Resolved"
          icon={<IconClock />}
          headingLevel={2}
          action={
            <span className="mon-detail-note">
              Last {historyDays} days · grouped by day
            </span>
          }
        >
          <Panel padded={false}>
            {historyTruncated ? (
              /*
               * Said, not hidden. The history is assembled one request per
               * monitor because the API has no instance-wide endpoint for
               * resolved incidents, and the fan-out is capped — so on a large
               * instance this card is genuinely incomplete. Presenting a
               * partial month as if it were the whole month is the one thing
               * a monitoring tool must not do.
               */
              <p className="inc-notice" role="status">
                Showing history for the first monitors only — the API has no
                instance-wide endpoint for resolved incidents yet, so this card
                is assembled one monitor at a time.
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
          </Panel>
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
function groupByDay(incidents: readonly Incident[]): Day[] {
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
      day = { key, label: dayLabel(date), items: [], totalS: 0 };
      days.set(key, day);
    }
    day.items.push(incident);
    day.totalS += Number.isFinite(incident.durationS) ? incident.durationS : 0;
  }
  return [...days.values()];
}

function dayLabel(date: Date): string {
  const today = new Date();
  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate();
  if (sameDay(date, today)) return "Today";
  const yesterday = new Date(today.getTime() - 86_400_000);
  if (sameDay(date, yesterday)) return "Yesterday";
  return date.toLocaleDateString(undefined, { day: "numeric", month: "short" });
}
