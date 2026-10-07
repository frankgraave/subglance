// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AddMonitor } from "./AddMonitor";
import { withType } from "./AddMonitorForm";
import type { AddMonitorValues } from "./AddMonitorForm";
import { ApiError, describePreview } from "./preview";
import type { PreviewResult } from "./preview";

afterEach(cleanup);

const setField = (label: RegExp, value: string) =>
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
const click = (name: RegExp) => fireEvent.click(screen.getByRole("button", { name }));
const chooseDomain = () => setField(/check type/i, "domain");

function result(over: Partial<PreviewResult> = {}): PreviewResult {
  return { ok: true, checked_at: "2026-01-01T12:00:00Z", latency_ms: 140, type: "domain", target: "example.com", ...over };
}

describe("adding a domain monitor", () => {
  it("asks for the warning outside the advanced panel, and drops the HTTP-only fields", () => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn() }} />);
    expect(screen.queryByLabelText(/warn before it expires/i)).toBeNull();
    chooseDomain();
    const warn = screen.getByLabelText(/warn before it expires/i) as HTMLInputElement;
    expect(warn.closest("details")).toBeNull();
    expect(warn.value).toBe("30");
    expect(screen.queryByLabelText(/body must contain/i)).toBeNull();
    expect(screen.queryByLabelText(/^json field$/i)).toBeNull();
    expect(screen.queryByLabelText(/minimum tls version/i)).toBeNull();
    expect(screen.queryByLabelText(/^record type$/i)).toBeNull();
  });

  it("previews and saves the threshold, at a day's interval rather than a minute's", async () => {
    const preview = vi.fn().mockResolvedValue(result());
    const create = vi.fn().mockResolvedValue({ id: "9" });
    render(<AddMonitor api={{ preview, create }} />);
    chooseDomain();
    setField(/what should be watched/i, "example.com");
    setField(/warn before it expires/i, "45");
    click(/test it/i);
    await waitFor(() => expect(preview).toHaveBeenCalled());
    expect(preview.mock.calls[0][0]).toMatchObject({ type: "domain", target: "example.com", domain_warn_days: 45 });

    click(/save monitor/i);
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toMatchObject({ type: "domain", target: "example.com", domain_warn_days: 45, interval_s: 86400 });
    expect(create.mock.calls[0][0]).not.toHaveProperty("keyword");
    expect(create.mock.calls[0][0]).not.toHaveProperty("dns");
  });

  it("keeps 0, which means warn only once it has expired", async () => {
    const create = vi.fn().mockResolvedValue({ id: "9" });
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    chooseDomain();
    setField(/what should be watched/i, "example.com");
    setField(/warn before it expires/i, "0");
    click(/save monitor/i);
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0].domain_warn_days).toBe(0);
  });

  it("sends no threshold once another type is chosen", async () => {
    const create = vi.fn().mockResolvedValue({ id: "9" });
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    chooseDomain();
    setField(/check type/i, "ping");
    setField(/what should be watched/i, "example.com");
    click(/save monitor/i);
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).not.toHaveProperty("domain_warn_days");
    // The day the domain type brought along goes with it.
    expect(create.mock.calls[0][0].interval_s).toBe(60);
  });

  it("places a refusal of the threshold under that field", async () => {
    const create = vi.fn().mockRejectedValue(new ApiError(400, "domain_warn_days must be between 0 and 365", null, "domain_warn_days"));
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    chooseDomain();
    setField(/what should be watched/i, "example.com");
    click(/save monitor/i);
    await waitFor(() =>
      expect(screen.getByLabelText(/warn before it expires/i).getAttribute("aria-invalid")).toBe("true"));
  });

  it("says what a name under the domain checks", () => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn() }} />);
    chooseDomain();
    expect(document.body.textContent).toMatch(/www\.example\.com, checks example\.com/);
  });
});

describe("withType", () => {
  const base = { type: "http", intervalS: 60 } as AddMonitorValues;

  it("lifts an interval under six hours to a day for a domain monitor", () => {
    expect(withType(base, "domain").intervalS).toBe(86400);
    expect(withType({ ...base, intervalS: 21599 }, "domain").intervalS).toBe(86400);
  });

  it("keeps an interval a domain monitor accepts", () => {
    expect(withType({ ...base, intervalS: 21600 }, "domain").intervalS).toBe(21600);
  });

  it("puts the form's default back only when the day was the domain type's", () => {
    expect(withType({ ...base, type: "domain", intervalS: 86400 }, "http").intervalS).toBe(60);
    expect(withType({ ...base, type: "domain", intervalS: 43200 }, "http").intervalS).toBe(43200);
    expect(withType({ ...base, type: "ssl", intervalS: 86400 }, "http").intervalS).toBe(86400);
  });
});

describe("describePreview for a domain", () => {
  it("names the expiry date instead of the registry's response time", () => {
    const text = describePreview(result({ domain_expiry: "2027-08-13T04:00:00Z" }));
    expect(text).toMatch(/^example\.com is registered until /);
    expect(text).toContain("2027");
    expect(text).not.toMatch(/ms/);
  });

  it("says when the date is already inside the warning window", () => {
    const text = describePreview(result({ kind: "domain_expiry", domain_expiry: "2026-01-20T00:00:00Z", error: "expires in 19 days" }));
    expect(text).toMatch(/inside the warning window/);
  });

  it("gives the reason when the date could not be read", () => {
    const text = describePreview(result({ ok: false, kind: "unknown", error: "the .example registry publishes no RDAP service" }));
    expect(text).toBe("The expiry date of example.com could not be read: the .example registry publishes no RDAP service.");
  });

  it("gives the check's own sentence for an expired registration", () => {
    const text = describePreview(result({ ok: false, kind: "domain_expiry", error: "domain registration of example.com expired on 2025-12-01" }));
    expect(text).toBe("domain registration of example.com expired on 2025-12-01");
  });
});
