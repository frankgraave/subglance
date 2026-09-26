import { memo } from "react";
import { Card } from "../components/Card";
import { IconList } from "../components/icons";
import { PanelList, PanelRow } from "../components/PanelList";
import { Value } from "../components/Value";
import { EmptyState } from "./EmptyState";
import { describeTarget, formatLatency, formatUptime } from "./format";
import { Led } from "./Led";
import { MonitorLink } from "./MonitorLink";
import { partition, sectionsByTag } from "./model";
import type { Monitor } from "./types";
import { Unknown } from "./Unknown";

/**
 * The dense layout: one line per monitor, for screens holding 100+.
 *
 * **Why a list and not a shorter table.** The row layout earns its `<table>`
 * by inviting comparison down a column — latency against latency, uptime
 * against uptime. This layout is for the opposite question: "is anything red?"
 * across a hundred lines you are not reading individually. Dropping to a list
 * halves the DOM per monitor, keeps find-in-page working, and stops promising
 * a grid whose columns nobody scans.
 *
 * **What it keeps.** Lamp, name, target, latency, uptime — the same facts,
 * just on one line with the heartbeat bar dropped. The bar is the one thing
 * that cannot survive this density: 40 rects x 200 monitors is the cost the
 * layout exists to avoid, and at this line height it would be 8px of noise.
 * Anyone who wants the trend switches to Rows, which is one click away.
 *
 * **Grouping is opt-in, and flat is the default.** DESIGN.md §7 groups this
 * layout by customer or environment, and it now can: choosing a tag key puts
 * one heading per value over the lines. Without a key it stays a single flat
 * `<ul>` ordered by `partition` (down first, then alphabetical), which is the
 * ordering every layout shares — at this density the broken monitors are
 * already the first lines on the screen, and permanent headings would cost
 * vertical space the layout exists to save.
 *
 * **What it does not drop: the status in words.** The lamp shows its label
 * here for every status except `up`, for the reason the row does (SUB-100):
 * at this density down, pending and paused were three hues of the same 20x7
 * pill. `up` stays wordless so the exception reads as the exception.
 *
 * **What it does not drop: the reason.** A down monitor has no latency to
 * report, so this layout borrows the row's rule and puts the error text in
 * that slot instead. Dropping it would leave a red lamp as the only signal —
 * colour alone (DESIGN.md §2.3), and a line that says something is broken
 * without saying what. The line is one line tall, so the text truncates with
 * an ellipsis and carries a `title` for the rest.
 */

export type MonitorCompactListProps = {
  monitors: readonly Monitor[];
  /** Non-empty when the list has been filtered, used only for empty-state copy. */
  query?: string;
  /** Total before filtering, so "no results" can be told from "no monitors". */
  totalCount?: number;
  /** True when a filter other than the query is narrowing the list. */
  filtered?: boolean;
  /** Tag key to group by, or null for one flat list. */
  groupKey?: string | null;
  /** Opens a monitor's detail view client-side. See MonitorLink. */
  onOpen?: (id: string) => void;
  /** Opens the add form from the empty state; see EmptyState. */
  onAddMonitor?: () => void;
  /**
   * True when the live stream is dead. Forwarded to every line so its status
   * word moves into the past tense (DESIGN.md §6). It matters most here: this
   * layout hides the word for `up`, so without it a hundred lines would go on
   * telling a screen reader "Up" with nothing on screen to contradict them.
   */
  stale?: boolean;
};

type CompactLineProps = {
  monitor: Monitor;
  onOpen?: (id: string) => void;
  stale?: boolean;
};

function CompactLineImpl({ monitor, onOpen, stale = false }: CompactLineProps) {
  const { name, status, latencyMs, uptime24h, error } = monitor;
  return (
    <PanelRow
      className="mon-line"
      status={status}
      data-testid={`monitor-line-${monitor.id}`}
      icon={
        <Led
          status={status}
          hideLabel={status === "up"}
          stale={stale}
          className="mon-line-led"
        />
      }
    >
      <MonitorLink
        id={monitor.id}
        name={name}
        onOpen={onOpen}
        className="mon-line-name"
      />
      <span className="mon-line-target">{describeTarget(monitor)}</span>
      <span className="mon-line-num">
        {status === "down" && error ? (
          <span className="mon-line-error" title={error}>
            {error}
          </span>
        ) : latencyMs === null ? (
          <Unknown what="latency" />
        ) : (
          // The raw number goes in beside the formatted text so a measured
          // zero dims as data rather than being mistaken for a missing one.
          <Value value={latencyMs}>{formatLatency(latencyMs)}</Value>
        )}
      </span>
      <span className="mon-line-num">
        {uptime24h === null ? (
          <Unknown what="uptime" />
        ) : (
          <Value value={uptime24h}>{formatUptime(uptime24h)}</Value>
        )}
      </span>
    </PanelRow>
  );
}

/** Memoised for the same reason MonitorRow is: one tick must not redraw 199 lines. */
const CompactLine = memo(CompactLineImpl, (prev, next) => {
  const a = prev.monitor;
  const b = next.monitor;
  return (
    prev.onOpen === next.onOpen &&
    // See MonitorRow: it changes the word, so it has to defeat the memo.
    prev.stale === next.stale &&
    a.id === b.id &&
    a.name === b.name &&
    a.status === b.status &&
    a.target === b.target &&
    a.latencyMs === b.latencyMs &&
    a.uptime24h === b.uptime24h &&
    // `error` is compared because the line now renders it. Leaving it out
    // would pin a stale reason on screen for as long as the other five fields
    // happened to stay equal.
    a.error === b.error
  );
});

export function MonitorCompactList({
  monitors,
  query = "",
  totalCount,
  filtered = false,
  groupKey = null,
  onOpen,
  onAddMonitor,
  stale = false,
}: MonitorCompactListProps) {
  const total = totalCount ?? monitors.length;
  if (monitors.length === 0) {
    return (
      <EmptyState
        query={query}
        totalCount={total}
        filtered={filtered}
        onAddMonitor={onAddMonitor}
      />
    );
  }

  const lines = (list: readonly Monitor[]) =>
    list.map((monitor) => (
      <CompactLine
        key={monitor.id}
        monitor={monitor}
        onOpen={onOpen}
        stale={stale}
      />
    ));

  if (groupKey !== null) {
    return (
      <div className="mon-lines">
        {sectionsByTag(monitors, groupKey).map((section) => (
          <Card
            key={section.id}
            className={
              section.attention
                ? "mon-line-group mon-line-group--attention"
                : "mon-line-group"
            }
            title={`${section.label} (${section.monitors.length})`}
            icon={<IconList />}
            headingLevel={3}
          >
            {/* The rows sit straight on the card: a line IS the panel, so a
                wrapper around them would be a third surface framing a second
                one. See `.mon-line-stack` for the measurement. */}
            <PanelList className="mon-line-stack">
              {lines(section.monitors)}
            </PanelList>
          </Card>
        ))}
      </div>
    );
  }

  // `partition` orders the list — down first, then alphabetical — and that is
  // all it does here: without a grouping key this layout stays flat.
  const { attention, rest } = partition(monitors);
  const ordered = [...attention, ...rest];

  return (
    <div className="mon-lines">
      <Card
        title={`Monitors (${ordered.length})`}
        icon={<IconList />}
        headingLevel={2}
      >
        <PanelList className="mon-line-stack">{lines(ordered)}</PanelList>
      </Card>
    </div>
  );
}
