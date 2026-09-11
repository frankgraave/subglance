import { memo } from "react";
import { HeartbeatBar } from "../heartbeat/HeartbeatBar";
import { formatLatency, formatUptime } from "./format";
import { Led } from "./Led";
import { Unknown } from "./Unknown";
import type { Monitor } from "./types";

/**
 * One monitor as one card, for phone-width viewports.
 *
 * **Why a second layout instead of a narrower table.** The desktop row packs
 * five columns into 1100px; at 375px the name alone wants most of that. The
 * usual rescue — horizontal scroll with a frozen first column — is wrong for
 * this screen: you open SubGlance on a phone *after* an alert fired, standing
 * somewhere, one-handed, and the answer has to be there without swiping
 * sideways to find the column that holds it. The other common rescue, hiding
 * the low-priority columns, would drop exactly the heartbeat and uptime that
 * make this a monitoring tool rather than a list of names (DESIGN.md §12).
 *
 * So the card keeps every fact the row shows and stacks them: status and name
 * on the first line, the heartbeat full-bleed under it, latency and uptime as
 * a labelled pair at the bottom. Column alignment is lost — that is the real
 * cost of cards — but the phone task is "read this one monitor", not "compare
 * 200 of them", and the alignment-heavy task stays on the desktop layout.
 *
 * The labels are visible here, unlike in the row. A row inherits meaning from
 * its column header; a card has no header, so "120 ms" on its own is a number
 * without a noun.
 */

/**
 * Heartbeat width inside a card, in pixels.
 *
 * Wider than the row's 168 because the card gives the bar the full content
 * width: a 375px phone minus page and card padding. The bar buckets checks to
 * fit whatever width it measures, so this is only the jsdom fallback and the
 * starting point before the container is measured.
 */
export const CARD_BEAT_WIDTH = 295;

export type MonitorCardProps = {
  monitor: Monitor;
  /** Explicit heartbeat width; required in jsdom, which has no layout. */
  beatWidth?: number;
};

function MonitorCardImpl({ monitor, beatWidth = CARD_BEAT_WIDTH }: MonitorCardProps) {
  const { name, status, target, latencyMs, uptime24h, beats, error } = monitor;

  return (
    <li className="mon-card" data-status={status} data-testid={`monitor-card-${monitor.id}`}>
      <div className="mon-card-head">
        {/* Labelled, and visibly so: the card has no column header to lend the
            lamp its meaning, and the status is the first thing being asked
            for. Colour plus word, never colour alone (DESIGN.md §2.3).
            The word comes from the lamp itself rather than a span beside it —
            printing it twice would have a screen reader say "Up Up". */}
        <Led status={status} hideLabel={false} className="mon-card-led" />
      </div>

      <h3 className="mon-card-name">{name}</h3>
      <p className="mon-card-target">{target}</p>

      {/* The heartbeat survives the move to the phone unchanged. It is the
          trend half of "status and trend without tapping through", and a
          sparkline-shaped substitute would be a second visual language for
          the same fact. */}
      <div className="mon-card-beats">
        <HeartbeatBar beats={beats} label={name} width={beatWidth} height={30} barWidth={5} gap={3} />
      </div>

      {status === "down" && error ? (
        <p className="mon-card-error">{error}</p>
      ) : null}

      <dl className="mon-card-facts">
        <div className="mon-card-fact">
          <dt>Latency</dt>
          <dd>{latencyMs === null ? <Unknown what="latency" /> : formatLatency(latencyMs)}</dd>
        </div>
        <div className="mon-card-fact">
          <dt>24h uptime</dt>
          <dd>{uptime24h === null ? <Unknown what="uptime" /> : formatUptime(uptime24h)}</dd>
        </div>
      </dl>
    </li>
  );
}

/** Same memo contract as MonitorRow: `beats` is a fresh array on every poll. */
export const MonitorCard = memo(MonitorCardImpl, (prev, next) => {
  const a = prev.monitor;
  const b = next.monitor;
  return (
    prev.beatWidth === next.beatWidth &&
    a.id === b.id &&
    a.name === b.name &&
    a.status === b.status &&
    a.target === b.target &&
    a.latencyMs === b.latencyMs &&
    a.uptime24h === b.uptime24h &&
    a.error === b.error &&
    a.lastCheck === b.lastCheck &&
    a.beats.length === b.beats.length &&
    a.beats[a.beats.length - 1]?.ts === b.beats[b.beats.length - 1]?.ts
  );
});
