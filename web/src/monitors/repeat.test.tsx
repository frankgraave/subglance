// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AddMonitor } from "./AddMonitor";
import { RepeatAlertField } from "./RepeatAlertField";
afterEach(cleanup);

/** Counts the first repeat in seconds, the API's own unit, for exact values. */
function inSeconds() {
  fireEvent.change(screen.getByLabelText("First repeat after: unit"), { target: { value: "s" } });
}

it("associates repeat mode with its help and rejection even when seconds are hidden", () => {
  const { rerender } = render(<RepeatAlertField value="900" onChange={vi.fn()} />);
  const mode = screen.getByLabelText("Repeat alerts");
  const descriptions = () => (mode.getAttribute("aria-describedby") ?? "").split(" ")
    .map((id) => document.getElementById(id)?.textContent ?? "");
  expect(descriptions().join(" ")).toMatch(/muting the incident stops repeats/i);
  expect(mode.getAttribute("aria-invalid")).toBeNull();
  rerender(<RepeatAlertField value="0" onChange={vi.fn()} error="Repeat setting was refused" />);
  expect(screen.queryByLabelText("First repeat after")).toBeNull();
  expect(descriptions()).toEqual([
    expect.stringMatching(/muting the incident stops repeats/i), "Repeat setting was refused",
  ]);
  expect(mode.getAttribute("aria-invalid")).toBe("true");
});

it("creates with an arbitrary repeat base and presents off as Do not repeat", async () => {
  const create = vi.fn().mockResolvedValue({ id: "1" });
  render(<AddMonitor api={{ create, preview: vi.fn() }} />);
  fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
  fireEvent.change(screen.getByLabelText(/check type/i), { target: { value: "http" } });
  const repeat = screen.getByLabelText("First repeat after") as HTMLInputElement;
  // The API's 900 seconds, shown the way the rest of the product writes it.
  expect(repeat.value).toBe("15");
  expect((screen.getByLabelText("First repeat after: unit") as HTMLSelectElement).value).toBe("min");
  inSeconds();
  fireEvent.change(repeat, { target: { value: "731" } });
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  await waitFor(() => expect(create).toHaveBeenCalled());
  expect(create.mock.calls[0][0]).toMatchObject({ repeat_after_s: 731 });
});

it.each(["", "1", "59", "86401", "60.5", "nope"])("rejects repeat %s before any request, naming and focusing the field", (value) => {
  const create = vi.fn(); const preview = vi.fn();
  render(<AddMonitor api={{ create, preview }} />);
  fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
  const repeat = screen.getByLabelText("First repeat after");
  inSeconds();
  fireEvent.change(repeat, { target: { value } });
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  expect(create).not.toHaveBeenCalled(); expect(preview).not.toHaveBeenCalled();
  expect(screen.getByRole("alert").textContent).toMatch(/1 min.*frequent/i);
  expect(document.activeElement).toBe(repeat);
});

it("keeps keyboard focus on Repeat alerts when typing zero removes the seconds field", () => {
  render(<AddMonitor api={{ create: vi.fn(), preview: vi.fn() }} />);
  const seconds = screen.getByLabelText("First repeat after"); seconds.focus();
  fireEvent.change(seconds, { target: { value: "0" } });
  expect(screen.queryByLabelText("First repeat after")).toBeNull();
  expect(document.activeElement).toBe(screen.getByLabelText("Repeat alerts"));
});

it.each(["00", "0.0", "-0", " 0 "])("presents numeric zero %s as Do not repeat before saving", async (value) => {
  const create = vi.fn().mockResolvedValue({ id: "1" });
  render(<AddMonitor api={{ create, preview: vi.fn() }} />);
  fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
  fireEvent.change(screen.getByLabelText(/check type/i), { target: { value: "http" } });
  fireEvent.change(screen.getByLabelText("First repeat after"), { target: { value } });
  expect((screen.getByLabelText("Repeat alerts") as HTMLSelectElement).value).toBe("off");
  expect(screen.queryByLabelText("First repeat after")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  await waitFor(() => expect(create).toHaveBeenCalled());
  expect(create.mock.calls[0][0]).toMatchObject({ repeat_after_s: 0 });
});

it("creates a push monitor with reminders explicitly disabled", async () => {
  const create = vi.fn().mockResolvedValue({ id: "1" });
  render(<AddMonitor api={{ create, preview: vi.fn() }} />);
  fireEvent.change(screen.getByLabelText(/check type/i), { target: { value: "push" } });
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Backup" } });
  fireEvent.change(screen.getByLabelText("Repeat alerts"), { target: { value: "off" } });
  expect(screen.queryByLabelText("First repeat after")).toBeNull();
  expect(screen.getByRole("option", { name: "Do not repeat" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  await waitFor(() => expect(create).toHaveBeenCalled());
  expect(create.mock.calls[0][0]).toEqual({ name: "Backup", type: "push", push_interval_s: 3600, push_grace_s: 60, repeat_after_s: 0 });
});

it.each([
  ["min", "1.5", 90],
  ["h", "2", 7200],
  ["s", "731", 731],
])("multiplies an amount in %s out to seconds before saving", async (unit, amount, seconds) => {
  const create = vi.fn().mockResolvedValue({ id: "1" });
  render(<AddMonitor api={{ create, preview: vi.fn() }} />);
  fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
  fireEvent.change(screen.getByLabelText(/check type/i), { target: { value: "http" } });
  fireEvent.change(screen.getByLabelText("First repeat after: unit"), { target: { value: unit } });
  fireEvent.change(screen.getByLabelText("First repeat after"), { target: { value: amount } });
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  await waitFor(() => expect(create).toHaveBeenCalled());
  expect(create.mock.calls[0][0]).toMatchObject({ repeat_after_s: seconds });
});

it("keeps the typed number when the unit changes, rather than converting it", () => {
  const onChange = vi.fn();
  render(<RepeatAlertField value="900" onChange={onChange} />);
  fireEvent.change(screen.getByLabelText("First repeat after: unit"), { target: { value: "h" } });
  expect((screen.getByLabelText("First repeat after") as HTMLInputElement).value).toBe("15");
  expect(onChange).toHaveBeenLastCalledWith("54000");
});

it("refuses a fraction of a minute that is not a whole number of seconds instead of rounding it", () => {
  const create = vi.fn();
  render(<AddMonitor api={{ create, preview: vi.fn() }} />);
  fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
  fireEvent.change(screen.getByLabelText("First repeat after"), { target: { value: "1.01" } });
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  expect(create).not.toHaveBeenCalled();
  expect(screen.getByRole("alert").textContent).toMatch(/whole seconds/i);
});
