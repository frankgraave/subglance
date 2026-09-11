import { useSyncExternalStore } from "react";

/**
 * A one-second clock as an external store.
 *
 * Same construction as `src/live/useNow.ts` — module-scope interval, shared by
 * every subscriber, `useSyncExternalStore` so nothing reads `Date.now()`
 * during render — but a different tick, and that difference is the point.
 * `useNow` is deliberately coarse because its consumer renders "2 min ago";
 * the status wall needs a second hand, because on a screen that almost never
 * changes a ticking second is the only proof you are looking at something
 * alive rather than a frozen tab (DESIGN.md §7).
 *
 * It is a separate store rather than a faster `useNow`: making the shared
 * clock tick every second would re-render every heartbeat age on the
 * dashboard 30x more often for no visible gain.
 */

const TICK_MS = 1000;

let current = Date.now();
let timer: ReturnType<typeof setInterval> | null = null;
const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (timer === null) {
    current = Date.now();
    timer = setInterval(() => {
      current = Date.now();
      for (const l of listeners) l();
    }, TICK_MS);
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0 && timer !== null) {
      clearInterval(timer);
      timer = null;
    }
  };
}

function snapshot(): number {
  return current;
}

/**
 * Wall-clock time, refreshed every second. Only the status wall should use it.
 *
 * Exported from a `.ts` rather than the wall component so the component file
 * keeps exporting components only (the lint rule that guards fast refresh).
 */
export function useSecondsClock(): number {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

/**
 * The clock face: 24-hour, zero-padded, with seconds.
 *
 * A fixed en-GB locale rather than the visitor's: a wall display is often a
 * shared screen whose browser locale is nobody's in particular, and 24-hour
 * time with seconds is unambiguous everywhere. Formatting is pure so the
 * padding can be tested without a browser.
 */
export function formatClock(ms: number): string {
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}
