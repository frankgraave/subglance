import { describe, expect, it } from "vitest";
import {
  formatDuration,
  formatMoment,
  incidentFromApi,
  windowFromApi,
} from "./detail";

describe("windowFromApi", () => {
  it("keeps a null uptime null instead of collapsing it to 0", () => {
    // A monitor created an hour ago has no 30d uptime. Rendering that as 0%
    // would claim a month-long outage that never happened.
    const w = windowFromApi({
      window: "30d",
      window_s: 2592000,
      total: 0,
      up: 0,
      down: 0,
      uptime: null,
    });
    expect(w.uptime).toBeNull();
    expect(w.total).toBe(0);
  });

  it("keeps a real 0 percent distinct from unknown", () => {
    const w = windowFromApi({
      window: "24h",
      window_s: 86400,
      total: 10,
      up: 0,
      down: 10,
      uptime: 0,
    });
    expect(w.uptime).toBe(0);
  });

  it("reads an absent average latency as unknown", () => {
    const w = windowFromApi({
      window: "24h",
      window_s: 86400,
      total: 5,
      up: 5,
      down: 0,
      uptime: 100,
    });
    expect(w.avgLatencyMs).toBeNull();
  });
});

describe("incidentFromApi", () => {
  const base = {
    id: 9,
    monitor_id: 1,
    started_at: "2026-09-13T04:00:00Z",
    confirmed: true,
    resolved: false,
    acked: false,
    duration_s: 120,
  };

  it("stringifies the id, because it becomes a React key", () => {
    expect(incidentFromApi(base).id).toBe("9");
  });

  it("carries the monitor id, which the incidents screen needs (SUB-34)", () => {
    // Left out of the original translation, and stringified like every other
    // id in this app: a number here fails every `===` against a monitor's id,
    // so the incidents screen would name none of its monitors.
    expect(incidentFromApi(base).monitorId).toBe("1");
  });

  it("carries acked_at and confirmed_at, which were dropped (SUB-34)", () => {
    /*
     * Both were in the API response and absent from the render model.
     *
     * `acked_at` is the one that matters: without it the UI can say *that*
     * somebody acknowledged an incident but not when, and "acknowledged at
     * 14:10 — still down" is the sentence that keeps an acked outage from
     * reading as a finished one. `confirmed_at` is the gap between "we saw
     * it" and "we alerted", which the server exposes deliberately.
     */
    const incident = incidentFromApi({
      ...base,
      acked: true,
      acked_at: "2026-09-13T04:07:00Z",
      confirmed_at: "2026-09-13T04:01:00Z",
    });
    expect(incident.ackedAt).toBe(Date.parse("2026-09-13T04:07:00Z"));
    expect(incident.confirmedAt).toBe(Date.parse("2026-09-13T04:01:00Z"));
  });

  it("leaves an un-acked incident's ack time null rather than 0", () => {
    expect(incidentFromApi(base).ackedAt).toBeNull();
  });

  it("parses timestamps to unix ms and leaves an absent one null", () => {
    const incident = incidentFromApi(base);
    expect(incident.startedAt).toBe(Date.parse("2026-09-13T04:00:00Z"));
    expect(incident.resolvedAt).toBeNull();
  });

  it("does not invent a resolution time for an open incident", () => {
    const incident = incidentFromApi({ ...base, resolved: false });
    expect(incident.resolved).toBe(false);
    expect(incident.resolvedAt).toBeNull();
  });
});

describe("formatDuration", () => {
  it("keeps seconds only below a minute, where they are the whole story", () => {
    expect(formatDuration(0)).toBe("0 s");
    expect(formatDuration(59)).toBe("59 s");
  });

  it("drops seconds above a minute rather than reading out three units", () => {
    expect(formatDuration(60)).toBe("1 min");
    expect(formatDuration(3599)).toBe("59 min");
  });

  it("gives hours a minute remainder, and drops it when it is zero", () => {
    expect(formatDuration(3600)).toBe("1 h");
    expect(formatDuration(3600 + 41 * 60 + 12)).toBe("1 h 41 min");
  });

  it("rolls over to days", () => {
    expect(formatDuration(86400)).toBe("1 d");
    expect(formatDuration(86400 + 7200)).toBe("1 d 2 h");
  });

  it("says unknown rather than printing a negative or NaN duration", () => {
    expect(formatDuration(-5)).toBe("unknown");
    expect(formatDuration(Number.NaN)).toBe("unknown");
  });
});

describe("formatMoment", () => {
  it("returns null for a missing instant instead of Invalid Date", () => {
    expect(formatMoment(null)).toBeNull();
    expect(formatMoment(Number.NaN)).toBeNull();
  });

  it("renders a real instant as text", () => {
    expect(formatMoment(Date.parse("2026-09-13T04:00:00Z"))).toBeTruthy();
  });
});
