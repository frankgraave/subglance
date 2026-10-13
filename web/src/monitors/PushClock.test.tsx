// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AddMonitor } from "./AddMonitor";
import { EditMonitorForm } from "./EditMonitorForm";
import { inventoryFromApi } from "./inventory";
import { describePushWindow } from "./format";
import { pushIntervalTakesZone, pushZoneFor } from "./pushClock";
import { ApiError } from "./preview";

afterEach(cleanup);

const URL_ = "http://box.local:8080/api/v1/push/sgu_secrettoken";
const ZONE = /^runs on the clock of$/i;
const change = (label: RegExp | string, value: string) =>
  fireEvent.change(screen.getByLabelText(label), { target: { value } });

describe("which intervals take a zone", () => {
  it("is whole days only", () => {
    expect(pushIntervalTakesZone(86400)).toBe(true);
    expect(pushIntervalTakesZone("604800")).toBe(true);
    expect(pushIntervalTakesZone(3600)).toBe(false);
    expect(pushIntervalTakesZone(90000)).toBe(false);
    expect(pushIntervalTakesZone(0)).toBe(false);
    expect(pushIntervalTakesZone("")).toBe(false);
  });

  it("drops a zone the interval cannot take, and trims one it can", () => {
    expect(pushZoneFor(86400, " Europe/Amsterdam ")).toBe("Europe/Amsterdam");
    expect(pushZoneFor(3600, "Europe/Amsterdam")).toBe("");
  });
});

describe("the add form", () => {
  const openPush = () => {
    const create = vi.fn().mockResolvedValue({ id: "9", pushUrl: URL_ });
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    change(/check type/i, "push");
    change(/^name$/i, "Nightly backup");
    return create;
  };

  it("offers the clock only once the interval is whole days, empty", () => {
    openPush();
    expect(screen.queryByLabelText(ZONE)).toBeNull();
    change("Should report every: unit", "d");
    const zone = screen.getByLabelText(ZONE) as HTMLInputElement;
    expect(zone.value).toBe("");
    expect(zone.closest("details")).toBeNull();
    expect(zone.getAttribute("aria-describedby")).toMatch(/\S/);
  });

  it("sends the zone with a daily window", async () => {
    const create = openPush();
    change("Should report every: unit", "d");
    change(ZONE, " Europe/Amsterdam ");
    fireEvent.click(screen.getByRole("button", { name: /save monitor/i }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toMatchObject({ push_interval_s: 86400, push_timezone: "Europe/Amsterdam" });
  });

  it("leaves it out when empty, and when the interval went back to hours", async () => {
    const create = openPush();
    change("Should report every: unit", "d");
    change(ZONE, "Europe/Amsterdam");
    change("Should report every: unit", "h");
    expect(screen.queryByLabelText(ZONE)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /save monitor/i }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).not.toHaveProperty("push_timezone");
  });

  it("places a refusal of the zone under it", async () => {
    const create = vi.fn().mockRejectedValue(
      new ApiError(400, "push_timezone: unknown IANA timezone \"Mars/Base\"", null, "push_timezone"),
    );
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    change(/check type/i, "push");
    change(/^name$/i, "Nightly backup");
    change("Should report every: unit", "d");
    change(ZONE, "Mars/Base");
    fireEvent.click(screen.getByRole("button", { name: /save monitor/i }));
    const zone = screen.getByLabelText(ZONE);
    await waitFor(() => expect(zone.getAttribute("aria-invalid")).toBe("true"));
    expect(zone.closest(".field")?.textContent).toMatch(/unknown IANA timezone/);
  });
});

describe("the edit form", () => {
  const raw = {
    id: 3, name: "Nightly", type: "push", target: "", interval_s: 60, timeout_s: 10, enabled: true,
    status: "up" as const, created_at: "2026-09-01T00:00:00Z", repeat_after_s: 900,
    push_interval_s: 86400, push_grace_s: 60,
  };

  it("loads the stored zone and sends only a change to it", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, push_timezone: "Europe/Amsterdam" })} onSave={onSave} />);
    expect((screen.getByLabelText(ZONE) as HTMLInputElement).value).toBe("Europe/Amsterdam");
    change("Allow it to be late by", "5");
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ push_grace_s: 300 }));
  });

  it("sets and clears it", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={inventoryFromApi(raw)} onSave={onSave} />);
    change(ZONE, "America/New_York");
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ push_timezone: "America/New_York" }));
  });

  it("clears a stored zone in the same save that makes the window hourly", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, push_timezone: "Europe/Amsterdam" })} onSave={onSave} />);
    change("Should report every: unit", "h");
    expect(screen.queryByLabelText(ZONE)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ push_interval_s: 3600, push_timezone: "" }));
  });

  it("is not offered for an hourly window", () => {
    render(<EditMonitorForm monitor={inventoryFromApi({ ...raw, push_interval_s: 3600 })} onSave={vi.fn()} />);
    expect(screen.queryByLabelText(ZONE)).toBeNull();
  });
});

describe("the monitor's page", () => {
  it("names the clock beside the window", () => {
    expect(describePushWindow(86400, 60, "Europe/Amsterdam")).toBe("Expected every 1 d on Europe/Amsterdam time, 1 min grace");
    expect(describePushWindow(86400, 60)).not.toMatch(/time/);
  });
});
