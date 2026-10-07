// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { EditMonitorForm } from "./EditMonitorForm";
import { inventoryFromApi } from "./inventory";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const raw = {
  id: 4, name: "example.com registration", type: "domain", target: "example.com", interval_s: 86400, timeout_s: 10,
  enabled: true, status: "up" as const, created_at: "2026-09-01T00:00:00Z", repeat_after_s: 0,
  // What the detail read sends for every type.
  method: "GET", expected_status: "200-299", keyword: "", keyword_mode: "absent_ok", follow_redirects: true,
  headers: {}, body: "", ssl_warn_days: 14,
  domain_warn_days: 30,
};
const label = "Warn before it expires (days)";
const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
const save = () => fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
const testIt = () => fireEvent.click(screen.getByRole("button", { name: /test it|testing/i }));
const passed = () => new Response(JSON.stringify({
  ok: true, target: "example.com", type: "domain", latency_ms: 140, checked_at: "2026-09-20T12:00:00Z",
  domain_expiry: "2027-08-13T04:00:00Z",
}));

it("shows the stored threshold and none of the HTTP settings", () => {
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={vi.fn()} />);
  expect((screen.getByLabelText(label) as HTMLInputElement).value).toBe("30");
  expect(screen.queryByLabelText("Minimum TLS version")).toBeNull();
  expect(screen.queryByLabelText("HTTP method")).toBeNull();
});

it("offers no threshold when the detail read did not carry it", () => {
  const { domain_warn_days: _days, ...listRead } = raw;
  render(<EditMonitorForm monitor={inventoryFromApi(listRead)} onSave={vi.fn()} />);
  expect(screen.queryByLabelText(label)).toBeNull();
});

it("needs a preview to save a new threshold, and sends no HTTP settings with it", async () => {
  const fetch = vi.fn().mockResolvedValue(passed()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
  change(label, "60");
  save();
  expect(onSave).not.toHaveBeenCalled();
  testIt();
  await screen.findByText(/registered until/);
  const sent = JSON.parse(fetch.mock.calls[0][1].body);
  expect(sent).toMatchObject({ type: "domain", target: "example.com", domain_warn_days: 60 });
  expect(sent).not.toHaveProperty("method");
  expect(sent).not.toHaveProperty("ssl_warn_days");
  save();
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({ domain_warn_days: 60 }));
});

it("refuses an interval under six hours before asking the server", () => {
  const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn();
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
  // A day reads as 24 hours; 1 hour is more often than a registry is asked.
  change("Check every", "1");
  testIt();
  save();
  expect(screen.getByText(/registries limit clients that ask more often/)).toBeTruthy();
  expect(screen.getByLabelText("Check every").getAttribute("aria-invalid")).toBe("true");
  expect(fetch).not.toHaveBeenCalled();
  expect(onSave).not.toHaveBeenCalled();
});

it("refuses a threshold outside 0 to 365 before asking the server", () => {
  const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={vi.fn()} />);
  change(label, "400");
  save();
  expect(screen.getByText(/between 0 and 365 days/)).toBeTruthy();
  expect(fetch).not.toHaveBeenCalled();
});
