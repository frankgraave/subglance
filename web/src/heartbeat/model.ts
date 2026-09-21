/**
 * Geometry and aggregation for the heartbeat bar.
 *
 * Everything here is pure and DOM-free, because the interesting decisions are
 * arithmetic, not markup: how 500 checks collapse into 40 bars without hiding
 * a failure, and how a latency becomes a height that still means something
 * when one probe took 40x longer than the rest.
 */

/** One stored check, mirroring GET /api/monitors/:id/heartbeats. */
export type Beat = {
  /** Unix milliseconds. */
  ts: number;
  ok: boolean;
  assessment?: "up" | "warning" | "down" | "";
  /** Round-trip time, or null when the probe produced no timing at all. */
  latencyMs: number | null;
  statusCode?: number;
  error?: string;
};

/** A drawn column that has no data behind it: history simply does not reach here. */
export type EmptySlot = { kind: "empty"; index: number };

/** A drawn column backed by one or more checks. */
export type BeatSlot = {
  kind: "beat";
  index: number;
  /** Checks folded into this column; 1 unless the series was bucketed. */
  count: number;
  /** Oldest and newest timestamp in the bucket (equal when count is 1). */
  from: number;
  to: number;
  /** False if *any* check in the bucket failed. Worst wins, never the average. */
  ok: boolean;
  assessment?: "up" | "warning" | "down" | "";
  downCount: number;
  warningCount?: number;
  /** Slowest measured latency in the bucket; null if none of them timed. */
  latencyMs: number | null;
  statusCode?: number;
  error?: string;
  /**
   * How many checks this column *should* hold, given the series' own cadence,
   * or null when the cadence cannot be established.
   */
  expected: number | null;
  /**
   * True when the column is missing a meaningful share of the checks it should
   * hold. A bucket that covers an hour but contains two checks is not a
   * healthy hour; before this it was drawn identically to a complete one.
   */
  partial: boolean;
};

export type Slot = EmptySlot | BeatSlot;

/**
 * Height fractions. A failure is always drawn full height and a healthy check
 * never is, so "tallest bar" is unambiguously "broken" even for the ~8% of men
 * who cannot separate the red from the green (DESIGN.md §2.3).
 */
export const MIN_OK_HEIGHT = 0.18;
export const MAX_OK_HEIGHT = 0.92;
export const UNKNOWN_LATENCY_HEIGHT = 0.45;
export const DOWN_HEIGHT = 1;

/** How many columns fit in `width` pixels. */
export function slotCountFor(width: number, barWidth: number, gap: number): number {
  if (width <= 0 || barWidth <= 0) return 0;
  return Math.max(1, Math.floor((width + gap) / (barWidth + gap)));
}

/**
 * Minimum tooltip width in pixels, mirroring `min-width` on `.hb-tooltip` in
 * heartbeat.css. Used as the fallback when the tooltip has not been measured
 * yet (first render, or an environment without layout).
 */
export const TOOLTIP_MIN_WIDTH = 148;

/** Breathing room kept between the tooltip and the edge of the viewport. */
export const TOOLTIP_VIEWPORT_MARGIN = 8;

export type TooltipPlacement = {
  /** Centre of the active column, in track coordinates. */
  columnCentre: number;
  tooltipWidth: number;
  /** Left edge of the track, in viewport coordinates. */
  trackLeft: number;
  viewportWidth: number;
  margin?: number;
};

/**
 * Horizontal offset for the tooltip, in track coordinates.
 *
 * The tooltip is centred on its column and then clamped against the
 * **viewport**, not against the track. Clamping to the track is wrong as soon
 * as the track is narrower than the tooltip (a dashboard row, a sidebar): the
 * tooltip would be pinned inside a 60px column and cut off, while there is
 * plenty of room next to it. Clamping to the viewport lets it overhang the
 * track and only stops at the window edge, which is the edge that actually
 * clips.
 *
 * A tooltip wider than the viewport cannot fit either way; it is aligned to
 * the left margin so the beginning stays readable.
 */
export function tooltipLeft({
  columnCentre,
  tooltipWidth,
  trackLeft,
  viewportWidth,
  margin = TOOLTIP_VIEWPORT_MARGIN,
}: TooltipPlacement): number {
  const ideal = columnCentre - tooltipWidth / 2;
  const min = margin - trackLeft;
  const max = viewportWidth - margin - tooltipWidth - trackLeft;
  if (max < min) return min;
  return Math.min(Math.max(ideal, min), max);
}

