// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { EditMonitorForm } from "./EditMonitorForm";
import { inventoryFromApi } from "./inventory";
import { ApiError } from "./preview";
import { confirmLeave } from "../shell/leaveGuard";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
const raw = { id: 1, name: "Auth", type: "http", target: "https://old.example", interval_s: 60, timeout_s: 10, enabled: true, status: "up" as const, created_at: "2026-09-01T00:00:00Z", repeat_after_s: 731, method: "POST", expected_status: "201-204", keyword: "healthy", keyword_mode: "must_not_contain", follow_redirects: false, headers: { Authorization: "Bearer test-only", "X-Empty": "" }, body: "payload", ssl_warn_days: 30, min_tls_version: "1.2" };
const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
const save = () => fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
const testTarget = () => fireEvent.click(screen.getByRole("button", { name: /test it|testing/i }));
const result = (target = "https://new.example") => new Response(JSON.stringify({ ok: false, target, type: "http", latency_ms: 8, checked_at: "2026-09-20T12:00:00Z", error: "connection refused" }));

it("previews the changed target with every stored check setting, then permits saving a known Down target", async () => {
  const fetch = vi.fn().mockResolvedValue(result()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
  change("Target", "https://new.example"); save();
  expect(onSave).not.toHaveBeenCalled();
  expect(screen.getByRole("alert").textContent).toMatch(/test it/i);
  testTarget();
  await screen.findByText(/connection refused/);
  expect(fetch.mock.calls[0][0]).toBe("/api/v1/monitors/preview");
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ type: "http", target: "https://new.example", timeout_s: 10, method: "POST", expected_status: "201-204", keyword: "healthy", keyword_mode: "must_not_contain", follow_redirects: false, headers: raw.headers, body: "payload", ssl_warn_days: 30, min_tls_version: "1.2" });
  expect(onSave).not.toHaveBeenCalled();
  save(); await waitFor(() => expect(onSave).toHaveBeenCalledWith({ target: "https://new.example" }));
});

it.each(["1.3", ""])("previews the edited TLS floor %j with a new target and preserves explicit PATCH clearing", async (floor) => {
  const fetch = vi.fn().mockResolvedValue(result()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
  change("Target", "https://new.example");
  change("Minimum TLS version", floor);
  change("Repeat alert base (seconds)", "173");
  save(); expect(onSave).not.toHaveBeenCalled();
  testTarget(); await screen.findByText(/connection refused/);
  const request = JSON.parse(fetch.mock.calls[0][1].body);
  const settings = raw;
  expect(request).toEqual({
    type: settings.type, target: "https://new.example", timeout_s: settings.timeout_s,
    method: settings.method, expected_status: settings.expected_status, keyword: settings.keyword,
    keyword_mode: settings.keyword_mode, follow_redirects: settings.follow_redirects,
    headers: settings.headers, body: settings.body, ssl_warn_days: settings.ssl_warn_days,
    ...(floor ? { min_tls_version: floor } : {}),
  });
  save();
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({
    target: "https://new.example", min_tls_version: floor, repeat_after_s: 173,
  }));
});

it.each([
  ["Keyword", "changed", "Timeout (seconds)", "20"],
  ["Minimum TLS version", "1.3", "Minimum TLS version", ""],
])("invalidates completed and in-flight previews when %s changes", async (first, firstValue, second, secondValue) => {
  let resolve!: (value: Response) => void;
  const fetch = vi.fn().mockImplementationOnce(() => new Promise<Response>((r) => { resolve = r; })).mockResolvedValue(result()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn();
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
  change("Target", "https://new.example"); testTarget();
  change(first, firstValue);
  expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
  await act(async () => resolve(result()));
  expect(screen.queryByText(/connection refused/)).toBeNull(); save(); expect(onSave).not.toHaveBeenCalled();
  testTarget(); await screen.findByText(/connection refused/);
  change(second, secondValue);
  expect(screen.queryByText(/connection refused/)).toBeNull(); save(); expect(onSave).not.toHaveBeenCalled();
});

it("round-trips explicit empty and false settings without resetting untouched fields", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(result(raw.target)));
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, follow_redirects: true })} onSave={onSave} />);
  change("Keyword", ""); change("Request body", ""); change("Headers (JSON)", "{}");
  fireEvent.click(screen.getByLabelText("Follow redirects"));
  testTarget(); await screen.findByText(/connection refused/); save();
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({ keyword: "", body: "", headers: {}, follow_redirects: false }));
});

