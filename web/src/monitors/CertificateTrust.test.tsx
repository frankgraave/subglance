// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { AddMonitor } from "./AddMonitor";
import { EditMonitorForm } from "./EditMonitorForm";
import { CertificateTrust } from "./CertificateTrust";
import { inventoryFromApi } from "./inventory";
import { carriesPin, fingerprintPreview } from "./preview";
import type { OfferedCertificate, PreviewResult } from "./preview";

/*
 * Trusting one certificate on one monitor from the forms: a failed Test it
 * shows the certificate the server presented and offers to trust it, the
 * retest carries the pin, the save stores it, and the edit form shows a
 * stored pin with a way to stop trusting it.
 */

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const FP = "3F:9A:0B:52:1C:7E:44:D0:9B:21:6A:88:F3:05:CE:71:2D:B4:60:19:7A:E2:55:0C:8D:3B:F1:46:A9:EE:07:C2";
const OTHER = "AA:BB:0B:52:1C:7E:44:D0:9B:21:6A:88:F3:05:CE:71:2D:B4:60:19:7A:E2:55:0C:8D:3B:F1:46:A9:EE:07:C2";
const cert = (sha256 = FP): OfferedCertificate => ({
  subject: "CN=pve.home.arpa", issuer: "CN=pve.home.arpa",
  not_before: "2026-01-01T00:00:00Z", not_after: "2027-03-01T00:00:00Z", sha256,
});
const failed = (over: Partial<PreviewResult> = {}): PreviewResult => ({
  ok: false, checked_at: "2026-10-13T12:00:00Z", latency_ms: 9, kind: "tls",
  error: "tls: certificate signed by unknown authority", type: "http",
  target: "https://pve.home.arpa:8006", certificate: cert(), ...over,
});
const passed = (): PreviewResult => ({
  ok: true, checked_at: "2026-10-13T12:00:01Z", latency_ms: 9, status_code: 200,
  type: "http", target: "https://pve.home.arpa:8006",
});
const click = (name: RegExp | string) => fireEvent.click(screen.getByRole("button", { name }));
const offer = () => screen.queryByRole("group", { name: /certificate the test was offered/i });

describe("carriesPin", () => {
  it.each([
    ["ssl", "pve.home.arpa:8006", true],
    ["http", "https://pve.home.arpa:8006", true],
    ["http", "HTTPS://pve.home.arpa", true],
    ["http", "http://pve.home.arpa", false],
    ["tcp", "pve.home.arpa:8006", false],
    ["ping", "pve.home.arpa", false],
    // Inferred by the server: a bare host is https, a host and port is TCP.
    ["", "pve.home.arpa", true],
    ["", "https://pve.home.arpa:8006", true],
    ["", "http://pve.home.arpa", false],
    ["", "pve.home.arpa:8006", false],
    ["", "", false],
  ])("%j %j -> %j", (type, target, want) => {
    expect(carriesPin(type, target)).toBe(want);
  });
});

it("puts the pin in a preview's fingerprint, so trusting a certificate invalidates the old result", () => {
  const request = { type: "http", target: "https://pve.home.arpa:8006" };
  expect(fingerprintPreview({ ...request, trusted_cert_sha256: FP })).not.toBe(fingerprintPreview(request));
});

it("reads the pin and when it was set from the monitor", () => {
  const m = inventoryFromApi({ id: 1, name: "pve", type: "ssl", target: "pve.home.arpa:8006", interval_s: 60, timeout_s: 10, enabled: true, status: "up", created_at: "2026-09-01T00:00:00Z", trusted_cert_sha256: FP, trusted_cert_at: "2026-10-11T08:00:00Z" });
  expect(m.trustedCertSHA256).toBe(FP);
  expect(m.trustedCertAt).toBe("2026-10-11T08:00:00Z");
});

