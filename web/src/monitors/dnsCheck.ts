/**
 * A dns monitor's settings, as the API reads and writes them, and the
 * translation from the form's text.
 *
 * The expected values are one per line in the form rather than separated by
 * commas, because a TXT record (an SPF policy, a verification token) may
 * itself contain commas, and a separator that can appear inside a value is
 * one that splits it in the wrong place.
 */

export const DNS_RECORD_TYPES = ["A", "AAAA", "CNAME", "MX", "TXT"] as const;

/** The wire shape: `dns` on create, PATCH, preview and the detail read. */
export type DnsCheck = {
  record_type: string;
  expected: string[];
  resolver?: string;
};

/** The settings as text for the form: values one per line. */
export function dnsExpectedText(expected: readonly string[] | undefined): string {
  return (expected ?? []).join("\n");
}

/**
 * The settings the form holds, as the request field. Blank lines are
 * dropped, and so is an empty resolver: omitted is the host's own.
 */
export function dnsFrom(recordType: string, expectedText: string, resolver: string): DnsCheck {
  const expected = expectedText
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
  const trimmed = resolver.trim();
  return { record_type: recordType, expected, ...(trimmed !== "" ? { resolver: trimmed } : {}) };
}

/** Help under the expected values, per record type: what one line looks like. */
export const DNS_EXPECTED_HELP: Record<string, string> = {
  A: "One IPv4 address per line. The answer must hold exactly these addresses; an extra one fails the check.",
  AAAA: "One IPv6 address per line. The answer must hold exactly these addresses; an extra one fails the check.",
  CNAME: "The name this one points to, such as shop.example-cdn.com.",
  MX: "One mail host per line, with its preference if that should be checked too: 10 mail.example.com. The answer must hold exactly these.",
  TXT: "One record per line, written in full. Each must be present; other TXT records on the name are allowed.",
};

/** Placeholder for the expected values, per record type. */
export const DNS_EXPECTED_PLACEHOLDER: Record<string, string> = {
  A: "192.0.2.10",
  AAAA: "2001:db8::10",
  CNAME: "shop.example-cdn.com",
  MX: "10 mail.example.com",
  TXT: "v=spf1 include:_spf.example.com -all",
};

export const DNS_RESOLVER_HELP =
  "Leave empty to ask the resolver of the server SubGlance runs on. Set one, such as 1.1.1.1 or your zone’s own name server, to see past a local cache.";

export const DNS_EMPTY_HELP = "With no values, any record of this type passes.";
