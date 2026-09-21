import { Card, Panel } from "../components/Card";
import { Legend, type LegendItem } from "../components/Legend";
import { IconAlert, IconGauge, IconPulse } from "../components/icons";
import { HeartbeatBar } from "../heartbeat/HeartbeatBar";
import type { Beat } from "../heartbeat/model";
import { describeAge, describeGap } from "../live/age";
import {
  describePushWindow,
  formatLatency,
  formatUptime,
  statusWord,
} from "./format";
import type { Incident, UptimeWindow } from "./detail";
import { IncidentStoryItem } from "../incidents/IncidentStoryItem";
import { describeChurn } from "../incidents/story";
import { Led } from "./Led";
import { Unknown } from "./Unknown";
import type { Monitor } from "./types";
import { ResponseHistory, type ResponseHistoryProps } from "./ResponseHistory";
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
 * like recently, how reliable is it over real windows, and what has already
 * gone wrong. Reference data (interval, type) stays on the inventory screen;
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
  /** Now, in unix ms, for relative ages. Passed in so render stays pure. */
  now: number;
  /** True when the extra panels are still loading. */
  loading?: boolean;
  /** Why the extra panels are missing, if they are. */
  error?: Error | null;
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
  checking?: boolean;
  checkResult?: CheckOutcome;
  checkError?: Error | null;
};

export function MonitorDetail({
  monitor,
  windows,
  incidents,
  responseHistory,
  now,
  loading = false,
  error = null,
  onBack,
  beatWidth = DETAIL_BEAT_WIDTH,
  stale = false,
  onAck,
  ackingIds = EMPTY_ACKING,
  ackError = null,
  onCheckNow,
  checking = false,
  checkResult,
  checkError = null,
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

  return (
    <article
      className="mon-detail"
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
       * Identity and state, in one block.
       *
       * The name, the target and the state used to be three children of a
       * 20px flex column, so the three facts about one monitor read as three
       * unrelated rows with the right half of the screen empty beside them.
       * They are one thing, and they are grouped now.
       *
       * The state rides on the title line as a pill: it is the second thing
       * asked for after "which monitor is this", and putting it there answers
       * both in one glance instead of two.
       */}
      <header className="mon-detail-head">
        <div className="mon-detail-titlerow">
          <h1 className="mon-detail-name">{name}</h1>
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
              <span>{statusWord(status, true)}</span>
            ) : (
              <strong>{statusWord(status, false)}</strong>
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
        </div>
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
        {status === "down" && lastError ? (
          <p className="mon-detail-reason">{lastError}</p>
        ) : null}
        {status === "waiting" ? (
          <p className="mon-detail-reason mon-detail-reason--quiet">
            Nothing has reported in yet.
          </p>
        ) : null}
      </header>

      <Card
        title={push === undefined ? "Recent checks" : "Recent reports"}
        icon={<IconPulse />}
        headingLevel={2}
        action={push === undefined && onCheckNow !== undefined ? (
          <button type="button" className="add-button" onClick={onCheckNow} disabled={checking}>
            {checking ? "Checking…" : "Check now"}
          </button>
        ) : undefined}
      >
        <Panel>
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

      {responseHistory ? <ResponseHistory key={monitor.id} {...responseHistory} /> : null}

      <Card title="Uptime" icon={<IconGauge />} headingLevel={2}>
        <p className="mon-detail-uptime-note">Only confirmed downtime counts. Warnings and history without a recorded assessment are excluded. This is a sample ratio, not elapsed time.</p>
        <Panel>
          {error !== null ? (
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
                      formatUptime(w.uptime)
                    )}
                  </dd>
                  <dd className="mon-detail-window-detail">
                    {w.total === 0
                      ? "no eligible checks"
                      : `${w.down} of ${w.total} confirmed down`}
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
        </Panel>
      </Card>

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
          ) : incidents.length === 0 ? (
            // Said positively. "No incidents" reads as missing data; this reads
            // as the good news it actually is.
            <p className="mon-detail-empty">Nothing has gone wrong yet.</p>
          ) : (
            /*
             * No `subject` on this page: the monitor's name is the page title
             * six inches above, and repeating it on every row would spend the
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
                />
              ))}
            </ul>
          )}
        </Panel>
      </Card>
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
 * Two entries, never more. A `Beat` carries `ok` and nothing else, so a third
 * status here would be a colour the plot never draws — a legend that names
 * marks which are not on screen is worse than none.
 *
 * A status with no occurrences is still listed. "0 failed" is a reading; an
 * absent row is silence, and the difference matters on the one panel someone
 * opens to find out whether anything went wrong.
 */
function legendItems(beats: Beat[]): LegendItem[] {
  const failed = beats.reduce((n, beat) => (beat.ok ? n : n + 1), 0);
  return [
    { key: "up", label: "Passed", marker: "up", value: beats.length - failed },
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