describe("CertificateTrust", () => {
  const noop = () => {};
  it("does not offer the certificate that is already trusted", () => {
    render(<CertificateTrust trusted={FP} offered={cert(FP)} onTrust={noop} onStop={noop} stopId="stop" />);
    expect(offer()).toBeNull();
    expect(screen.getByRole("button", { name: "Stop trusting" })).toBeTruthy();
  });

  it("draws nothing with no pin and no offer", () => {
    const { container } = render(<CertificateTrust trusted="" onTrust={noop} onStop={noop} stopId="stop" />);
    expect(container.textContent).toBe("");
  });

  it("names the issuer when the certificate is not self-signed", () => {
    render(<CertificateTrust trusted="" offered={{ ...cert(), issuer: "CN=Home Lab Root CA" }} onTrust={noop} onStop={noop} stopId="stop" />);
    expect(screen.getByText("CN=Home Lab Root CA")).toBeTruthy();
    expect(screen.queryByText(/self-signed/)).toBeNull();
  });
});

describe("AddMonitor", () => {
  it("shows the certificate a failed test was offered, retests with it trusted, and saves the pin", async () => {
    const preview = vi.fn().mockResolvedValueOnce(failed()).mockResolvedValueOnce(passed());
    const create = vi.fn().mockResolvedValue({ id: "4" });
    render(<AddMonitor api={{ preview, create }} />);
    fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://pve.home.arpa:8006" } });
    click(/test it/i);

    const shown = await screen.findByRole("group", { name: /certificate the test was offered/i });
    expect(screen.getByRole("status").textContent).toMatch(/shown below, to compare and trust/);
    const facts = within(shown);
    expect(facts.getByText("CN=pve.home.arpa")).toBeTruthy();
    expect(facts.getByText("Itself (self-signed)")).toBeTruthy();
    expect(facts.getByText("1 Mar 2027")).toBeTruthy();
    expect(facts.getByText(FP)).toBeTruthy();
    expect(shown.textContent).toMatch(/compare this fingerprint/i);
    // Nothing is trusted until the button is pressed.
    expect(preview.mock.calls[0][0].trusted_cert_sha256).toBeUndefined();

    click("Trust this certificate");
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
    expect(preview.mock.calls[1][0]).toEqual(expect.objectContaining({ target: "https://pve.home.arpa:8006", trusted_cert_sha256: FP }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toMatch(/answered/));
    expect(offer()).toBeNull();
    expect(screen.getByText(FP)).toBeTruthy();

    click(/save monitor/i);
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toEqual(expect.objectContaining({ type: "http", target: "https://pve.home.arpa:8006", trusted_cert_sha256: FP }));
  });

  it("stops offering a certificate once the address it came from is edited", async () => {
    const preview = vi.fn().mockResolvedValue(failed());
    render(<AddMonitor api={{ preview, create: vi.fn() }} />);
    const target = screen.getByLabelText(/what should be watched/i);
    fireEvent.change(target, { target: { value: "https://pve.home.arpa:8006" } });
    click(/test it/i);
    await screen.findByRole("group", { name: /certificate the test was offered/i });
    fireEvent.change(target, { target: { value: "https://nas.home.arpa" } });
    expect(offer()).toBeNull();
  });

  it("offers nothing for a failure a pin would not fix", async () => {
    const preview = vi.fn().mockResolvedValue(failed({ certificate: undefined, error: "x509: certificate is valid for a.example, not b.example" }));
    render(<AddMonitor api={{ preview, create: vi.fn() }} />);
    fireEvent.change(screen.getByLabelText(/what should be watched/i), { target: { value: "https://b.example" } });
    click(/test it/i);
    await waitFor(() => expect(screen.getByRole("status").textContent).toMatch(/did not answer/));
    expect(offer()).toBeNull();
    expect(screen.queryByRole("button", { name: "Trust this certificate" })).toBeNull();
  });

  it("does not send a pin left behind when the address stops being https", async () => {
    const preview = vi.fn().mockResolvedValueOnce(failed()).mockResolvedValueOnce(passed())
      .mockResolvedValue({ ...passed(), target: "http://pve.home.arpa:8006" });
    const create = vi.fn().mockResolvedValue({ id: "5" });
    render(<AddMonitor api={{ preview, create }} />);
    const target = screen.getByLabelText(/what should be watched/i);
    fireEvent.change(target, { target: { value: "https://pve.home.arpa:8006" } });
    click(/test it/i);
    await screen.findByRole("group", { name: /certificate the test was offered/i });
    click("Trust this certificate");
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));

    fireEvent.change(target, { target: { value: "http://pve.home.arpa:8006" } });
    expect(screen.queryByText(FP)).toBeNull();
    click(/test it/i);
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(3));
    expect(preview.mock.calls[2][0].trusted_cert_sha256).toBeUndefined();
    await waitFor(() => expect(screen.getByRole("status").textContent).toMatch(/answered/));
    click(/save monitor/i);
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0].trusted_cert_sha256).toBeUndefined();
  });
});

