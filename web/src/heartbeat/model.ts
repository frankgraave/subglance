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
  downCount: number;
  /** Slowest measured latency in the bucket; null if none of them timed. */
  latencyMs: number | null;
  statusCode?: number;
  error?: string;
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

function aggregate(group: Beat[], index: number): BeatSlot {
  const downs = group.filter((b) => !b.ok);
  const latencies = group
    .map((b) => b.latencyMs)
    .filter((v): v is number => v !== null && Number.isFinite(v));
  // The bucket's identity is its worst check: that is the fact a monitoring
  // tool must not average away.
  const worst = downs.length > 0 ? downs[downs.length - 1] : group[group.length - 1];
  return {
    kind: "beat",
    index,
    count: group.length,
    from: group[0].ts,
    to: group[group.length - 1].ts,
    ok: downs.length === 0,
    downCount: downs.length,
    latencyMs: latencies.length > 0 ? Math.max(...latencies) : null,
    statusCode: worst.statusCode,
    error: downs.length > 0 ? worst.error : undefined,
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
  if (beats.length === 0) {
    return Array.from({ length: slotCount }, (_, index) => ({ kind: "empty", index }) as EmptySlot);
  }
  if (beats.length <= slotCount) {
    const pad = slotCount - beats.length;
    const slots: Slot[] = Array.from(
      { length: pad },
      (_, index) => ({ kind: "empty", index }) as EmptySlot,
    );
    beats.forEach((beat, i) => slots.push(aggregate([beat], pad + i)));
    return slots;
  }

  const base = Math.floor(beats.length / slotCount);
  const remainder = beats.length % slotCount;
  const slots: Slot[] = [];
  let cursor = 0;
  for (let i = 0; i < slotCount; i++) {
    const size = base + (i < remainder ? 1 : 0);
    slots.push(aggregate(beats.slice(cursor, cursor + size), i));
    cursor += size;
  }
  return slots;
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

export type SlotStatus = "up" | "down" | "unknown" | "empty";

export function slotStatus(slot: Slot): SlotStatus {
  if (slot.kind === "empty") return "empty";
  if (!slot.ok) return "down";
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