it("edits push reporting windows without a target, poll interval, timeout or preview", async () => {
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, type: "push", target: "", push_interval_s: 3600, push_grace_s: 60 })} onSave={onSave} />);
  expect(screen.queryByLabelText("Interval (seconds)")).toBeNull(); expect(screen.queryByLabelText("Target")).toBeNull();
  expect(screen.queryByRole("button", { name: /test it/i })).toBeNull();
  change("Should report every (seconds)", "7200"); change("Allow it to be late by (seconds)", "0"); save();
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({ push_interval_s: 7200, push_grace_s: 0 }));
});

it("keeps a conflict draft and requires deliberate reload instead of retrying", async () => {
  const onSave = vi.fn().mockRejectedValue(new ApiError(412, "stale version")); const onReload = vi.fn();
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} onReload={onReload} />);
  change("Name", "My draft"); save();
  await screen.findByText(/changed.*someone else/i);
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("My draft");
  save(); expect(onSave).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Reload latest settings" })); expect(onReload).toHaveBeenCalledTimes(1);
});

it("places a checkbox API rejection beside its actual control", async () => {
  const onSave = vi.fn().mockRejectedValue(new ApiError(400, "Redirects are not allowed", null, "follow_redirects"));
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
  change("Name", "Rename"); save();
  expect((await screen.findByRole("alert")).textContent).toBe("Redirects are not allowed");
  const checkbox = screen.getByLabelText("Follow redirects");
  expect(checkbox.getAttribute("aria-invalid")).toBe("true");
  expect(document.activeElement).toBe(checkbox);
});

it.each(["save", "preview"])("opens and focuses a TLS floor rejection from %s with help and error associations", async (source) => {
  const message = "This TLS floor is not allowed";
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(
    JSON.stringify({ error: message, field: "min_tls_version" }), { status: 400 },
  )));
  const onSave = vi.fn().mockRejectedValue(new ApiError(400, message, null, "min_tls_version"));
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
  const select = screen.getByLabelText("Minimum TLS version");
  const panel = select.closest("details")!;
  expect(panel.open).toBe(false);
  if (source === "save") { change("Name", "Rename"); save(); }
  else testTarget();
  const error = await screen.findByRole("alert");
  expect(error.textContent).toBe(message);
  expect(select.getAttribute("aria-invalid")).toBe("true");
  expect(panel.open).toBe(true);
  expect(document.activeElement).toBe(select);
  expect((select.getAttribute("aria-describedby") ?? "").split(" ")
    .map((id) => document.getElementById(id)?.textContent)).toEqual([
      expect.stringMatching(/floor SubGlance dials with/i), message,
    ]);
  expect(error.querySelector('svg[aria-hidden="true"]')).not.toBeNull();
});

it("keeps conflict explanation visible while the retained draft is edited", async () => {
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={vi.fn().mockRejectedValue(new ApiError(412, "stale"))} onReload={vi.fn()} />);
  change("Name", "Draft"); save(); await screen.findByText(/nothing was overwritten/i);
  change("Name", "Keep this draft");
  expect(screen.getByRole("alert").textContent).toMatch(/nothing was overwritten/i);
});

it("guards cancel and navigation without storing the draft", () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false); const onCancel = vi.fn();
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={vi.fn()} onCancel={onCancel} />);
  change("Name", "Private draft");
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(confirm).toHaveBeenCalledTimes(1); expect(onCancel).not.toHaveBeenCalled();
  expect(confirmLeave()).toBe(false);
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Private draft");
});
