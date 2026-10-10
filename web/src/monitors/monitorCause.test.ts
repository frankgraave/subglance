import { describe, expect, it } from "vitest";
import { monitorCause } from "./monitorCause";
import type { MonitorStatus } from "./types";

/*
 * The one rule the rows and compact layouts both read for a monitor's why
 * (SUB-203). The compact layout used to keep its own copy, on `down` only and
 * in the raw error, which is how the two layouts came to say the same failure
 * in two ways.
 */
describe("monitorCause", () => {
  const cause = (status: MonitorStatus, error?: string, failureKind?: string) =>
    monitorCause({ status, error, failureKind });

  it("names a classed failure in the incident row's words", () => {
    expect(cause("down", "dial tcp: connect: connection refused", "connection")).toBe(
      "connection refused",
    );
  });

  it("falls back to the server's own message for an unclassed failure", () => {
    expect(cause("down", "something new broke")).toBe("something new broke");
  });

  it("says why for the statuses that have a why", () => {
    expect(cause("expiring", "certificate expires in 6 days", "cert_expiry")).toBe(
      "certificate expiring",
    );
    expect(cause("unknown", "registry did not answer", "unknown")).toBe("expiry date unknown");
  });

  it("says nothing for the statuses that have none", () => {
    for (const status of ["up", "warning", "pending", "recovering", "paused", "waiting"] as const) {
      expect(cause(status, "timeout", "timeout"), status).toBeNull();
    }
  });

  it("claims no why without an error", () => {
    expect(cause("down", undefined, "timeout")).toBeNull();
    expect(cause("down", "", "timeout")).toBeNull();
  });
});
