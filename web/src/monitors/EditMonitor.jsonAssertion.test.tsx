// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { EditMonitorForm } from "./EditMonitorForm";
import { inventoryFromApi } from "./inventory";
import { ApiError } from "./preview";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const raw = {
  id: 1, name: "API", type: "http", target: "https://api.example/health", interval_s: 60, timeout_s: 10,
  enabled: true, status: "up" as const, created_at: "2026-09-01T00:00:00Z", repeat_after_s: 900,
  method: "GET", expected_status: "200-299", keyword: "", keyword_mode: "absent_ok", follow_redirects: true,
  headers: {}, body: "", ssl_warn_days: 14,
};
const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
const save = () => fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
const testIt = () => fireEvent.click(screen.getByRole("button", { name: /test it|testing/i }));
const passed = () => new Response(JSON.stringify({ ok: true, target: raw.target, type: "http", latency_ms: 8, checked_at: "2026-09-20T12:00:00Z", status_code: 200 }));

it("shows the stored assertion with a plain word for a string and quotes for a numeric string", () => {
  const { unmount } = render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, json_assertion: { path: "checks.db.status", operator: "equals", expected: "up" } })} onSave={vi.fn()} />);
  expect((screen.getByLabelText("JSON field") as HTMLInputElement).value).toBe("checks.db.status");
  expect((screen.getByLabelText("Must") as HTMLSelectElement).value).toBe("equals");
  expect((screen.getByLabelText("Value") as HTMLInputElement).value).toBe("up");
  unmount();
  render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, json_assertion: { path: "n", operator: "equals", expected: "1" } })} onSave={vi.fn()} />);
  expect((screen.getByLabelText("Value") as HTMLInputElement).value).toBe('"1"');
});

it("offers no assertion controls when the detail read did not say what is stored", () => {
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={vi.fn()} />);
  expect(screen.queryByLabelText("JSON field")).toBeNull();
});

it("sends a changed assertion whole, typed, and only after a preview that carried it", async () => {
  const fetch = vi.fn().mockResolvedValue(passed()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, json_assertion: null })} onSave={onSave} />);
  change("JSON field", "replication.lag");
  change("Must", "less_than");
  change("Value", "30");
  save();
  expect(onSave).not.toHaveBeenCalled();
  testIt();
  await screen.findByText(/answered/);
  expect(JSON.parse(fetch.mock.calls[0][1].body).json_assertion).toEqual({ path: "replication.lag", operator: "less_than", expected: 30 });
  save();
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({
    json_assertion: { path: "replication.lag", operator: "less_than", expected: 30 },
  }));
});

it("removes the assertion with null when the field is cleared, and previews without one", async () => {
  const fetch = vi.fn().mockResolvedValue(passed()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, json_assertion: { path: "ok", operator: "equals", expected: true } })} onSave={onSave} />);
  change("JSON field", "");
  testIt();
  await screen.findByText(/answered/);
  expect(JSON.parse(fetch.mock.calls[0][1].body)).not.toHaveProperty("json_assertion");
  save();
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({ json_assertion: null }));
});

it("hides the value for exists and sends none", async () => {
  const fetch = vi.fn().mockResolvedValue(passed()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, json_assertion: { path: "id", operator: "equals", expected: 1 } })} onSave={onSave} />);
  change("Must", "exists");
  expect(screen.queryByLabelText("Value")).toBeNull();
  testIt(); await screen.findByText(/answered/);
  save();
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({ json_assertion: { path: "id", operator: "exists" } }));
});

it("puts a rejection of the assertion under the control the server named", async () => {
  const fetch = vi.fn().mockResolvedValue(passed()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockRejectedValue(new ApiError(400, "less_than compares numbers", null, "json_assertion.expected"));
  render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, json_assertion: null })} onSave={onSave} />);
  change("JSON field", "lag"); change("Must", "less_than"); change("Value", "soon");
  testIt(); await screen.findByText(/answered/);
  save();
  await waitFor(() => expect(screen.getByLabelText("Value").getAttribute("aria-invalid")).toBe("true"));
  expect(screen.getByRole("alert").textContent).toMatch(/compares numbers/);
});

it("refuses a number the browser would round instead of saving the rounded one", async () => {
  const fetch = vi.fn().mockResolvedValue(passed()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, json_assertion: null })} onSave={onSave} />);
  change("JSON field", "id"); change("Value", "9007199254740993");
  testIt();
  await waitFor(() => expect(screen.getByLabelText("Value").getAttribute("aria-invalid")).toBe("true"));
  expect(screen.getByRole("alert").textContent).toMatch(/more digits than a browser can hold/);
  expect(fetch).not.toHaveBeenCalled();
  save();
  expect(onSave).not.toHaveBeenCalled();
});