describe("EditMonitorForm", () => {
  const raw = { id: 1, name: "pve", type: "http", target: "https://pve.home.arpa:8006", interval_s: 60, timeout_s: 10, enabled: true, status: "down" as const, created_at: "2026-09-01T00:00:00Z", method: "GET", expected_status: "200-299", ssl_warn_days: 14 };
  const respond = (body: PreviewResult) => new Response(JSON.stringify(body));
  const sent = (fetch: ReturnType<typeof vi.fn>, call: number) => JSON.parse(fetch.mock.calls[call][1].body);

  it("shows a stored pin with the day it was trusted, and stops trusting it through a test and a save", async () => {
    const fetch = vi.fn().mockResolvedValue(respond(failed())); vi.stubGlobal("fetch", fetch);
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, trusted_cert_sha256: FP, trusted_cert_at: "2026-10-11T08:00:00Z" })} onSave={onSave} />);
    expect(screen.getByText(FP)).toBeTruthy();
    expect(screen.getByText(/Trusted on 11 Oct 2026/)).toBeTruthy();
    // The stored certificate is not offered back to the form that trusts it.
    expect(offer()).toBeNull();

    click("Stop trusting");
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    expect(sent(fetch, 0).trusted_cert_sha256).toBeUndefined();
    expect(screen.queryByRole("button", { name: "Stop trusting" })).toBeNull();
    // The same certificate fails without the pin and is offered again.
    await screen.findByRole("group", { name: /certificate the test was offered/i });

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ trusted_cert_sha256: "" }));
  });

  it("probes with the stored pin, so Test it answers for the monitor as saved", async () => {
    const fetch = vi.fn().mockResolvedValue(respond(passed())); vi.stubGlobal("fetch", fetch);
    render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, trusted_cert_sha256: FP })} onSave={vi.fn()} />);
    click(/test it/i);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    expect(sent(fetch, 0).trusted_cert_sha256).toBe(FP);
  });

  it("trusts the certificate a failed test was offered, and requires that retest before saving it", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(respond(failed())).mockResolvedValueOnce(respond(passed()));
    vi.stubGlobal("fetch", fetch);
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
    expect(screen.queryByRole("button", { name: "Stop trusting" })).toBeNull();
    click(/test it/i);
    await screen.findByRole("group", { name: /certificate the test was offered/i });
    expect(screen.getByRole("status").textContent).toMatch(/shown below/);

    click("Trust this certificate");
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(sent(fetch, 1)).toEqual(expect.objectContaining({ target: raw.target, trusted_cert_sha256: FP }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toMatch(/answered/));
    expect(screen.getByRole("button", { name: "Stop trusting" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ trusted_cert_sha256: FP }));
  });

  it("offers a replacement when the server now presents another certificate", async () => {
    const fetch = vi.fn().mockResolvedValue(respond(failed({ error: "certificate changed since it was trusted on 2026-10-11", certificate: cert(OTHER) })));
    vi.stubGlobal("fetch", fetch);
    render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, trusted_cert_sha256: FP })} onSave={vi.fn()} />);
    click(/test it/i);
    const shown = await screen.findByRole("group", { name: /certificate the test was offered/i });
    expect(shown.textContent).toMatch(/different certificate from the one trusted/);
    expect(within(shown).getByText(OTHER)).toBeTruthy();
    expect(screen.getByText(FP)).toBeTruthy();
  });

  it("offers no pin on a monitor that cannot carry one", () => {
    render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, target: "http://pve.home.arpa:8006" })} onSave={vi.fn()} />);
    expect(screen.queryByText(/trusted certificate/i)).toBeNull();
  });
});
