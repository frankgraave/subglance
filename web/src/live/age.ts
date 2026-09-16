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

/**
 * The same span, worded as a gap rather than as a moment: "4 min", "2 h".
 *
 * `describeAge` dates a reading — "checked 4 min ago" — which is a sentence
 * about something that happened. Once the stream is dead the useful sentence
 * is about something that has *not* happened, and "no data for 4 min ago" is
 * not English. Rather than have the caller slice the "ago" off a string, the
 * two phrasings are two functions over the same arithmetic.
 *
 * Under a minute is spelled out instead of returning "just now": a silence of
 * forty seconds is not worth alarming anyone about, but "no data for just now"
 * would be nonsense, and rounding it to "0 min" reads as a bug.
 */
export function describeGap(since: number | null | undefined, now: number): string | null {
  if (since === null || since === undefined) return null;
  const seconds = Math.floor((now - since) / 1000);
  if (seconds < 0) return null;
  if (seconds < 60) return "under a minute";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  return `${hours} h`;
}