/**
 * Share of its expected checks a column must hold before it counts as whole.
 *
 * Not 1: a check scheduled every 60s does not land every 60s, so the last
 * bucket of a series is routinely one check short of arithmetic and flagging
 * that would make the marker meaningless. 0.9 tolerates the ordinary jitter
 * and still catches a bucket that lost a tenth of its window.
 */
export const PARTIAL_COVERAGE = 0.9;

/**
 * The series' own check interval, in milliseconds, or null if it cannot be
 * told.
 *
 * The **median** gap rather than the mean, because the mean is dragged up by
 * exactly the events this is meant to detect: one four-hour outage in a day of
 * 60s checks pulls the average interval up far enough that every gappy bucket
 * then looks complete. The median ignores the outage and reports the schedule.
 *
 * Read from the data instead of from the monitor's configured interval on
 * purpose: the stored history is what is being drawn, and a monitor whose
 * interval was changed last week has history at the old cadence. The
 * configured number would mark all of it partial.
 */
export function cadence(beats: Beat[]): number | null {
  if (beats.length < 2) return null;
  const gaps: number[] = [];
  for (let i = 1; i < beats.length; i++) {
    const gap = beats[i].ts - beats[i - 1].ts;
    if (gap > 0) gaps.push(gap);
  }
  if (gaps.length === 0) return null;
  gaps.sort((a, b) => a - b);
  const mid = Math.floor(gaps.length / 2);
  return gaps.length % 2 === 1
    ? gaps[mid]
    : Math.round((gaps[mid - 1] + gaps[mid]) / 2);
}

/**
 * How many checks a window from `from` to `to` should contain at `cadenceMs`.
 *
 * Inclusive of both ends: a window holding a single check spans 0ms and still
 * contains one check, so the count is the number of intervals plus one.
 */
export function expectedChecks(
  from: number,
  to: number,
  cadenceMs: number | null,
): number | null {
  if (cadenceMs === null || cadenceMs <= 0 || to < from) return null;
  return Math.round((to - from) / cadenceMs) + 1;
}

/**
 * Aggregates one bucket of checks into a column.
 *
 * `bounds` is the window the column *stands for*, which is not the same as the
 * window its surviving checks span. Deriving the expected count from
 * `group[0]` and `group[last]` lets a bucket that lost its first and last
 * checks shrink its own yardstick: twenty missing checks at the edges make the
 * window look twenty checks shorter, so the gap cancels itself out and the
 * column claims to be complete. The caller knows where the bucket really
 * starts and ends — from its neighbours — and passes that in.
 */
function aggregate(
  group: Beat[],
  index: number,
  cadenceMs: number | null,
  bounds?: { from: number; to: number },
): BeatSlot {
  const downs = group.filter((b) => !b.ok);
  const latencies = group
    .map((b) => b.latencyMs)
    .filter((v): v is number => v !== null && Number.isFinite(v));
  // The bucket's identity is its worst check: that is the fact a monitoring
  // tool must not average away.
  const confirmed = downs.filter((b) => b.assessment === "down");
  const unclassified = downs.filter((b) => !b.assessment);
  const failures = confirmed.length ? confirmed : unclassified.length ? unclassified : downs;
  const worst = failures.length > 0 ? failures[failures.length - 1] : group[group.length - 1];
  const from = group[0].ts;
  const to = group[group.length - 1].ts;
  const expected = expectedChecks(
    bounds?.from ?? from,
    bounds?.to ?? to,
    cadenceMs,
  );
  return {
    kind: "beat",
    index,
    count: group.length,
    from,
    to,
    ok: downs.length === 0,
    downCount: downs.length,
    warningCount: downs.filter((b) => b.assessment === "warning").length,
    assessment: downs.length > 0 && downs.every((b) => b.assessment === "warning") ? "warning" : worst.assessment,
    latencyMs: latencies.length > 0 ? Math.max(...latencies) : null,
    statusCode: worst.statusCode,
    error: downs.length > 0 ? worst.error : undefined,
    expected,
    // A single-check column falls out of this without a special case: it
    // spans no window, so it expects exactly the one check it has and one is
    // never below 90% of one.
    partial: expected !== null && group.length < expected * PARTIAL_COVERAGE,
  };
}

/**
 * Lays `beats` (oldest first) out over exactly `slotCount` columns, newest on
 * the right.
 *
 * Fewer beats than columns: the missing history is padded on the left with
 * empty slots rather than by stretching the bars. A short history should look
 * short; a bar that widened to fill the space would claim data we do not have.
 *
 * More beats than columns: contiguous buckets, remainder given to the oldest
 * ones so the newest column stays as fine-grained as possible — that is the
 * column the eye lands on.
 */
