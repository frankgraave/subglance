export type ReminderStatus = "scheduled" | "unconfirmed" | "acknowledged" | "resolved" | "paused" | "disabled" | "flapping" | "maintenance" | "maintenance_pending";
export type ReminderInfo = { count: number; remindedAt: string | null; nextAt: string | null; status: ReminderStatus };
const STATUSES: readonly string[] = ["scheduled", "unconfirmed", "acknowledged", "resolved", "paused", "disabled", "flapping", "maintenance", "maintenance_pending"];
const timestamp = (value: unknown): value is string => typeof value === "string" && /^\d{4}-\d\d-\d\dT/.test(value) && Number.isFinite(Date.parse(value));

/** Do not substitute a guessed escalation ladder or zero for an absent read. */
export function reminderFromApi(api: {
  reminder_count?: unknown; reminded_at?: unknown; next_reminder_at?: unknown; reminder_status?: unknown;
}): ReminderInfo | null {
  if (typeof api.reminder_count !== "number" || !Number.isSafeInteger(api.reminder_count) || api.reminder_count < 0 ||
      !(api.reminded_at === null || timestamp(api.reminded_at)) ||
      typeof api.reminder_status !== "string" || !STATUSES.includes(api.reminder_status) ||
      (api.reminder_status === "scheduled" ? !timestamp(api.next_reminder_at) : api.next_reminder_at !== null)) return null;
  return { count: api.reminder_count, remindedAt: api.reminded_at, nextAt: api.next_reminder_at as string | null, status: api.reminder_status as ReminderStatus };
}
