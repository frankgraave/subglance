import { Card, Panel } from "../components/Card";
import { Legend, type LegendItem } from "../components/Legend";
import { IconAlert, IconClock, IconGauge, IconPause, IconPlay, IconPulse, IconTrash } from "../components/icons";
import { Menu, type MenuItem } from "../components/Menu";
import { HeartbeatBar } from "../heartbeat/HeartbeatBar";
import type { Beat } from "../heartbeat/model";
import { describeAge, describeGap, formatUptime } from "../format/format";
import {
  describePushWindow,
  formatLatency,
  statusWord,
  waitingReason,
} from "./format";
import { windowCoverageCaveat, type Incident, type UptimeWindow } from "./detail";
import { Value } from "../components/Value";
import { IncidentStoryItem } from "../incidents/IncidentStoryItem";
import { describeChurn } from "../incidents/story";
import { Led } from "./Led";
import { Unknown } from "./Unknown";
import type { Monitor } from "./types";
import type { ResponseHistoryProps } from "./ResponseHistory";
import { LazyResponseHistory } from "./LazyResponseHistory";
import { LatencyChart, type LatencyChartProps } from "./LatencyChart";
import type { CheckOutcome } from "./inventoryApi";

/** Shared empty default: a new Set per render would break memoisation. */
const EMPTY_ACKING: ReadonlySet<string> = new Set();

/**
 * One monitor, in full.
 *
 * The dashboard answers "is anything wrong". This answers "what is wrong with
 * this one, and how long has it been like that" — which is a different
 * question and needs a different shape, not a wider row (DESIGN.md §12).
 *
 * The order down the page is the order the questions get asked, and it is not
 * the order of the API: what is it, is it up and why not, what has it looked
 * like recently, how reliable is it over real windows, is it getting slower,
 * what has already gone wrong — and only then the raw failed checks behind
 * those answers. Reference data (interval, type) stays on the inventory screen;
 * this screen uses the existing live list rather than a separate settings read.
 *
 * Presentational, like `Dashboard`: it fetches nothing. `MonitorDetailRoute`
 * above it owns the data, which is what lets this render from a fixture.
 */

/**
 * Heartbeat width on the detail page, in pixels.
 *
 * Much wider than the row's 168 and the card's 295, and that is the point: the
 * ticket asks for the heartbeat "over a longer window than the row shows", and
 * the bar buckets checks to fit its width. At 168px the row folds 100 checks
 * into 28 slots, each one an aggregate that hides which check failed. At this
 * width the same 100 checks get a slot each, so an isolated blip stops being
 * averaged away.
 *
 * Only the fallback for environments without layout. In a browser the bar
 * measures its own panel, which is what keeps this number off a phone: 720px
 * inside a 317px panel used to push the page sideways (SUB-29).
 */
export const DETAIL_BEAT_WIDTH = 720;

