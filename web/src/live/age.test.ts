import { describe, expect, it } from "vitest";
import { describeAge } from "./age";

const now = Date.parse("2026-09-11T12:00:00Z");
const ago = (ms: number) => now - ms;

describe("describeAge", () => {
  it("says 'just now' inside the first minute", () => {
    expect(describeAge(ago(0), now)).toBe("just now");
    expect(describeAge(ago(59_000), now)).toBe("just now");
  });

  it("counts whole minutes, then whole hours", () => {
    expect(describeAge(ago(60_000), now)).toBe("1 min ago");
    expect(describeAge(ago(59 * 60_000), now)).toBe("59 min ago");
    expect(describeAge(ago(60 * 60_000), now)).toBe("1 h ago");
    expect(describeAge(ago(150 * 60_000), now)).toBe("2 h ago");
  });

  it("has nothing to say without a timestamp", () => {
    // A monitor that was never checked has no age; "0 min ago" would claim it
    // was just measured.
    expect(describeAge(null, now)).toBeNull();
    expect(describeAge(undefined, now)).toBeNull();
  });

  it("refuses a future timestamp rather than printing a negative age", () => {
    expect(describeAge(now + 5_000, now)).toBeNull();
  });
});
