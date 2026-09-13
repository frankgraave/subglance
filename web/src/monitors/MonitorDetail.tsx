import { HeartbeatBar } from "../heartbeat/HeartbeatBar";
import { describeAge } from "../live/age";
import { formatLatency, formatUptime, STATUS_LABEL } from "./format";
import { formatDuration, formatMoment } from "./detail";
import type { Incident, UptimeWindow } from "./detail";
import { Led } from "./Led";
import { Unknown } from "./Unknown";
import type { Monitor } from "./types";

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
 * gone wrong. Reference data (interval, type) is deliberately absent — the
 * server does not return it on the list endpoint, and inventing a second fetch
 * for facts nobody opens this page to read would slow down the ones they do.
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
 * averaged away. Only the jsdom fallback — in a browser the bar measures its
 * own container and uses the real width.
 */
export const DETAIL_BEAT_WIDTH = 720;

export type MonitorDetailProps = {
  monitor: Monitor;
  windows: readonly UptimeWindow[];
  incidents: readonly Incident[];
  /** Now, in unix ms, for relative ages. Passed in so render stays pure. */
  now: number;
  /** True when the extra panels are still loading. */
  loading?: boolean;
  /** Why the extra panels are missing, if they are. */
  error?: Error | null;
  /** Returns to the dashboard. */
  onBack?: () => void;
  /** Explicit heartbeat width; required in jsdom, which has no layout. */
  beatWidth?: number;
  /** True when the live stream is down; drains colour exactly as on the list. */
  stale?: boolean;
};

export function MonitorDetail({
  monitor,
  windows,
  incidents,
  now,
  loading = false,
  error = null,
  onBack,
  beatWidth = DETAIL_BEAT_WIDTH,
  stale = false,
}: MonitorDetailProps) {
  const { name, status, target, latencyMs, beats, lastCheck, error: lastError } = monitor;
  const age = describeAge(lastCheck, now);

  return (
    <article className="mon-detail" data-status={status} data-conn={stale ? "stale" : "live"}>
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

      <header className="mon-detail-head">
        <h1 className="mon-detail-name">{name}</h1>
        {/* Not a link. The target may be an internal host or a host:port that
            is not a URL at all, and a link that sometimes 404s the user into
            their own infrastructure is worse than text they can copy. */}
        <p className="mon-detail-target">{target}</p>
      </header>

      {/*
       * The status sentence, which is the one thing that has to survive being
       * read on a phone at arm's length. It says the state, the reason when
       * there is one, and when it was last confirmed — the three halves of
       * "what is wrong and how long has it been like that".
       */}
      {/*
       * Lamp and word, in one sentence rather than two places.
       *
       * The lamp was originally a second, labelled copy up in the header. That
       * made the page say the status twice — "Down. Down — connection
       * refused" to a screen reader — which is the same duplication
       * `MonitorCard` avoids by letting the lamp carry the word alone. Here
       * the sentence needs the word anyway, because it continues into the
       * reason, so the lamp gives up its label and becomes what it looks
       * like: the colour beside the sentence (DESIGN.md §2.3 is satisfied by
       * the word, not by the lamp).
       */}
      <p className="mon-detail-status">
        <Led status={status} labelled={false} className="mon-detail-led" />
        <strong>{STATUS_LABEL[status]}</strong>
        {status === "down" && lastError ? <> — {lastError}</> : null}
        {age !== null ? <span className="mon-detail-age"> · checked {age}</span> : null}
      </p>

      <section className="mon-detail-panel" aria-labelledby="mon-detail-beats-title">
        <h2 id="mon-detail-beats-title" className="mon-detail-panel-title">
          Recent checks
        </h2>
        <div className="mon-detail-beats">
          <HeartbeatBar
            beats={beats}
            label={`${name} recent checks`}
            width={beatWidth}
            height={44}
            barWidth={6}
            gap={3}
          />
        </div>
        {beats.length === 0 ? (
          <p className="mon-detail-empty">No checks recorded yet.</p>
        ) : (
          <p className="mon-detail-note">
            Last {beats.length} checks, oldest first. Latest latency:{" "}
            {latencyMs === null ? <Unknown what="latency" /> : formatLatency(latencyMs)}.
          </p>
        )}
      </section>

      <section className="mon-detail-panel" aria-labelledby="mon-detail-uptime-title">
        <h2 id="mon-detail-uptime-title" className="mon-detail-panel-title">
          Uptime
        </h2>
        {error !== null ? (
          <p role="alert" className="mon-detail-empty">
            Could not load uptime: {error.message}
          </p>
        ) : loading ? (
          <p className="mon-detail-empty">Loading uptime…</p>
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
                  {w.uptime === null ? <Unknown what="uptime" /> : formatUptime(w.uptime)}
                </dd>
                <dd className="mon-detail-window-detail">
                  {w.total === 0
                    ? "no checks"
                    : `${w.down} of ${w.total} failed`}
                  {w.avgLatencyMs !== null ? ` · ${formatLatency(w.avgLatencyMs)} avg` : ""}
                </dd>
              </div>
            ))}
          </dl>
        )}
      </section>

      <section className="mon-detail-panel" aria-labelledby="mon-detail-incidents-title">
        <h2 id="mon-detail-incidents-title" className="mon-detail-panel-title">
          Incidents
        </h2>
        {error !== null ? (
          <p role="alert" className="mon-detail-empty">
            Could not load incidents: {error.message}
          </p>
        ) : loading ? (
          <p className="mon-detail-empty">Loading incidents…</p>
        ) : incidents.length === 0 ? (
          // Said positively. "No incidents" reads as missing data; this reads
          // as the good news it actually is.
          <p className="mon-detail-empty">Nothing has gone wrong yet.</p>
        ) : (
          <ol className="mon-detail-incidents">
            {incidents.map((incident) => (
              <IncidentItem key={incident.id} incident={incident} />
            ))}
          </ol>
        )}
      </section>
    </article>
  );
}

function IncidentItem({ incident }: { incident: Incident }) {
  const started = formatMoment(incident.startedAt);
  return (
    <li className="mon-detail-incident" data-resolved={incident.resolved ? "true" : "false"}>
      <span className="mon-detail-incident-when">
        {/* <time> only when there is a real instant behind it: a dateTime
            attribute built from an unparseable timestamp is worse than none,
            because it is machine-readable and wrong. */}
        {started === null || incident.startedAt === null ? (
          "Unknown start"
        ) : (
          <time dateTime={new Date(incident.startedAt).toISOString()}>{started}</time>
        )}
      </span>
      <span className="mon-detail-incident-state">
        {incident.resolved
          ? `Resolved after ${formatDuration(incident.durationS)}`
          : `Ongoing for ${formatDuration(incident.durationS)}`}
        {incident.acked ? " · acknowledged" : ""}
      </span>
      {incident.lastError ? (
        <span className="mon-detail-incident-error">{incident.lastError}</span>
      ) : incident.cause ? (
        <span className="mon-detail-incident-error">{incident.cause}</span>
      ) : null}
    </li>
  );
}
