import { describe, expect, it } from "vitest";
import { maintenanceInputs, refusalInput } from "./maintenanceFields";

describe("maintenanceInputs", () => {
  it("lists the inputs a one-off window for one monitor draws", () => {
    expect(maintenanceInputs("monitor", false)).toEqual(["name", "monitor_id", "starts_at", "ends_at"]);
  });

  it("lists the inputs a weekly window for a tag group draws", () => {
    expect(maintenanceInputs("tag", true)).toEqual(
      ["name", "tag_key", "tag_value", "timezone", "weekdays", "local_time", "duration_minutes"]);
  });
});

describe("refusalInput", () => {
  it("puts a refusal under the input it names when the form draws that input", () => {
    expect(refusalInput("timezone", "monitor", true)).toBe("timezone");
    expect(refusalInput("tag_value", "tag", false)).toBe("tag_value");
    expect(refusalInput("starts_at", "monitor", false)).toBe("starts_at");
  });

  it("sends a refusal with no field to the form-wide alert", () => {
    expect(refusalInput(null, "monitor", true)).toBeNull();
  });

  it("sends a refusal naming an input the form does not draw now to the form-wide alert", () => {
    // A weekly field while the form is one-off, a tag while it names a monitor.
    expect(refusalInput("timezone", "monitor", false)).toBeNull();
    expect(refusalInput("tag_key", "monitor", true)).toBeNull();
    expect(refusalInput("monitor_id", "tag", true)).toBeNull();
    // A field the form never draws at all.
    expect(refusalInput("created_at", "monitor", true)).toBeNull();
  });
});
