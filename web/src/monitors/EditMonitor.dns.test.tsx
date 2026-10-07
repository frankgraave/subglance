// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { EditMonitorForm } from "./EditMonitorForm";
import { inventoryFromApi } from "./inventory";
import { ApiError } from "./preview";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const raw = {
  id: 3, name: "Mail", type: "dns", target: "example.com", interval_s: 300, timeout_s: 10,
  enabled: true, status: "up" as const, created_at: "2026-09-01T00:00:00Z", repeat_after_s: 900,
  // What the detail read sends for every type.
  method: "GET", expected_status: "200-299", keyword: "", keyword_mode: "absent_ok", follow_redirects: true,
  headers: {}, body: "", ssl_warn_days: 14,
  dns: { record_type: "MX", expected: ["10 mx1.example.com", "20 mx2.example.com"], resolver: "1.1.1.1" },
};
const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
const save = () => fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
const testIt = () => fireEvent.click(screen.getByRole("button", { name: /test it|testing/i }));
const passed = () => new Response(JSON.stringify({ ok: true, target: "example.com", type: "dns", latency_ms: 4, checked_at: "2026-09-20T12:00:00Z" }));

it("shows the stored record, the values one per line and the resolver", () => {
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={vi.fn()} />);
  expect((screen.getByLabelText("Record type") as HTMLSelectElement).value).toBe("MX");
  expect((screen.getByLabelText("Expected values") as HTMLTextAreaElement).value).toBe("10 mx1.example.com\n20 mx2.example.com");
  expect((screen.getByLabelText("Resolver") as HTMLInputElement).value).toBe("1.1.1.1");
  // A DNS query has no TLS and no HTTP request to configure.
  expect(screen.queryByLabelText("Minimum TLS version")).toBeNull();
  expect(screen.queryByLabelText("HTTP method")).toBeNull();
});

it("offers no dns controls when the detail read did not carry them", () => {
  const { dns: _dns, ...listRead } = raw;
  render(<EditMonitorForm monitor={inventoryFromApi(listRead)} onSave={vi.fn()} />);
  expect(screen.queryByLabelText("Record type")).toBeNull();
});

it("sends the settings whole after a preview that carried them", async () => {
  const fetch = vi.fn().mockResolvedValue(passed()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
  change("Resolver", "");
  save();
  expect(onSave).not.toHaveBeenCalled();
  testIt();
  await screen.findByText(/answered/);
  const sent = JSON.parse(fetch.mock.calls[0][1].body);
  expect(sent).toMatchObject({ type: "dns", target: "example.com", dns: { record_type: "MX", expected: ["10 mx1.example.com", "20 mx2.example.com"] } });
  expect(sent.dns).not.toHaveProperty("resolver");
  expect(sent).not.toHaveProperty("method");
  save();
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({
    dns: { record_type: "MX", expected: ["10 mx1.example.com", "20 mx2.example.com"] },
  }));
});

it("puts a refusal of the settings under the control the server named", async () => {
  const fetch = vi.fn().mockResolvedValue(passed()); vi.stubGlobal("fetch", fetch);
  const onSave = vi.fn().mockRejectedValue(new ApiError(400, "\"x\" is not a mail host", null, "dns.expected"));
  render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
  change("Expected values", "x");
  testIt(); await screen.findByText(/answered/);
  save();
  await waitFor(() => expect(screen.getByLabelText("Expected values").getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(screen.getByLabelText("Expected values"));
});