export function toSlots(beats: Beat[], slotCount: number): Slot[] {
  if (slotCount <= 0) return [];
  // Measured once over the whole series, not per bucket: a bucket that lost
  // most of its checks would otherwise derive its own, wider cadence from what
  // little it has left and declare itself complete.
  const step = cadence(beats);
  if (beats.length === 0) {
    return Array.from({ length: slotCount }, (_, index) => ({ kind: "empty", index }) as EmptySlot);
  }
  if (beats.length <= slotCount) {
    const pad = slotCount - beats.length;
    const slots: Slot[] = Array.from(
      { length: pad },
      (_, index) => ({ kind: "empty", index }) as EmptySlot,
    );
    beats.forEach((beat, i) => slots.push(aggregate([beat], pad + i, step)));
    return slots;
  }

  const base = Math.floor(beats.length / slotCount);
  const remainder = beats.length % slotCount;
  const groups: Beat[][] = [];
  let cursor = 0;
  for (let i = 0; i < slotCount; i++) {
    const size = base + (i < remainder ? 1 : 0);
    groups.push(beats.slice(cursor, cursor + size));
    cursor += size;
  }

  return groups.map((group, i) => {
    // The bucket's real window, taken from where its neighbours stop rather
    // than from its own surviving checks: a bucket missing the checks at its
    // edges must not get a shorter window to be measured against.
    //
    // The newest bucket is the exception, and deliberately so: it is still
    // filling up, so its window ends at its last check rather than at a border
    // that does not exist yet. Without that, every render would flag the
    // right-hand column as incomplete.
    const previous = groups[i - 1];
    const next = groups[i + 1];
    const from =
      step !== null && previous !== undefined && previous.length > 0
        ? previous[previous.length - 1].ts + step
        : group[0].ts;
    const to =
      step !== null && next !== undefined && next.length > 0
        ? next[0].ts - step
        : group[group.length - 1].ts;
    return aggregate(group, i, step, {
      from: Math.min(from, group[0].ts),
      to: Math.max(to, group[group.length - 1].ts),
    });
  });
}

/**
 * The latency that maps to a full-height healthy bar.
 *
 * The 95th percentile rather than the maximum: a single 8-second outlier would
 * otherwise squash every normal bar into an identical stub and destroy exactly
 * the trend the component exists to show. Anything above the ceiling clamps.
 */
export function latencyCeiling(slots: Slot[]): number {
  const latencies = slots
    .filter((s): s is BeatSlot => s.kind === "beat" && s.ok && s.latencyMs !== null)
    .map((s) => s.latencyMs as number)
    .sort((a, b) => a - b);
  if (latencies.length === 0) return 0;
  const index = Math.min(latencies.length - 1, Math.ceil(latencies.length * 0.95) - 1);
  return Math.max(latencies[Math.max(index, 0)], 1);
}

/** Height of a column as a fraction of the track, 0 for an empty slot. */
export function barHeight(slot: Slot, ceiling: number): number {
  if (slot.kind === "empty") return 0;
  if (!slot.ok) return DOWN_HEIGHT;
  if (slot.latencyMs === null || ceiling <= 0) return UNKNOWN_LATENCY_HEIGHT;
  const scaled = MIN_OK_HEIGHT + (MAX_OK_HEIGHT - MIN_OK_HEIGHT) * (slot.latencyMs / ceiling);
  return Math.min(Math.max(scaled, MIN_OK_HEIGHT), MAX_OK_HEIGHT);
}

export type SlotStatus = "up" | "down" | "warning" | "unknown" | "empty";

export function slotStatus(slot: Slot): SlotStatus {
  if (slot.kind === "empty") return "empty";
  if (!slot.ok) return slot.assessment === "warning" ? "warning" : "down";
  return slot.latencyMs === null ? "unknown" : "up";
}

/** Counts for the graphic's accessible description. */
export function summarise(slots: Slot[]): { checks: number; failed: number; span: [number, number] | null } {
  let checks = 0;
  let failed = 0;
  let from: number | null = null;
  let to: number | null = null;
  for (const slot of slots) {
    if (slot.kind !== "beat") continue;
    checks += slot.count;
    failed += slot.downCount;
    if (from === null) from = slot.from;
    to = slot.to;
  }
  return { checks, failed, span: from === null || to === null ? null : [from, to] };
}