export type MonitorDetailProps = {
  monitor: Monitor;
  windows: readonly UptimeWindow[];
  incidents: readonly Incident[];
  /** Raw diagnostic history, independent of the bulk/live beat bar. */
  responseHistory?: ResponseHistoryProps;
  /**
   * The latency chart over a chosen window. Omitted for push monitors, which
   * are reported to rather than probed and so have no latency to draw.
   */
  latency?: LatencyChartProps;
  /** Now, in unix ms, for relative ages. Passed in so render stays pure. */
  now: number;
  /** True when the extra panels are still loading. */
  loading?: boolean;
  /** Why the extra panels are missing, if they are. */
  error?: Error | null;
  /**
   * True once the extra panels' read has succeeded at least once.
   *
   * The read is polled, so an error can arrive on top of figures that were
   * already on screen. Blanking them for one failed refresh replaces a
   * slightly old answer with no answer, and the latency chart and failure
   * responses on the same page already keep theirs. With this set, an error
   * keeps the last figures and says the refresh failed; without it, an error
   * is a failed first load and nothing is drawn under it.
   */
  loaded?: boolean;
  /** Returns to the dashboard. */
  onBack?: () => void;
  /** Heartbeat width for environments without layout, such as jsdom. */
  beatWidth?: number;
  /**
   * True when the live stream is down.
   *
   * It drains colour exactly as on the list, and it moves the status word out
   * of the present tense — see the pill below for why that is markup and not
   * another CSS rule.
   */
  stale?: boolean;
  /**
   * Acknowledges one incident: "seen, working on it".
   *
   * Offered here rather than only on the incidents screen because ack exists
   * to stop an escalating repeat sequence, and the detail view is where
   * somebody lands from an alert link. A control that is one navigation
   * further away than the place you arrive is a control that does not get
   * used at 03:00 — which leaves the repeats running.
   *
   * Absent means the control is not drawn at all: a viewer cannot write, and
   * a button that always answers 403 is worse than no button.
   */
  onAck?: (id: string) => void;
  /** The incident currently being acknowledged, if any. */
  /** Incidents whose ack request is still in flight. */
  ackingIds?: ReadonlySet<string>;
  /** The ack that failed, and why. */
  ackError?: Error | null;
  /** Absent for read-only users; push monitors never render this action. */
  onCheckNow?: () => void;
  onEdit?: () => void;
  checking?: boolean;
  checkResult?: CheckOutcome;
  checkError?: Error | null;
  /**
   * Pauses or resumes this monitor; the argument is the state asked for.
   *
   * Offered here for the same reason ack is: the detail screen is where an
   * alert link lands, and "stop checking this while I fix it" is the first
   * thing asked there. Absent for read-only users, like every other write.
   */
  onTogglePaused?: (paused: boolean) => void;
  /** True while a pause, resume or delete request is in flight. */
  busy?: boolean;
  /**
   * Asks to delete this monitor. The screen that owns the data confirms it
   * (retyping the name, DESIGN.md §7.5); this only opens that question.
   */
  onDelete?: () => void;
  /** Why the last pause, resume or delete failed, if it did. */
  actionError?: Error | null;
  /**
   * Opens this monitor's maintenance schedule. Absent for read-only users:
   * from here the point is to plan a window, which is a write.
   */
  onMaintenance?: () => void;
};

/**
 * The less frequent writes, behind one "More" menu.
 *
 * Edit and Check now stay as buttons: they are the two things done on this
 * screen routinely. Pause and delete are rarer and heavier, and a row of four
 * equal buttons would give a delete the same weight as a check. The menu is
 * also what lets each of them say what it does — "stops checks and alerts,
 * history is kept" — which a bare verb on a button cannot (DESIGN.md §8).
 */
function detailMenuItems(
  paused: boolean,
  busy: boolean,
  onTogglePaused: ((paused: boolean) => void) | undefined,
  onDelete: (() => void) | undefined,
  onMaintenance: (() => void) | undefined,
): MenuItem[] {
  const items: MenuItem[] = [];
  /*
   * Maintenance first, above Pause, because it is the answer to the same
   * question — "stop bothering me about this while I work on it" — and the
   * one that keeps the history: checks go on, alerts and uptime skip the
   * window. The descriptions are what lets someone pick between the two.
   */
  if (onMaintenance !== undefined) {
    items.push({
      key: "maintenance",
      title: "Schedule maintenance",
      description: "Suppresses alerts for a planned window. Checks continue.",
      icon: <IconClock />,
      onSelect: onMaintenance,
    });
  }
  if (onTogglePaused !== undefined) {
    items.push(
      paused
        ? {
            key: "resume",
            title: "Resume",
            description: "Starts checking again and alerting on failures.",
            icon: <IconPlay />,
            disabled: busy,
            onSelect: () => onTogglePaused(false),
          }
        : {
            key: "pause",
            title: "Pause",
            description: "Stops checks and alerts. History is kept.",
            icon: <IconPause />,
            disabled: busy,
            onSelect: () => onTogglePaused(true),
          },
    );
  }
  if (onDelete !== undefined) {
    items.push({
      key: "delete",
      title: "Delete",
      description: "Removes the monitor and everything recorded about it.",
      icon: <IconTrash />,
      tone: "danger",
      disabled: busy,
      onSelect: onDelete,
    });
  }
  return items;
}

