/**
 * How every date, time, duration, count and percentage is written.
 *
 * One module because the app had at least four ways to write a date — "Oct 1,
 * 2026, 01:59 PM" on one card, "10/1/2026, 1:59:44 PM GMT+2" on the next —
 * and three ways to write an uptime (59.60%, 97.2%, 100%). Each one was a
 * reasonable local choice, a dozen `toLocale*` calls with their own options,
 * and together they read as if several products had been stitched into one.
 * `format.guard.test.ts` refuses a date, time or number formatted anywhere
 * else, so a thirteenth local choice fails the build instead of a review.
 *
 * The rules, one per kind (DESIGN.md §2.5 states them for readers):
 *
 * - **Moment** — `1 Oct 2026, 13:59`: an absolute point in time, where it has
 *   to be lined up against a deploy log or somebody else's screenshot.
 * - **Date** — `1 Oct 2026`: a day with no time worth stating (created on,
 *   expires on).
 * - **Day** — `1 Oct`: a day inside a list or a window that is already
 *   recent, where the year is noise.
 * - **Day and time** — `1 Oct, 13:59`: an axis label or a range end inside a
 *   window of days.
 * - **Clock** — `13:59`: a time on a day the sentence already names.
 * - **Relative** — `4 min ago`: how old the newest data is, coarse on purpose.
 * - **Duration** — `3 h 41 min`: how long something lasted, to the unit that
 *   matters.
 * - **Uptime** — `99.95%`: two decimals, always, rounded down.
 * - **Count** — `43,138`: grouped thousands.
 *
 * English month names and a 24-hour clock, whatever the browser's locale.
 * The interface is English, so a date in the browser's own language was a
 * second language on the same line ("1 okt. 2026" beside "Down since"), and a
 * 12-hour clock in an en-US browser put "PM" into columns sized for "13:59".
 * Day before month, because "1 Oct" cannot be misread in either direction
 * and "10/1" can. The time zone is the reader's own, everywhere, and so is
 * not printed: a zone on four cards and none on the other twenty said the
 * twenty were in some other zone. The one exception is the server-side
 * schedule on the retention card, which says in words that it follows the
 * server's clock.
 *
 * Built by hand from the Date's parts rather than through `Intl`: with the
 * locale pinned, `Intl.DateTimeFormat` adds nothing but a dependency on the
 * ICU build ("Sep" in one engine is "Sept" in another, under en-GB).
 */

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

const pad = (n: number) => String(n).padStart(2, "0");

/** What a formatter writes for an instant that is not a number. */
const UNKNOWN = "—";

/**
 * "13:59", or "13:59:44" with seconds; null for no time at all, so a caller
 * holding a nullable timestamp can write its own word for "never".
 */
export function formatClock(ms: number, seconds?: boolean): string;
export function formatClock(ms: number | null | undefined, seconds?: boolean): string | null;
export function formatClock(ms: number | null | undefined, seconds = false): string | null {
  if (ms === null || ms === undefined) return null;
  if (!Number.isFinite(ms)) return UNKNOWN;
  const d = new Date(ms);
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  return seconds ? `${hm}:${pad(d.getSeconds())}` : hm;
}

/** "1 Oct". */
export function formatDay(ms: number): string {
  if (!Number.isFinite(ms)) return UNKNOWN;
  const d = new Date(ms);
  return `${d.getDate()} ${MONTHS[d.getMonth()]}`;
}

/** "1 Oct 2026". */
export function formatDate(ms: number): string {
  if (!Number.isFinite(ms)) return UNKNOWN;
  return `${formatDay(ms)} ${new Date(ms).getFullYear()}`;
}

/** "1 Oct, 13:59", or "1 Oct, 13:59:44" with seconds. */
export function formatDayTime(ms: number, seconds = false): string {
  if (!Number.isFinite(ms)) return UNKNOWN;
  return `${formatDay(ms)}, ${formatClock(ms, seconds)}`;
}

/**
 * "1 Oct 2026, 13:59": an absolute moment, or null when there is none.
 *
 * Absolute rather than relative, unlike the dashboard's "2 min ago": the
 * detail view is where you reconstruct what happened and line it up against
 * a deploy log or somebody else's screenshot, and "2 min ago" is unusable for
 * that the moment the page has been open for a while.
 */
export function formatMoment(ms: number | null | undefined): string | null {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return null;
  return `${formatDate(ms)}, ${formatClock(ms)}`;
}

