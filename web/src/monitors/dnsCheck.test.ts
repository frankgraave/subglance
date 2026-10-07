import { describe, expect, it } from "vitest";
import { DNS_EXPECTED_HELP, DNS_EXPECTED_PLACEHOLDER, DNS_RECORD_TYPES, dnsExpectedText, dnsFrom } from "./dnsCheck";
import { fingerprintPreview } from "./preview";

describe("dnsFrom", () => {
  it("splits on lines only, trims each and drops blanks", () => {
    expect(dnsFrom("TXT", " a, b \n\n c ", "")).toEqual({ record_type: "TXT", expected: ["a, b", "c"] });
  });
  it("keeps a resolver that was set, trimmed", () => {
    expect(dnsFrom("A", "", " 1.1.1.1 ")).toEqual({ record_type: "A", expected: [], resolver: "1.1.1.1" });
  });
  it("round-trips through the text the form shows", () => {
    const expected = ["10 mx1.example.com", "20 mx2.example.com"];
    expect(dnsFrom("MX", dnsExpectedText(expected), "").expected).toEqual(expected);
  });
});

it("has help and a placeholder for every record type", () => {
  for (const t of DNS_RECORD_TYPES) {
    expect(DNS_EXPECTED_HELP[t], t).toBeTruthy();
    expect(DNS_EXPECTED_PLACEHOLDER[t], t).toBeTruthy();
  }
});

it("gives a preview of other dns settings another fingerprint", () => {
  // A passing preview says nothing about a different record, value or
  // resolver, so a save must not reuse it.
  const base = { type: "dns", target: "example.com" };
  const one = fingerprintPreview({ ...base, dns: dnsFrom("A", "192.0.2.1", "") });
  expect(fingerprintPreview({ ...base, dns: dnsFrom("A", "192.0.2.2", "") })).not.toBe(one);
  expect(fingerprintPreview({ ...base, dns: dnsFrom("AAAA", "192.0.2.1", "") })).not.toBe(one);
  expect(fingerprintPreview({ ...base, dns: dnsFrom("A", "192.0.2.1", "1.1.1.1") })).not.toBe(one);
  expect(fingerprintPreview({ ...base, dns: dnsFrom("A", "192.0.2.1", "") })).toBe(one);
});