export function MonitorDetail({
  monitor,
  windows,
  incidents,
  responseHistory,
  latency,
  now,
  loading = false,
  error = null,
  loaded = false,
  onBack,
  beatWidth = DETAIL_BEAT_WIDTH,
  stale = false,
  onAck,
  ackingIds = EMPTY_ACKING,
  ackError = null,
  onCheckNow,
  onEdit,
  checking = false,
  checkResult,
  checkError = null,
  onTogglePaused,
  busy = false,
  onDelete,
  actionError = null,
  onMaintenance,
}: MonitorDetailProps) {
  const {
    name,
    status,
    target,
    latencyMs,
    beats,
    lastCheck,
    error: lastError,
  } = monitor;
  const age = describeAge(lastCheck, now);
  const gap = describeGap(lastCheck, now);
  const push = monitor.push;
  const churn = describeChurn(incidents, now);
  const menuItems = detailMenuItems(status === "paused", busy, onTogglePaused, onDelete, onMaintenance);

  return (
    <article
      className="mon-detail"
      /* Named by the monitor, since its heading is the masthead's: the region
         says which monitor it is about to anything that lists regions. */
      aria-label={name}
      data-status={status}
      data-conn={stale ? "stale" : "live"}
    >
      {/*
       * A real button, not a link styled as one, and not the browser's Back.
       * Arriving here from a pasted URL means there is nothing to go back
       * *to*; this always lands on the dashboard, which is the only place the
       * control claims to go.
       */}
      <nav className="mon-detail-nav">
        <button type="button" className="mon-detail-back" onClick={onBack}>
          <span aria-hidden="true">←</span> All monitors
        </button>
      </nav>

      {/*
       * State and address, in one block.
       *
       * The name, the target and the state used to be three children of a
       * 20px flex column, so the three facts about one monitor read as three
       * unrelated rows with the right half of the screen empty beside them.
       * They are one thing, and they are grouped now.
       *
       * The name itself is the masthead's title (SUB-207): every page's `h1`
       * is there, and a second one here would make this the one page with
       * two. The state leads the block instead — it is the first thing asked
       * after "which monitor is this", and the masthead has just answered
       * that.
       */}
      <header className="mon-detail-head">
          {/*
           * Lamp, word and age — never the reason.
           *
           * The lamp gives up its label because the pill states the word
           * anyway; printing both would have a screen reader say "Up Up", the
           * duplication `MonitorCard` avoids the same way. DESIGN.md §2.3 is
           * satisfied by the word, not by the lamp.
           *
           * **The tense is the honesty (SUB-111).** With a live stream this
           * says "Up · checked 2 min ago". With a dead one it says "Was up ·
           * no data for 4 min", and the age half stops being a footnote: it
           * becomes the loud part, because how long we have been blind is the
           * fact that decides what to do next.
           *
           * The word is kept rather than replaced by "Status stale", which was
           * the obvious fix and is the wrong one: during an outage the last
           * known status is the most valuable thing on the page. "It was down
           * when we lost it" and "it was up when we lost it" send someone to
           * two different places, and blanking the word to be truthful would
           * cost more than the lie did.
           *
           * It is markup and not another rule in connection.css, unlike the
           * draining of the lamp beside it: CSS can dim text, but it cannot
           * change what the text says — and a dimmed "Up" is still an assertion
           * in the present tense.
           *
           * The emphasis swaps with the tense, and it swaps by moving the
           * `<strong>` rather than by adding a class. `<strong>` means "this
           * matters now": on a live stream that is the status, and once the
           * stream is dead it is how long we have been blind. Moving the
           * element says so in the markup, costs the stylesheet nothing — the
           * existing `.mon-detail-status > strong` rule already gives whatever
           * sits inside it full ink against the pill's drained `--ink-3` — and
           * survives a reader who has turned stylesheets off.
           */}
          <p className="mon-detail-status" data-status={status}>
            <Led status={status} labelled={false} className="mon-detail-led" />
            {stale ? (
              <span>{statusWord(status, true, monitor.recovery)}</span>
            ) : (
              <strong>{statusWord(status, false, monitor.recovery)}</strong>
            )}
            {stale && gap !== null ? (
              // The silence, not the reading's age. The two are the same
              // arithmetic, but "no data for 4 min" is a statement about now
              // and "checked 4 min ago" is a statement about then — and on a
              // dead stream only the first one is still true.
              //
              // A direct child of the pill, like the word it changes places
              // with, because `.mon-detail-status > strong` is what hands it
              // the full ink — nesting it inside the quiet age span would keep
              // the emphasis in the markup and lose it on screen.
              <>
                {" "}
                <strong>· no data for {gap}</strong>
              </>
            ) : age !== null ? (
              <span className="mon-detail-age">
                {" "}
                · {push === undefined ? "checked" : "last reported"} {age}
              </span>
            ) : null}
          </p>
        {/* Not a link. The target may be an internal host or a host:port that
            is not a URL at all, and a link that sometimes 404s the user into
            their own infrastructure is worse than text they can copy. */}
        {/* A push monitor has no address to show. Its window is what it is
            defined by, so that goes here instead of an empty line. */}
        <p className="mon-detail-target">
          {push === undefined
            ? target
            : describePushWindow(push.intervalS, push.graceS)}
        </p>
        {/*
         * The reason, on its own line and only when there is one.
         *
         * It cannot go in the pill: an error is free text — "DNS lookup
         * failed: host axonlawyers.com not found" — and a pill that grows to
         * fit one would push the title around and wrap the line it sits on.
         * Below the target it gets the column's full reading width, which is
         * what a sentence someone has to act on needs.
         */}
        {(status === "down" || status === "expiring") && lastError ? (
          <p className="mon-detail-reason">{lastError}</p>
        ) : null}
        {status === "waiting" ? (
          <p className="mon-detail-reason mon-detail-reason--quiet">
            {waitingReason(push?.waitingSince, stale)}
          </p>
        ) : null}
      </header>

      <Card
        title={push === undefined ? "Recent checks" : "Recent reports"}
        icon={<IconPulse />}
        headingLevel={2}
        action={<div className="button-row">
          {onEdit && <button type="button" className="button" aria-label="Edit monitor" onClick={onEdit}>Edit monitor</button>}
          {push === undefined && onCheckNow !== undefined && <button type="button" className="button mon-check-now" onClick={onCheckNow} disabled={checking}>
            {checking ? "Checking…" : "Check now"}
          </button>}
          {menuItems.length > 0 && <Menu
            trigger={<>More <span aria-hidden="true">▾</span></>}
            triggerLabel="More actions"
            triggerClassName="button"
            label={`Actions for ${name}`}
            align="end"
            items={menuItems}
          />}
        </div>}
      >
        <Panel>
          {actionError !== null ? (
            <p role="alert" className="mon-detail-note mon-detail-check-error">
              <IconAlert />
              <span>{actionError.message}</span>
            </p>
          ) : null}
          {push === undefined && checkError !== null ? (
            <p role="alert" className="mon-detail-note mon-detail-check-error">
              <IconAlert />
              <span>Could not run check: {checkError.message}</span>
            </p>
          ) : null}
          {push === undefined && checkResult !== undefined ? (
            <p role="status" className="mon-detail-note">
              {checkResult.ok ? "Check passed" : "Check failed"} · {formatLatency(checkResult.latencyMs)}
              {checkResult.statusCode !== undefined ? ` · HTTP ${checkResult.statusCode}` : ""}
              {checkResult.error ? ` · ${checkResult.error}` : ""}
              {checkResult.recorded ? " · Recorded" : " · Not recorded — monitor history and status are unchanged."}
            </p>
          ) : null}
          <div className="mon-detail-beats">
            <HeartbeatBar
              beats={beats}
              label={`${name} recent checks`}
              width={beatWidth}
              height={44}
              barWidth={6}
              gap={3}
              stale={stale}
              framed={beats.length > 0}
              legend={
                beats.length > 0 ? (
                  // Only when there are bars to explain. A legend above an
                  // empty plot names colours that are not on screen, which
                  // reads as a rendering fault rather than as help.
                  //
                  // Counted rather than listed: "what do these colours mean"
                  // and "how many of each" are the same question at a glance,
                  // and the bar cannot answer the second — 90 bars at 6px do
                  // not let anyone tally the red ones.
                  <Legend
                    label="What the bars mean"
                    items={legendItems(beats)}
                  />
                ) : undefined
              }
            />
          </div>
          {beats.length === 0 ? (
            // Two different facts, and only one of them is a problem. A probed
            // monitor with no checks is a monitor the scheduler has not reached
            // yet and will. A push monitor with no reports is waiting on a URL
            // somebody still has to wire up, and saying "no checks recorded"
            // sends them to look at SubGlance instead of at their crontab.
            <p className="mon-detail-empty">
              {push === undefined
                ? "No checks recorded yet."
                : "No reports yet. This monitor stays quiet until the job pings its push URL for the first time."}
            </p>
          ) : (
            <p className="mon-detail-note">
              Last {beats.length} checks, oldest first. Latest latency:{" "}
              {latencyMs === null ? (
                <Unknown what="latency" />
              ) : (
                formatLatency(latencyMs)
              )}
              .
            </p>
          )}
        </Panel>
      </Card>

      <Card title="Uptime" icon={<IconGauge />} headingLevel={2}>
        {monitor.maintenance ? (
          <p role="status">
            {stale
              ? "Scheduled maintenance when we last heard — checks continued; alerts were suppressed."
              : "Scheduled maintenance — checks continue; alerts suppressed."}
          </p>
        ) : null}
        <Panel>
          {error !== null && loaded ? (
            <p role="alert" className="mon-detail-note">
              Could not refresh uptime: {error.message}. Showing the last loaded figures.
            </p>
          ) : null}
          {error !== null && !loaded ? (
            <p
              role="alert"
              className="mon-detail-empty mon-detail-empty--quiet"
            >
              Could not load uptime: {error.message}
            </p>
          ) : loading ? (
            <p className="mon-detail-empty mon-detail-empty--quiet">
              Loading uptime…
            </p>
          ) : windows.length === 0 ? (
            <p className="mon-detail-empty">No uptime data yet.</p>
          ) : (
            <dl className="mon-detail-windows">
              {windows.map((w) => (
                <div key={w.window} className="mon-detail-window">
                  <dt>{w.window}</dt>
                  <dd className="mon-detail-window-value">
                    {/* Null is "nothing was checked in this window", which is not
                      0% — a monitor created an hour ago has no 30d uptime, and
                      showing 0% would read as a month-long outage. */}
                    {w.uptime === null ? (
                      <Unknown what="uptime" />
                    ) : (
                      // A window longer than the monitor's life is the one
                      // place this screen has a true number that claims more
                      // than it knows, so it carries the warning-value
                      // treatment (dotted underline plus glyph).
                      // No `value` prop: Value dims a zero as a reading that
                      // steps back, and 0% uptime is the opposite of that.
                      <Value
                        className="mon-detail-window-reading"
                        warning={windowCoverageCaveat(w, monitor.createdAt, now)}
                      >
                        {formatUptime(w.uptime)}
                      </Value>
                    )}
                  </dd>
                  <dd className="mon-detail-window-detail">
                    {w.total === 0
                      ? "no eligible checks"
                      : `${w.down} of ${w.total} confirmed down`}
                    {(w.maintenance ?? 0) > 0 ? ` · ${w.maintenance} maintenance checks excluded` : ""}
                    {(w.warning ?? 0) > 0 ? ` · ${w.warning} warnings excluded` : ""}
                    {(w.legacy ?? 0) > 0 ? ` · ${w.legacy} legacy checks excluded` : ""}
                    {w.avgLatencyMs !== null
                      ? ` · ${formatLatency(w.avgLatencyMs)} avg`
                      : ""}
                  </dd>
                </div>
              ))}
            </dl>
          )}
          {/*
           * How the figures are counted, under them and closed (SUB-184).
           * Always open above the numbers, it was the first thing read on a
           * card somebody opens for three percentages. It stays one press
           * away rather than gone: "why is my 24h uptime 100% when it was
           * failing" is answered here and nowhere else on the page.
           */}
          <details className="mon-detail-uptime-note">
            <summary>How uptime is counted</summary>
            <p>Only confirmed downtime counts. Warnings, maintenance checks and history without a recorded assessment are excluded. This is a sample ratio, not elapsed time.</p>
          </details>
        </Panel>
      </Card>

      {/* After uptime, not before it: uptime answers "is it reliable", which
          is the question an alert link lands with; the trend answers "is it
          getting slower", which is the next one. */}
      {latency ? <LatencyChart {...latency} /> : null}

      <Card title="Incidents" icon={<IconAlert />} headingLevel={2}>
        <Panel>
          {/*
           * Flapping suppresses the notifications, not the record.
           *
           * A monitor that oscillates has its repeat alerts stopped by the
           * engine and goes on opening incidents the whole time. Without this
           * note the panel is at its most misleading exactly when the service
           * is at its worst: three rows and a silent phone read as a problem
           * that settled down.
           */}
          {churn === null ? null : (
            <p className="inc-churn" role="status">
              {churn}
            </p>
          )}
          {ackError !== null ? (
            <p role="alert" className="inc-notice">
              Could not mute repeat alerts: {ackError.message}
            </p>
          ) : null}
          {error !== null && loaded ? (
            <p role="alert" className="mon-detail-note">
              Could not refresh incidents: {error.message}. Showing the last loaded list.
            </p>
          ) : null}
          {error !== null && !loaded ? (
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
          ) : incidents.length === 0 ? (
            // Said positively. "No incidents" reads as missing data; this reads
            // as the good news it actually is.
            <p className="mon-detail-empty">Nothing has gone wrong yet.</p>
          ) : (
            /*
             * No `subject` on this page: the monitor's name is the page title
             * in the masthead, and repeating it on every row would spend the
             * name column on a word the reader already has. The row falls back
             * to stating when the outage began, which is the fact that
             * actually distinguishes one of these rows from the next.
             */
            <ul className="inc-list">
              {incidents.map((incident) => (
                <IncidentStoryItem
                  key={incident.id}
                  incident={incident}
                  now={now}
                  onAck={onAck}
                  acking={ackingIds.has(incident.id)}
                  stale={stale}
                  past={incident.resolved}
                  showReminders
                />
              ))}
            </ul>
          )}
        </Panel>
      </Card>

      {/*
       * Last, after the incidents (SUB-184). These are the raw failed checks
       * behind the summaries above them: evidence somebody digs into once the
       * uptime, the trend and the incident have said what happened. Above
       * Uptime, a monitor that had been down for forty minutes pushed every
       * summary on the page below forty rows of the same error.
       */}
      {responseHistory ? <LazyResponseHistory key={monitor.id} {...responseHistory} /> : null}
    </article>
  );
}

