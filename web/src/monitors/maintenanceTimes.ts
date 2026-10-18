import { formatClock, formatDuration, formatMomentFrom, formatMomentIso } from "../format/format";

/*
 * The clock a maintenance window is read and written on: the reader's own.
 *
 * The one-off form used to read what was typed as UTC and say so in its
 * labels, the only time field in the product that did. Someone in Amsterdam
 * who typed 20:00 for tonight's deploy got a window from 22:00 to 23:00 local
 * time: the deploy alerted anyway, and an hour nobody was working in had its
 * outages silenced. Everywhere else a time is the reader's (DESIGN.md §2.5),
 * so the form now reads the browser's zone and sends the instant; the API is
 * unchanged and still takes timestamps with an explicit offset.
 *
 * Beside Maintenance.tsx rather than in it for the reason maintenanceScope.ts
 * gives: that file is a lazily loaded component module, and helpers exported
 * from it break fast refresh.
 */

const pad = (n: number) => String(n).padStart(2, "0");

/**
 * "2026-10-20T20:00": an instant as the value of a `datetime-local` field, in
 * the reader's zone. It is the machine shape the field holds, not text for
 * reading, so it lives here and not in format.ts; the clock part still comes
 * from there.
 */
export function localInputValue(ms: number): string {
  if (!Number.isFinite(ms)) return "";
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${formatClock(ms)}`;
}

/** What a typed local date and time stands for. */
export type LocalInstant =
  | { ok: true; iso: string }
  | { ok: false; message: string };

/**
 * The instant a `datetime-local` value names on this browser's clock.
 *
 * Two days a year the clock is not a straight line. When it goes back, a time
 * such as 02:30 happens twice; the Date constructor resolves it to the earlier
 * occurrence (ECMA-262 takes the first of the possible instants), which is
 * what a weekly window does too. When it goes forward, 02:30 never happens;
 * the constructor would quietly move it an hour, so it is refused instead,
 * found by writing the instant back and comparing it with what was typed.
 *
 * A value that is not a date and a time at all is passed on as typed. The
 * field is `required` and the browser's picker writes only this shape, so it
 * does not happen in practice; when it does, the server's refusal, which
 * names the field, is the one wording for it.
 */
export function localInstant(value: string): LocalInstant {
  const parts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value);
  if (parts === null) return { ok: true, iso: value };
  const [year, month, day, hour, minute] = parts.slice(1).map(Number);
  const ms = new Date(year, month - 1, day, hour, minute).getTime();
  const written = localInputValue(ms);
  // A day the calendar does not have (30 February) rolls into the next month.
  if (written.slice(0, 10) !== value.slice(0, 10)) return { ok: false, message: "That date does not exist." };
  if (written !== value) {
    return { ok: false, message: `${value.slice(11)} does not happen on that day in your time zone: the clocks go forward past it. Choose a time after the change.` };
  }
  return { ok: true, iso: new Date(ms).toISOString() };
}

/** Where a window stands at `now`, as the list says it. */
export type WindowState = "active" | "upcoming" | "ended";

export type ListedWindow = { starts_at?: string; ends_at?: string; timezone?: string; active: boolean };

/**
 * Active is the server's word, and wins: it is what suppresses alerts. A
 * one-off window that is not active and whose end is behind `now`, the moment
 * the list was read, has ended; any other inactive window is still to come. A
 * weekly window always has another occurrence coming, so it never ends.
 */
export function windowState(window: ListedWindow, now: number): WindowState {
  if (window.active) return "active";
  if (window.timezone === undefined || window.timezone === "") {
    const end = Date.parse(window.ends_at ?? "");
    if (Number.isFinite(end) && end <= now) return "ended";
  }
  return "upcoming";
}

export const STATE_WORDS: Record<WindowState, string> = {
  active: "Active now",
  upcoming: "Upcoming",
  ended: "Ended",
};

/**
 * "1 Oct 2026, 20:00 → 21:00": a one-off window, the end dated only when it
 * falls on another day than the start.
 */
export function oneOffSpan(startsAt: string, endsAt: string): string {
  return `${formatMomentIso(startsAt)} → ${formatMomentFrom(Date.parse(endsAt), Date.parse(startsAt)) ?? "—"}`;
}

const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

/**
 * "Sunday at 02:00 (Europe/Amsterdam), for 1 h". The time is the zone's
 * wall clock as stored, not converted: it is the schedule's own clock, which
 * the zone in brackets names.
 */
export function weeklySpan(weekdays: readonly number[] | undefined, localTime: string | undefined, timezone: string, minutes: number | undefined): string {
  const days = (weekdays ?? []).map((day) => DAYS[day] ?? `day ${day}`).join(", ");
  return `${days} at ${localTime ?? "—"} (${timezone}), for ${formatDuration((minutes ?? 0) * 60)}`;
}
