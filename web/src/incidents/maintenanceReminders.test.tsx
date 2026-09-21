// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { reminderFromApi } from "./reminders";
import { ReminderSummary } from "./ReminderSummary";

afterEach(cleanup);
const history = { reminder_count: 2, reminded_at: "2026-09-20T11:05:00Z" };
it.each(["maintenance", "maintenance_pending"])("parses %s without inventing a due time or discarding issuance", (status) => {
  const reminder = reminderFromApi({ ...history, reminder_status: status, next_reminder_at: null });
  expect(reminder).toEqual({ count: 2, remindedAt: history.reminded_at, nextAt: null, status });
  render(<ReminderSummary reminder={reminder} stale={true} />);
  expect(screen.getByText("2 reminders issued.")).toBeTruthy();
  expect(screen.getByText(/last reported reminder schedule/i)).toBeTruthy();
  expect(screen.queryByText(/next reminder due/i)).toBeNull();
  expect(screen.queryByText(/not a delivery guarantee/i)).toBeNull();
});
it.each(["maintenance", "maintenance_pending"])("rejects a due timestamp paired with %s rather than trusting contradictory data", (status) => {
  expect(reminderFromApi({ ...history, reminder_status: status, next_reminder_at: "2026-09-20T12:37:17Z" })).toBeNull();
});