/**
 * What the bars mean, with a count for each.
 *
 * Counted rather than merely named, because "what is this colour" and "how
 * many of those are there" are the same question at a glance and the bar
 * cannot answer the second: ninety columns at 6px do not let anyone tally the
 * red ones. The count is the reason this legend earns its space.
 *
 * Warning assessments have their own mark: an unconfirmed failure must not
 * be counted again as Failed. Unassessed historical failures retain their
 * raw result here; the uptime denominator is calculated separately.
 *
 * A status with no occurrences is still listed. "0 failed" is a reading; an
 * absent row is silence, and the difference matters on the one panel someone
 * opens to find out whether anything went wrong.
 */
function legendItems(beats: Beat[]): LegendItem[] {
  const warnings = beats.filter((beat) => beat.assessment === "warning").length;
  const failed = beats.filter((beat) => !beat.ok && beat.assessment !== "warning").length;
  return [
    { key: "up", label: "Passed", marker: "up", value: beats.length - failed - warnings },
    { key: "warning", label: "Warnings (unconfirmed)", marker: "warn", value: warnings },
    { key: "down", label: "Failed", marker: "down", value: failed },
  ];
}

/*
 * `IncidentItem` used to live here, and it is gone rather than moved.
 *
 * It printed "Resolved after 1 h" or "Ongoing for 15 min" with "· acknowledged"
 * tacked on the end, which is four facts short of the sentence SUB-34 asks for
 * and — more seriously — drew an acknowledged outage as a footnote on the same
 * line as a resolved one. Its replacement is `IncidentStoryItem`, shared with
 * the incidents screen so the two surfaces cannot grow separate vocabularies
 * for the three states.
 */
