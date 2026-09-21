// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { formatMoment, incidentFromApi } from "./detail";
import { MonitorDetail } from "./MonitorDetail";
import { fromApi } from "./types";

afterEach(cleanup);
const api = { id: 7, monitor_id: 1, started_at: "2026-09-20T11:00:00Z", confirmed_at: "2026-09-20T11:01:00Z", confirmed: true, resolved: false, acked: false, duration_s: 50, reminder_count: 2, reminded_at: "2026-09-20T11:05:00Z", next_reminder_at: "2026-09-20T12:37:17Z", reminder_status: "scheduled" };
const monitor = fromApi({ id: 1, name: "api", type: "http", target: "https://example.com", status: "down", enabled: true, interval_s: 60, timeout_s: 10, created_at: api.started_at });
function view(over: Record<string, unknown> = {}, stale = false) {
  return render(<MonitorDetail monitor={monitor} windows={[]} incidents={[incidentFromApi({ ...api, ...over })]} now={Date.parse("2026-09-20T13:00:00Z")} beatWidth={390} stale={stale} />);
}
it("shows the server's precise due time even if already due, and counts issued not delivered reminders", () => {
  view();
  expect(screen.getByText(/2 reminders issued/)).toBeTruthy();
  const due = screen.getByText(/next reminder due/i).querySelector("time");
  expect(due?.getAttribute("datetime")).toBe(api.next_reminder_at);
  expect(screen.getByText(/not a delivery guarantee/i)).toBeTruthy();
  expect(document.body.textContent).not.toMatch(/delivered/);
});
it.each([api.next_reminder_at, "2026-12-31T23:07:43-05:00"])("formats reminder due time with the shared incident timestamp semantics: %s", (nextAt) => {
  view({ next_reminder_at: nextAt });
  const due = screen.getByText(/next reminder due/i).querySelector("time");
  expect(due?.getAttribute("datetime")).toBe(nextAt);
  expect(due?.textContent).toBe(formatMoment(Date.parse(nextAt)));
  expect(screen.getByText(/not a delivery guarantee/i)).toBeTruthy();
});
it("preserves meaningful zero without inventing an already-issued reminder", () => {
  view({ reminder_count: 0, reminded_at: null });
  expect(screen.getByText(/0 reminders issued/)).toBeTruthy();
});
it.each([
  ["resolved", "resolved"], ["acknowledged", "acknowledged"], ["unconfirmed", "confirmation"],
  ["paused", "monitor is paused"], ["disabled", "Do not repeat"], ["flapping", "flapping"],
])("uses authoritative %s suppression instead of computing a due time", (status, words) => {
  view({ reminder_status: status, next_reminder_at: null });
  expect(screen.getByText(new RegExp(`Reminders.*${words}`, "i"))).toBeTruthy();
  expect(screen.queryByText(/next reminder due/i)).toBeNull();
});
it.each([
  { reminder_count: undefined }, { reminder_count: -1 }, { reminder_count: "0" }, { reminder_count: 0.5 },
  { reminded_at: undefined }, { reminded_at: "yesterday" }, { next_reminder_at: undefined }, { next_reminder_at: "invalid" },
  { reminder_status: "other" }, { reminder_status: undefined }, { reminder_status: "paused" },
])("reports malformed or absent metadata as unavailable: %j", (over) => {
  view(over);
  expect(screen.getByText(/reminder information unavailable/i)).toBeTruthy();
  expect(screen.queryByText(/reminders issued/)).toBeNull();
  expect(screen.queryByText(/next reminder due/i)).toBeNull();
});
it("qualifies stale metadata rather than promising a current schedule", () => {
  view({}, true);
  expect(screen.getByText(/last reported reminder schedule/i)).toBeTruthy();
});
