/**
 * Which clock a push monitor's window is counted on.
 *
 * The server counts a window of whole days on an IANA zone's clock when the
 * monitor has a `push_timezone`, so a daily job scheduled at 06:00 local time
 * is due at 06:00 local time even the night the clocks go back, when it runs
 * 25 hours after the previous run. Any other interval is counted in elapsed
 * time, and the server refuses a zone beside one.
 */

const DAY_S = 86400;

/** Whether a push interval, in seconds, can be counted on a zone's clock. */
export function pushIntervalTakesZone(intervalS: number | string): boolean {
  const seconds = Number(intervalS);
  return Number.isInteger(seconds) && seconds > 0 && seconds % DAY_S === 0;
}

/**
 * The zone to send for an interval: what was typed, trimmed, when the
 * interval is whole days, and nothing otherwise. A zone left in the box after
 * the interval became hourly is hidden with the box, and sending it would get
 * a refusal about a field nobody can see.
 */
export function pushZoneFor(intervalS: number | string, zone: string): string {
  return pushIntervalTakesZone(intervalS) ? zone.trim() : "";
}
