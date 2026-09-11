/**
 * How old the newest data on screen is, in words.
 *
 * Its own module because a .tsx that exports helpers as well as components
 * breaks fast refresh, and because this is pure arithmetic that deserves a
 * test without a renderer.
 */

/** "just now", "2 min ago" — coarse on purpose; a live second counter draws
 *  the eye away from the monitors and is never the useful fact. */
export function describeAge(since: number | null | undefined, now: number): string | null {
  if (since === null || since === undefined) return null;
  const seconds = Math.floor((now - since) / 1000);
  if (seconds < 0) return null;
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours} h ago`;
}
