// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AddMonitor } from "./AddMonitor";
import { ApiError } from "./preview";
import type { PreviewResult } from "./preview";

afterEach(cleanup);

const setField = (label: RegExp, value: string) =>
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
const click = (name: RegExp) => fireEvent.click(screen.getByRole("button", { name }));
const chooseDNS = () => setField(/check type/i, "dns");

function result(over: Partial<PreviewResult> = {}): PreviewResult {
  return { ok: true, checked_at: "2026-01-01T12:00:00Z", latency_ms: 9, type: "dns", target: "example.com", ...over };
}

describe("adding a dns monitor", () => {
  it("asks for the record outside the advanced panel, and drops the HTTP-only fields", () => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn() }} />);
    expect(screen.queryByLabelText(/^record type$/i)).toBeNull();
    chooseDNS();
    const recordType = screen.getByLabelText(/^record type$/i);
    expect(recordType.closest("details")).toBeNull();
    expect((recordType as HTMLSelectElement).value).toBe("A");
    expect(screen.getByLabelText(/^expected values$/i).closest("details")).toBeNull();
    expect(screen.queryByLabelText(/body must contain/i)).toBeNull();
    expect(screen.queryByLabelText(/^json field$/i)).toBeNull();
    expect(screen.queryByLabelText(/minimum tls version/i)).toBeNull();
  });

  it("previews and saves the record, the values one per line and the resolver", async () => {
    const preview = vi.fn().mockResolvedValue(result());
    const create = vi.fn().mockResolvedValue({ id: "9" });
    render(<AddMonitor api={{ preview, create }} />);
    chooseDNS();
    setField(/what should be watched/i, "example.com");
    setField(/^record type$/i, "TXT");
    // A TXT value may hold commas; lines are the separator, and blanks drop.
    setField(/^expected values$/i, "v=spf1 include:a.example,b.example -all\n\n  google-site-verification=x  \n");
    setField(/^resolver$/i, " 1.1.1.1 ");
    click(/test it/i);
    await waitFor(() => expect(preview).toHaveBeenCalled());
    const dns = { record_type: "TXT", expected: ["v=spf1 include:a.example,b.example -all", "google-site-verification=x"], resolver: "1.1.1.1" };
    expect(preview.mock.calls[0][0]).toMatchObject({ type: "dns", target: "example.com", dns });

    click(/save monitor/i);
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toMatchObject({ type: "dns", target: "example.com", dns });
    expect(create.mock.calls[0][0]).not.toHaveProperty("keyword");
  });

  it("leaves the resolver out when none is set, and sends an empty list for any record", async () => {
    const create = vi.fn().mockResolvedValue({ id: "9" });
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    chooseDNS();
    setField(/what should be watched/i, "example.com");
    click(/save monitor/i);
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0].dns).toEqual({ record_type: "A", expected: [] });
  });

  it("does not let a preview survive a change to the expected values", async () => {
    const preview = vi.fn().mockResolvedValue(result());
    const create = vi.fn().mockResolvedValue({ id: "9" });
    render(<AddMonitor api={{ preview, create }} />);
    chooseDNS();
    setField(/what should be watched/i, "example.com");
    setField(/^expected values$/i, "192.0.2.1");
    click(/test it/i);
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    setField(/^expected values$/i, "192.0.2.2");
    click(/test it/i);
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
    expect(preview.mock.calls[1][0].dns.expected).toEqual(["192.0.2.2"]);
  });

  it("sends no dns settings once another type is chosen", async () => {
    const create = vi.fn().mockResolvedValue({ id: "9" });
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    chooseDNS();
    setField(/^expected values$/i, "192.0.2.1");
    setField(/check type/i, "ping");
    setField(/what should be watched/i, "example.com");
    click(/save monitor/i);
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).not.toHaveProperty("dns");
  });

  it("places a refusal of an expected value under that field", async () => {
    const create = vi.fn().mockRejectedValue(new ApiError(400, "\"x\" is not an IPv4 address", null, "dns.expected"));
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    chooseDNS();
    setField(/what should be watched/i, "example.com");
    setField(/^expected values$/i, "x");
    click(/save monitor/i);
    await waitFor(() =>
      expect(screen.getByLabelText(/^expected values$/i).getAttribute("aria-invalid")).toBe("true"));
    expect(screen.getByText(/is not an IPv4 address/).textContent).toContain("x");
  });

  it("changes the help under the values with the record type", () => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn() }} />);
    chooseDNS();
    setField(/^record type$/i, "MX");
    expect((screen.getByLabelText(/^expected values$/i) as HTMLTextAreaElement).placeholder).toBe("10 mail.example.com");
    expect(document.body.textContent).toMatch(/with its preference if that should be checked too/);
    setField(/^record type$/i, "TXT");
    expect(document.body.textContent).toMatch(/other TXT records on the name are allowed/);
  });
});
