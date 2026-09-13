// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MonitorDetail } from "./MonitorDetail";
import type { Incident, UptimeWindow } from "./detail";
import type { Monitor, MonitorStatus } from "./types";

afterEach(cleanup);

/** jsdom has no layout, so the heartbeat bar needs an explicit width. */
const WIDTH = 720;
const NOW = 1_700_000_060_000;

const monitor = (status: MonitorStatus, over: Partial<Monitor> = {}): Monitor => ({
  id: "7",
  name: "api.example.com",
  status,
  target: "https://api.example.com/health",
  latencyMs: 120,
  uptime24h: 99.9,
  beats: [{ ts: 1_700_000_000_000, ok: true, latencyMs: 120 }],
  lastCheck: 1_700_000_000_000,
  ...over,
});

const window_ = (over: Partial<UptimeWindow> = {}): UptimeWindow => ({
  window: "24h",
  windowS: 86400,
  total: 100,
  up: 99,
  down: 1,
  uptime: 99,
  avgLatencyMs: 120,
  ...over,
});

const incident = (over: Partial<Incident> = {}): Incident => ({
  id: "1",
  startedAt: 1_699_900_000_000,
  resolvedAt: 1_699_903_600_000,
  confirmed: true,
  resolved: true,
  acked: false,
  durationS: 3600,
  ...over,
});

const view = (props: Partial<Parameters<typeof MonitorDetail>[0]> = {}) =>
  render(
    <MonitorDetail
      monitor={monitor("up")}
      windows={[]}
      incidents={[]}
      now={NOW}
      beatWidth={WIDTH}
      {...props}
    />,
  );

describe("the status sentence", () => {
  it("says the status in words, not only as a colour", () => {
    view({ monitor: monitor("down", { error: "connection refused" }) });
    expect(screen.getByText("Down")).toBeTruthy();
  });

  it("gives the reason when a monitor is down, which is why you opened it", () => {
    view({ monitor: monitor("down", { error: "connection refused" }) });
    expect(document.body.textContent).toContain("connection refused");
  });

  it("does not print a stale error next to a healthy status", () => {
    // The API keeps `error` on a monitor that has since recovered. Showing it
    // beside "Up" would report an outage that is over.
    view({ monitor: monitor("up", { error: "connection refused" }) });
    expect(document.body.textContent).not.toContain("connection refused");
  });

  it("says when the monitor was last checked", () => {
    view({ monitor: monitor("up", { lastCheck: NOW - 120_000 }) });
    expect(document.body.textContent).toContain("checked 2 min ago");
  });
});

describe("uptime windows", () => {
  it("shows a window per row with its failure count", () => {
    view({ windows: [window_({ window: "7d", total: 1000, down: 3, uptime: 99.7 })] });
    expect(screen.getByText("7d")).toBeTruthy();
    expect(document.body.textContent).toContain("3 of 1000 failed");
  });

  it("renders an unknown uptime as unknown, never as 0%", () => {
    // A monitor created an hour ago has no 30d uptime. "0%" would read as a
    // month-long outage.
    view({ windows: [window_({ window: "30d", total: 0, up: 0, down: 0, uptime: null })] });
    expect(document.body.textContent).toContain("No uptime data");
    expect(document.body.textContent).not.toContain("0%");
    expect(document.body.textContent).toContain("no checks");
  });

  it("still renders a real 0 percent as a number", () => {
    view({ windows: [window_({ total: 10, up: 0, down: 10, uptime: 0 })] });
    expect(screen.getByText("0%")).toBeTruthy();
  });
});

describe("incidents", () => {
  it("distinguishes an ongoing incident from a resolved one", () => {
    view({ incidents: [incident({ resolved: false, resolvedAt: null, durationS: 900 })] });
    expect(document.body.textContent).toContain("Ongoing for 15 min");
    const item = document.querySelector(".mon-detail-incident");
    expect(item?.getAttribute("data-resolved")).toBe("false");
  });

  it("reports how long a resolved incident lasted", () => {
    view({ incidents: [incident({ durationS: 3600 })] });
    expect(document.body.textContent).toContain("Resolved after 1 h");
  });

  it("says nothing has gone wrong rather than showing an empty list", () => {
    view({ incidents: [] });
    expect(document.body.textContent).toContain("Nothing has gone wrong yet");
  });

  it("omits the time element when the timestamp could not be parsed", () => {
    // A <time dateTime> built from NaN is machine-readable and wrong, which is
    // worse than no element at all.
    view({ incidents: [incident({ startedAt: null })] });
    expect(document.querySelector("time")).toBeNull();
    expect(document.body.textContent).toContain("Unknown start");
  });
});

describe("loading and failure", () => {
  it("says the panels are loading rather than claiming there is no data", () => {
    view({ loading: true });
    expect(document.body.textContent).toContain("Loading uptime");
    expect(document.body.textContent).not.toContain("No uptime data yet");
  });

  it("reports a failure as an alert, not as an empty state", () => {
    view({ error: new Error("HTTP 500") });
    const alerts = document.querySelectorAll('[role="alert"]');
    expect(alerts.length).toBeGreaterThan(0);
    expect(document.body.textContent).toContain("HTTP 500");
    expect(document.body.textContent).not.toContain("Nothing has gone wrong yet");
  });

  it("still shows the live status when the extra panels failed", () => {
    // The monitor comes from the stream, not from the failed request. Losing
    // incident history must not blank out the answer the page exists to give.
    view({ monitor: monitor("down", { error: "timeout" }), error: new Error("HTTP 500") });
    expect(screen.getByText("Down")).toBeTruthy();
  });
});

describe("the stale signal", () => {
  it("carries the same attribute the dashboard uses, so one CSS rule covers both", () => {
    view({ stale: true });
    expect(document.querySelector(".mon-detail")?.getAttribute("data-conn")).toBe("stale");
  });

  it("is live by default", () => {
    view();
    expect(document.querySelector(".mon-detail")?.getAttribute("data-conn")).toBe("live");
  });
});
