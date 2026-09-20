// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AddMonitor } from "./AddMonitor";
afterEach(cleanup);

it("creates with an arbitrary repeat base and presents off as Do not repeat", async () => {
  const create = vi.fn().mockResolvedValue({ id: "1" });
  render(<AddMonitor api={{ create, preview: vi.fn() }} />);
  fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
  fireEvent.change(screen.getByLabelText(/check type/i), { target: { value: "http" } });
  const repeat = screen.getByLabelText("Repeat alert base (seconds)") as HTMLInputElement;
  expect(repeat.value).toBe("900");
  fireEvent.change(repeat, { target: { value: "731" } });
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  await waitFor(() => expect(create).toHaveBeenCalled());
  expect(create.mock.calls[0][0]).toMatchObject({ repeat_after_s: 731 });
});

it.each(["", "1", "59", "86401", "60.5", "nope"])("rejects repeat %s before any request, naming and focusing the field", (value) => {
  const create = vi.fn(); const preview = vi.fn();
  render(<AddMonitor api={{ create, preview }} />);
  fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
  const repeat = screen.getByLabelText("Repeat alert base (seconds)");
  fireEvent.change(repeat, { target: { value } });
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  expect(create).not.toHaveBeenCalled(); expect(preview).not.toHaveBeenCalled();
  expect(screen.getByRole("alert").textContent).toMatch(/repeat.*60.*frequent/i);
  expect(document.activeElement).toBe(repeat);
});

it("keeps keyboard focus on Repeat alerts when typing zero removes the seconds field", () => {
  render(<AddMonitor api={{ create: vi.fn(), preview: vi.fn() }} />);
  const seconds = screen.getByLabelText("Repeat alert base (seconds)"); seconds.focus();
  fireEvent.change(seconds, { target: { value: "0" } });
  expect(screen.queryByLabelText("Repeat alert base (seconds)")).toBeNull();
  expect(document.activeElement).toBe(screen.getByLabelText("Repeat alerts"));
});

it.each(["00", "0.0", "-0", " 0 "])("presents numeric zero %s as Do not repeat before saving", async (value) => {
  const create = vi.fn().mockResolvedValue({ id: "1" });
  render(<AddMonitor api={{ create, preview: vi.fn() }} />);
  fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://example.com" } });
  fireEvent.change(screen.getByLabelText(/check type/i), { target: { value: "http" } });
  fireEvent.change(screen.getByLabelText("Repeat alert base (seconds)"), { target: { value } });
  expect((screen.getByLabelText("Repeat alerts") as HTMLSelectElement).value).toBe("off");
  expect(screen.queryByLabelText("Repeat alert base (seconds)")).toBeNull();
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
  expect(screen.queryByLabelText("Repeat alert base (seconds)")).toBeNull();
  expect(screen.getByRole("option", { name: "Do not repeat" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  await waitFor(() => expect(create).toHaveBeenCalled());
  expect(create.mock.calls[0][0]).toEqual({ name: "Backup", type: "push", push_interval_s: 3600, push_grace_s: 60, repeat_after_s: 0 });
});
