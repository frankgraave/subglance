// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AddMonitor } from "./AddMonitor";
import { EditMonitorForm } from "./EditMonitorForm";
import { inventoryFromApi } from "./inventory";
import { readsSetting, settingsReadBy } from "./typeSettings";
import type { PreviewResult } from "./preview";

/*
 * A setting offered on a type whose check never reads it is a control that
 * does nothing, and the API refuses the value: a keyword on a TCP monitor
 * passes whatever the service answers. Both forms offer, send and preview a
 * setting only where it is read.
 */

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

/** The labels the edit form gives each type-bound setting. */
const HTTP_LABELS = ["HTTP method", "Expected status", "Keyword", "Keyword rule", "Follow redirects", "Headers (JSON)", "Request body"];
const TLS_LABELS = ["Certificate warning", "Minimum TLS version"];

/** A monitor as the detail read sends it: every check setting, for every type. */
function detail(type: string, target: string, over: Record<string, unknown> = {}) {
  return inventoryFromApi({
    id: 7, name: "svc", type, target, interval_s: 60, timeout_s: 10, enabled: true, status: "up",
    created_at: "2026-09-01T10:00:00Z", tags: {}, repeat_after_s: 900,
    method: "GET", expected_status: "200-299", keyword: "", keyword_mode: "absent_ok",
    follow_redirects: true, headers: {}, body: "", ssl_warn_days: 14, min_tls_version: "1.2",
    ...over,
  } as Parameters<typeof inventoryFromApi>[0]);
}

const labelled = (label: string) => screen.queryByLabelText(label) !== null;

describe("readsSetting", () => {
  it("binds the request settings to http and the TLS pair to http and ssl", () => {
    for (const type of ["tcp", "ping", "ssl", "dns", "domain"]) expect(readsSetting(type, "keyword")).toBe(false);
    for (const type of ["tcp", "ping", "dns", "domain"]) expect(readsSetting(type, "min_tls_version")).toBe(false);
    expect(readsSetting("ssl", "ssl_warn_days")).toBe(true);
    expect(readsSetting("http", "headers")).toBe(true);
    // A type still to be worked out from the address may be http.
    expect(readsSetting("", "keyword")).toBe(true);
    expect(readsSetting("tcp", "name")).toBe(true);
  });

  it("keeps only the settings a type reads", () => {
    expect(settingsReadBy("ssl", { method: "GET", ssl_warn_days: 14, min_tls_version: "1.2" }))
      .toEqual({ ssl_warn_days: 14, min_tls_version: "1.2" });
    expect(settingsReadBy("tcp", { method: "GET", ssl_warn_days: 14 })).toEqual({});
  });
});

describe("EditMonitorForm", () => {
  it.each([
    ["tcp", "db.example.com:5432", [], [...HTTP_LABELS, ...TLS_LABELS]],
    ["ping", "example.com", [], [...HTTP_LABELS, ...TLS_LABELS]],
    ["ssl", "example.com:443", TLS_LABELS, HTTP_LABELS],
    ["http", "https://example.com", [...HTTP_LABELS, ...TLS_LABELS], []],
  ] as const)("offers a %s monitor only the settings its check reads", (type, target, shown, hidden) => {
    render(<EditMonitorForm monitor={detail(type, target)} onSave={vi.fn()} />);
    for (const label of shown) expect(labelled(label), `${type} shows ${label}`).toBe(true);
    for (const label of hidden) expect(labelled(label), `${type} hides ${label}`).toBe(false);
  });

  it("tests and saves a tcp monitor without the settings it does not read", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      ok: true, target: "db2.example.com:5432", type: "tcp", latency_ms: 4, checked_at: "2026-09-20T12:00:00Z",
    })));
    vi.stubGlobal("fetch", fetch);
    const onSave = vi.fn().mockResolvedValue(undefined);
    // Stored before the API refused them: still sent nowhere by this form.
    render(<EditMonitorForm monitor={detail("tcp", "db.example.com:5432", { keyword: "ok", keyword_mode: "must_contain", min_tls_version: "1.3" })} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Target"), { target: { value: "db2.example.com:5432" } });
    fireEvent.click(screen.getByRole("button", { name: /test it/i }));
    await screen.findByText(/answered/i);
    const sent = JSON.parse(fetch.mock.calls[0][1].body);
    expect(sent).toEqual({ type: "tcp", target: "db2.example.com:5432", timeout_s: 10 });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ target: "db2.example.com:5432" }));
  });

  it("tests an ssl monitor with its TLS settings and without the request ones", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      ok: true, target: "example.com:443", type: "ssl", latency_ms: 4, checked_at: "2026-09-20T12:00:00Z",
    })));
    vi.stubGlobal("fetch", fetch);
    render(<EditMonitorForm monitor={detail("ssl", "example.com:443", { ssl_warn_days: 30, min_tls_version: "1.3" })} onSave={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /test it/i }));
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    const sent = JSON.parse(fetch.mock.calls[0][1].body);
    expect(sent).toMatchObject({ type: "ssl", ssl_warn_days: 30, min_tls_version: "1.3" });
    for (const key of ["method", "expected_status", "keyword", "keyword_mode", "follow_redirects", "headers", "body"]) {
      expect(sent).not.toHaveProperty(key);
    }
  });
});

function result(over: Partial<PreviewResult> = {}): PreviewResult {
  return { ok: true, checked_at: "2026-01-01T12:00:00Z", latency_ms: 12, type: "tcp", target: "db.example.com:5432", ...over };
}
const setField = (label: RegExp, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

describe("AddMonitor", () => {
  it.each([
    ["tcp", false, false],
    ["ping", false, false],
    ["ssl", false, true],
    ["http", true, true],
    ["", true, true],
  ] as const)("offers type %j the keyword %s and the TLS floor %s", (type, keyword, floor) => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn() }} />);
    setField(/check type/i, type);
    expect(screen.queryByLabelText(/body must contain/i) !== null).toBe(keyword);
    expect(screen.queryByLabelText(/minimum tls version/i) !== null).toBe(floor);
  });

  it.each([
    ["tcp", "db.example.com:5432", []],
    ["ssl", "example.com:443", ["min_tls_version"]],
  ] as const)("sends a %s monitor only the settings typed for a type that reads them", async (type, target, kept) => {
    const preview = vi.fn().mockResolvedValue(result({ type, target }));
    const create = vi.fn().mockResolvedValue({ id: "9" });
    render(<AddMonitor api={{ preview, create }} />);
    setField(/check type/i, "http");
    setField(/minimum tls version/i, "1.3");
    setField(/body must contain/i, "Welcome");
    setField(/check type/i, type);
    setField(/what should be watched/i, target);
    fireEvent.click(screen.getByRole("button", { name: /test it/i }));
    await waitFor(() => expect(preview).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: /save monitor/i }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    for (const sent of [preview.mock.calls[0][0], create.mock.calls[0][0]]) {
      expect(sent).not.toHaveProperty("keyword");
      expect(sent).not.toHaveProperty("keyword_mode");
      expect("min_tls_version" in sent).toBe(kept.includes("min_tls_version" as never));
    }
  });
});