/** An ISO timestamp, as the API sends it, as a moment. */
export function formatMomentIso(iso: string): string {
  return formatMoment(Date.parse(iso)) ?? UNKNOWN;
}

/** An ISO timestamp as its date alone. */
export function formatDateIso(iso: string): string {
  return formatDate(Date.parse(iso));
}

/** Whether two instants fall on the same calendar day, in the reader's zone. */
export function sameDay(a: number, b: number): boolean {
  const x = new Date(a);
  const y = new Date(b);
  return x.getFullYear() === y.getFullYear() && x.getMonth() === y.getMonth() && x.getDate() === y.getDate();
}

/**
 * A moment dated only as much as it needs to be: "14:15" on the same day as
 * `reference`, the full moment on any other, null when there is none.
 *
 * "Recovered at 02:40" without a day is a sentence that quietly loses
 * twenty-four hours, and repeating the date the sentence already gave makes
 * the line twice as long to say nothing.
 */
export function formatMomentFrom(ms: number | null | undefined, reference: number | null): string | null {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return null;
  if (reference === null || !Number.isFinite(reference) || !sameDay(ms, reference)) return formatMoment(ms);
  return formatClock(ms);
}

/**
 * "just now", "2 min ago" — coarse on purpose; a live second counter draws
 * the eye away from the monitors and is never the useful fact.
 */
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

/**
 * A duration in seconds as the shortest sentence that is still exact enough.
 *
 * An outage is judged by order of magnitude — "4 min" and "3 h" lead to
 * different conversations, "3 h 41 min 12 s" leads to the same one as "3 h"
 * while taking longer to read. Seconds only survive below a minute, where
 * they are the whole story.
 */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "unknown";
  if (seconds < 60) return `${Math.floor(seconds)} s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    const rest = minutes % 60;
    return rest === 0 ? `${hours} h` : `${hours} h ${rest} min`;
  }
  const days = Math.floor(hours / 24);
  const rest = hours % 24;
  return rest === 0 ? `${days} d` : `${days} d ${rest} h`;
}

/**
 * How long a job ran, in milliseconds: "0.4 s" below ten seconds, where the
 * tenth is the difference between instant and noticeable, and `formatDuration`
 * above it.
 */
export function formatRunTime(ms: number): string {
  if (Number.isFinite(ms) && ms >= 0 && ms < 10_000) return `${(ms / 1000).toFixed(1)} s`;
  return formatDuration(ms / 1000);
}

const COUNT = new Intl.NumberFormat("en", { maximumFractionDigits: 0 });

/** "43,138": a whole count with grouped thousands. */
export function formatCount(n: number): string {
  return COUNT.format(n);
}

/** The number of decimals every uptime figure is written to. */
export const UPTIME_DECIMALS = 2;

/**
 * An uptime share, in percent, as every screen writes it: two decimals,
 * rounded down, never up — "99.95%", "100.00%", "0.00%".
 *
 * Two decimals because they are the precision at which uptime is talked
 * about: one confirmed-down minute in a month is 99.99% at two places and
 * 100.0% at one, rounded down to 99.9% — the figure that separates an SLA
 * that held from one that did not. The same two on every screen, including
 * the exact ends, so a column of figures lines up on the decimal point and
 * the dashboard and the public status page cannot disagree about what one
 * window rounds to. The public page's figure comes from the server
 * (`Uptime` in `internal/statuspage/history.go`) with the same rule.
 *
 * Down, not to nearest: 21 confirmed-down checks out of 43,138 is 99.951%,
 * which to-nearest prints as "100%" at one place — the number people read as
 * "never down", printed right above the line that counts the outages. So
 * 100.00 is reserved for exactly 100, which only happens with zero down
 * checks (the API divides up by total, and n / n is exactly 1). The tiny
 * epsilon absorbs float noise: 29 of 100 comes out of up / total * 100 as
 * 28.999999999999996, which a bare floor would print as 28.99. The clamp
 * catches the one case the epsilon can push the wrong way, a share a
 * billionth short of 100.
 */
export function formatUptime(pct: number): string {
  const scale = 10 ** UPTIME_DECIMALS;
  if (pct >= 100) return `${(100).toFixed(UPTIME_DECIMALS)}%`;
  const floored = Math.max(0, Math.floor(pct * scale + 1e-9) / scale);
  return `${Math.min(floored, 100 - 1 / scale).toFixed(UPTIME_DECIMALS)}%`;
}
