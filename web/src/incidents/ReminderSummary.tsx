import type { ReminderInfo, ReminderStatus } from "./reminders";
import { formatMoment } from "../monitors/detail";

const REASON: Record<Exclude<ReminderStatus, "scheduled">, string> = {
  unconfirmed: "Reminders wait for incident confirmation.",
  acknowledged: "Reminders stopped — incident acknowledged.",
  resolved: "Reminders stopped — incident resolved.",
  paused: "Reminders suspended — monitor is paused.",
  disabled: "Reminders disabled — Do not repeat is selected.",
  flapping: "Reminders suppressed — the server reports flapping.",
  maintenance: "Reminders suspended — a maintenance window is active.",
  maintenance_pending: "Reminders suspended — the initial alert is pending after maintenance.",
};
export function ReminderSummary({ reminder, stale }: { reminder?: ReminderInfo | null; stale: boolean }) {
  return <div className="inc-reminders">
    {reminder == null ? <p>Reminder information unavailable.</p> : <>
      {stale && <p>Last reported reminder schedule — connection is stale.</p>}
      <p>{reminder.count} {reminder.count === 1 ? "reminder" : "reminders"} issued.</p>
      {reminder.status === "scheduled" && reminder.nextAt !== null
        ? <><p>Next reminder due: <time dateTime={reminder.nextAt}>{formatMoment(Date.parse(reminder.nextAt))}</time>.</p><p>This is an eligibility time, not a delivery guarantee.</p></>
        : <p>{REASON[reminder.status as Exclude<ReminderStatus, "scheduled">]}</p>}
    </>}
  </div>;
}
