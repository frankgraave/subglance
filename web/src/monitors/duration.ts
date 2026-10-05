/**
 * Durations in a form: a number and a unit, over a wire value in one fixed
 * unit (seconds for most monitor settings, minutes for a maintenance window,
 * days for the certificate warning).
 *
 * The API counts in its own unit and should; a person should not have to.
 * "900" in a box labelled seconds is a sum the reader has to do before they
 * know whether it is a quarter of an hour, and the rest of the product already
 * writes the answer ("15 min", see `formatDuration`). So a loaded value is
 * shown in the largest unit that holds it exactly, and whatever the person
 * types is multiplied back out to the wire unit.
 *
 * Exactly, not approximately: 731 seconds stays "731 seconds" rather than
 * becoming "12 minutes", because a form that rounds what it loaded would save
 * a value nobody chose on the first unrelated edit.
 */

import { formatDuration } from "../format/format";

export type DurationUnit = {
  id: string;
  /** The unit's word for one, and for any other amount. */
  one: string;
  many: string;
  /** The addon text when the unit is the only one on offer. */
  short: string;
  /** How many wire units make one of these. */
  size: number;
};

const unit = (id: string, one: string, short: string, size: number): DurationUnit =>
  ({ id, one, many: `${one}s`, short, size });

const SECOND = unit("s", "second", "sec", 1);
const MINUTE = unit("min", "minute", "min", 60);
const HOUR = unit("h", "hour", "h", 3600);
const DAY = unit("d", "day", "days", 86400);

/** A wire value in seconds that never exceeds a day: check interval, first repeat. */
export const SECONDS_TO_HOURS: readonly DurationUnit[] = [SECOND, MINUTE, HOUR];
/** A wire value in seconds up to a month: a push monitor's window and grace. */
export const SECONDS_TO_DAYS: readonly DurationUnit[] = [SECOND, MINUTE, HOUR, DAY];
/** A wire value in seconds that tops out at two minutes: a probe's timeout. */
export const SECONDS_ONLY: readonly DurationUnit[] = [SECOND];
/** A wire value in whole days: the certificate warning. */
export const DAYS_ONLY: readonly DurationUnit[] = [unit("d", "day", "days", 1)];
/** A wire value in minutes: a weekly maintenance window's length. */
export const MINUTES_TO_HOURS: readonly DurationUnit[] = [unit("min", "minute", "min", 1), unit("h", "hour", "h", 60)];

/**
 * The largest unit that holds `value` exactly, or the smallest one when none
 * does or there is no positive number to hold (empty, zero, not a number).
 */
export function unitFor(value: string, units: readonly DurationUnit[]): DurationUnit {
  const wire = Number(value);
  if (value.trim() === "" || !Number.isFinite(wire) || wire <= 0) return units[0];
  for (let i = units.length - 1; i > 0; i--) {
    if (wire % units[i].size === 0) return units[i];
  }
  return units[0];
}

/** `value` counted in `unit`, as the box should show it. Text that is not a number stays as typed. */
export function amountIn(value: string, unit: DurationUnit): string {
  const wire = Number(value);
  if (value.trim() === "" || !Number.isFinite(wire)) return value;
  return String(wire / unit.size);
}

/**
 * What the person typed, multiplied out to the wire unit.
 *
 * Text that is not a number passes through untouched so the form's own
 * check rejects it under the field, rather than this turning it into a number
 * nobody typed. The product is rounded to a millionth only to absorb binary
 * fractions (1.1 minutes is 66, not 66.00000000000001); it is never rounded
 * to a whole number, so 1.01 minutes reaches the form's check as 60.6 seconds
 * and is refused there instead of being saved as 61.
 */
export function toWire(amount: string, unit: DurationUnit): string {
  const typed = Number(amount);
  if (amount.trim() === "" || !Number.isFinite(typed)) return amount;
  return String(Math.round(typed * unit.size * 1e6) / 1e6);
}

/** The unit as a word after `amount`: "1 minute", "15 minutes". */
export const unitName = (unit: DurationUnit, amount: string): string =>
  Number(amount) === 1 ? unit.one : unit.many;

/**
 * Whether two wire values say the same thing.
 *
 * A form that keeps numbers rather than text hands back `"0"` for an emptied
 * box and `"NaN"` for one holding a word; neither is a new value from outside,
 * and treating them as one would overwrite what is being typed.
 */
export const sameWire = (a: string, b: string): boolean =>
  a === b || Object.is(Number(a), Number(b));

/**
 * The range the API accepts for each duration, in its wire unit, and what a
 * value outside it is told: the range written the way the rest of the product
 * writes a length of time, and that the wire value must be whole, so a
 * fraction of a minute that is not a whole number of seconds is refused
 * rather than rounded into something nobody typed. Both monitor forms read
 * this, so they cannot disagree.
 */
const limit = (what: string, min: number, max: number, days = false) => ({
  min, max,
  message: `${what} must be between ${formatDuration(days ? min * 86400 : min)} and ${formatDuration(days ? max * 86400 : max)}, in whole ${days ? "days" : "seconds"}.`,
});
export const DURATION_LIMITS: Record<string, { min: number; max: number; message: string }> = {
  interval_s: limit("The check interval", 20, 86400),
  timeout_s: limit("The timeout", 1, 120),
  push_interval_s: limit("The report interval", 60, 2592000),
  push_grace_s: limit("The allowed lateness", 0, 2592000),
  ssl_warn_days: limit("The certificate warning", 1, 365, true),
};

/** Whether `value` is a whole number within `field`'s limits. */
export function durationAllowed(field: string, value: string | number): boolean {
  const { min, max } = DURATION_LIMITS[field];
  const number = Number(value);
  return String(value).trim() !== "" && Number.isInteger(number) && number >= min && number <= max;
}
