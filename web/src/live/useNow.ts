import { useSyncExternalStore } from "react";

/**
 * The current time as an external store, shared by every subscriber.
 *
 * Reading `Date.now()` during render is impure: two renders of the same props
 * would differ, which React is free to notice. A clock is exactly the kind of
 * external, already-existing value `useSyncExternalStore` exists for — the
 * same reasoning as `useMediaQuery` in src/layout.
 *
 * The tick, the cached value and the listener set live at module scope rather
 * than inside the hook. A snapshot function that returns a fresh `Date.now()`
 * on every call is an infinite render loop, and one interval shared by every
 * badge on the page beats one per component.
 */

const TICK_MS = 30_000;

let current = Date.now();
let timer: ReturnType<typeof setInterval> | null = null;
const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (timer === null) {
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
 * Wall-clock time, refreshed every 30 seconds.
 *
 * Coarse on purpose: the only consumer renders "2 min ago", so a per-second
 * re-render of the dashboard would be pure cost.
 */
export function useNow(): number {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
