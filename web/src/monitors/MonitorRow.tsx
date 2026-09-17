import { memo } from "react";
import { HeartbeatBar } from "../heartbeat/HeartbeatBar";
import { describeTarget, formatLatency, formatUptime } from "./format";
import { Led } from "./Led";
import { MonitorLink } from "./MonitorLink";
import { Unknown } from "./Unknown";
import type { Monitor } from "./types";

/**
 * One monitor as one table row.
 *
 * Memoised because the dashboard renders up to 200 of these and each one
 * carries a ~40-rect heartbeat bar. There is no virtualisation here by
 * decision (DESIGN.md §10, research note 1): 200 rows is roughly 11k DOM
 * nodes, which the browser handles fine, and a virtualised list breaks
 * Ctrl-F, breaks screen-reader row counts and breaks table semantics. What it
 * does need is that a tick on one monitor does not re-render the other 199 —
 * that is this `memo` plus a stable `key={monitor.id}`.
 */

/** How many heartbeat columns a row shows. DESIGN.md §4: the last 28 checks. */
export const ROW_BEAT_WIDTH = 168;

export type MonitorRowProps = {
  monitor: Monitor;
  /**
   * Heartbeat width for environments without layout. jsdom reports every
   * element as 0 wide, so without this the bar renders empty in tests; in the
   * browser the bar measures its own column and this is not used.
   */
  beatWidth?: number;
  /** Opens this monitor's detail view client-side. See MonitorLink. */
  onOpen?: (id: string) => void;
  /**
   * True when the live stream is dead, so the lamp's word reads "Was up".
   *
   * Passed down rather than read from CSS: the row's status word is `sr-only`
   * for `up` and visible otherwise, and `[data-conn="stale"]` in a stylesheet
   * cannot rewrite either of them. Draining only the colour would leave the
   * text saying "Up" to the readers the text exists for (DESIGN.md §6, §2.3).
   */
  stale?: boolean;
};

function MonitorRowImpl({
  monitor,
  beatWidth = ROW_BEAT_WIDTH,
  onOpen,
  stale = false,
}: MonitorRowProps) {
  const { name, status, latencyMs, uptime24h, beats, error } = monitor;

  return (
    <tr
      className="mon-row"
      data-status={status}
      data-testid={`monitor-row-${monitor.id}`}
    >
      {/*
       * The lamp, and the status word as `sr-only` text beside it (SUB-140).
       *
       * The product owner asked for the lamp alone in this cell — "graag
       * alleen de Led, geen tekst er achter" — with the cell smaller, squarer,
       * and the lamp centred in it both ways.
       *
       * The word is hidden, never removed. It is the status for anyone using a
       * screen reader, and `hideLabel` clips it rather than dropping it, so
       * the row's accessible name is unchanged: "Down, api.example.com,
       * Latency, …" reads exactly as it did.
       *
       * What carries the status for a *sighted* reader who cannot separate the
       * hues is no longer this cell, and that is a real narrowing of §9 — see
       * DESIGN.md §9.1, which now states where the second signal lives
       * instead: the heartbeat bar's height, which §2.3 already names as a
       * non-colour carrier (a failed check is drawn full height), plus, for
       * `down`, the row's position under a counted "Needs attention (n)"
       * heading and the failure reason printed in words where the latency
       * would be.
       */}
      <td className="mon-cell mon-cell--led">
        <Led status={status} stale={stale} />
      </td>

      {/* scope="row" makes the name the row's header, so a screen reader
          announces "api.example.com, Latency, 120 ms" when you move across
          the row instead of reading a bare number. */}
      <th scope="row" className="mon-cell mon-cell--name">
        <MonitorLink
          id={monitor.id}
          name={name}
          onOpen={onOpen}
          className="mon-name"
        />
        <span className="mon-target">{describeTarget(monitor)}</span>
      </th>

      <td className="mon-cell mon-cell--beats">
        {/* Not interactive here (SUB-100). One focusable bar per row put 402
            tab stops in front of the last row's link at 200 monitors, and one
            sr-only table per row put the DOM at ~33k nodes against the ~11k
            budget above. The row already says status, latency, uptime and the
            failure reason in text; the bar is the trend, and the detail view
            keeps the readable version. */}
        <HeartbeatBar
          beats={beats}
          label={name}
          width={beatWidth}
          height={26}
          barWidth={4}
          gap={2}
          interactive={false}
        />
      </td>

      <td className="mon-cell mon-cell--num">
        {status === "down" && error ? (
          // A failed check has no latency to report, so the column carries the
          // reason instead — colour plus text, never colour alone (§2.3).
          <span className="mon-error" title={error}>
            {error}
          </span>
        ) : latencyMs === null ? (
          <Unknown what="latency" />
        ) : (
          formatLatency(latencyMs)
        )}
      </td>

      <td className="mon-cell mon-cell--num">
        {uptime24h === null ? (
          <Unknown what="uptime" />
        ) : (
          formatUptime(uptime24h)
        )}
      </td>
    </tr>
  );
}

/**
 * Compares only the fields the row draws. The default shallow compare would
 * do, but `beats` is a fresh array on every poll even when the checks are
 * identical, which would defeat the memo for exactly the update it exists to
 * absorb.
 */
export const MonitorRow = memo(MonitorRowImpl, (prev, next) => {
  const a = prev.monitor;
  const b = next.monitor;
  return (
    prev.beatWidth === next.beatWidth &&
    // Compared because it changes the rendered word. A memo that ignored it
    // would freeze "Up" on screen for as long as the monitor's own fields
    // happened to stay equal — which, once the stream is dead, is forever.
    prev.stale === next.stale &&
    // Compared, not ignored: the row renders it into a link's handler, so a
    // changed callback must reach the DOM. It is a stable useCallback in
    // practice, so this costs nothing.
    prev.onOpen === next.onOpen &&
    a.id === b.id &&
    a.name === b.name &&
    a.status === b.status &&
    a.target === b.target &&
    a.latencyMs === b.latencyMs &&
    a.uptime24h === b.uptime24h &&
    a.error === b.error &&
    a.lastCheck === b.lastCheck &&
    a.beats.length === b.beats.length &&
    // Beats are append-only and oldest-first, so the newest timestamp is a
    // sufficient fingerprint for "the series changed".
    a.beats[a.beats.length - 1]?.ts === b.beats[b.beats.length - 1]?.ts
  );
});
