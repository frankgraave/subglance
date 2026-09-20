// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MonitorDetail } from "./MonitorDetail";
import type { Incident, UptimeWindow } from "./detail";
import type { Monitor, MonitorStatus } from "./types";

afterEach(cleanup);

/** jsdom has no layout, so the heartbeat bar needs an explicit width. */
const WIDTH = 720;
const NOW = 1_700_000_060_000;

const monitor = (
  status: MonitorStatus,
  over: Partial<Monitor> = {},
): Monitor => ({
  id: "7",
  name: "api.example.com",
  status,
  target: "https://api.example.com/health",
  latencyMs: 120,
  uptime24h: 99.9,
  beats: [{ ts: 1_700_000_000_000, ok: true, latencyMs: 120 }],
  lastCheck: 1_700_000_000_000,
  tags: {},
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
  monitorId: "7",
  startedAt: 1_699_900_000_000,
  confirmedAt: 1_699_900_060_000,
  resolvedAt: 1_699_903_600_000,
  ackedAt: null,
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

describe("detail Check now", () => {
  it("offers a check for a probed monitor and not for a push window", () => {
    const check = vi.fn();
    const mounted = view({ onCheckNow: check });
    fireEvent.click(screen.getByRole("button", { name: "Check now" }));
    expect(check).toHaveBeenCalledTimes(1);
    mounted.unmount();
    view({ onCheckNow: check, monitor: monitor("waiting", {
      push: { intervalS: 3600, graceS: 60, tokenPrefix: "prefix" },
    }) });
    expect(screen.queryByRole("button", { name: /check now/i })).toBeNull();
  });

  it("does not offer a write without a handler", () => {
    view();
    expect(screen.queryByRole("button", { name: "Check now" })).toBeNull();
  });

  it("disables the control while checking and reports failures in place", () => {
    view({ onCheckNow: vi.fn(), checking: true, checkError: new Error("rate limited") });
    expect((screen.getByRole("button", { name: "Checking…" }) as HTMLButtonElement).disabled).toBe(true);
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("rate limited");
    expect(alert.querySelector('svg[aria-hidden="true"]')).not.toBeNull();
  });

  it.each([true, false])("shows the probe result and its recording status: %s", (recorded) => {
    view({ onCheckNow: vi.fn(), checkResult: { ok: false, latencyMs: 0, statusCode: 503,
      error: "service unavailable", recorded } });
    const result = screen.getByRole("status");
    expect(result.textContent).toContain("Check failed");
    expect(result.textContent).toContain("0 ms");
    expect(result.textContent).toContain("HTTP 503");
    expect(result.textContent).toContain("service unavailable");
    expect(result.textContent).toContain(recorded ? "Recorded" : "Not recorded");
  });
});

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

  it("keeps the state on the title line, grouped with the name", () => {
    /*
     * The header used to be three children of the page's 20px flex column —
     * name, target, status — so a monitor's own status sat as far from its
     * hostname as the whole block sat from the first card, and the three facts
     * read as three unrelated rows.
     *
     * Asserted structurally rather than by looking at pixels: the pill has to
     * be *inside* the title row for the grouping to survive a refactor that
     * only moves CSS around.
     */
    const { container } = view({ monitor: monitor("up") });
    const row = container.querySelector(".mon-detail-titlerow");
    expect(row, "the header needs a title row").not.toBeNull();
    expect(
      row?.querySelector(".mon-detail-name"),
      "the name belongs on that row",
    ).not.toBeNull();
    expect(
      row?.querySelector(".mon-detail-status"),
      "so does the state — that is the whole point of the row",
    ).not.toBeNull();
  });

  it("keeps the reason out of the pill and gives it its own line", () => {
    /*
     * A pill holds a lamp, a word and an age: all short by construction. An
     * error is free text — "DNS lookup failed: host axonlawyers.com not
     * found" — and letting one into the pill would push the title around and
     * wrap the line it sits on.
     *
     * This is the assertion that stops the next person from folding the reason
     * back into the status element because it reads better in one sentence.
     */
    const reason = "DNS lookup failed: host api.example.com not found";
    const { container } = view({ monitor: monitor("down", { error: reason }) });

    const pill = container.querySelector(".mon-detail-status");
    expect(pill?.textContent, "the pill states the status, not the reason").not.toContain(
      "DNS lookup failed",
    );
    expect(
      container.querySelector(".mon-detail-reason")?.textContent,
      "the reason gets a line of its own",
    ).toContain(reason);
  });
});

describe("uptime windows", () => {
  it("shows a window per row with its failure count", () => {
    view({
      windows: [window_({ window: "7d", total: 1000, down: 3, uptime: 99.7 })],
    });
    expect(screen.getByText("7d")).toBeTruthy();
    expect(document.body.textContent).toContain("3 of 1000 failed");
  });

  it("renders an unknown uptime as unknown, never as 0%", () => {
    // A monitor created an hour ago has no 30d uptime. "0%" would read as a
    // month-long outage.
    view({
      windows: [
        window_({ window: "30d", total: 0, up: 0, down: 0, uptime: null }),
      ],
    });
    const windows = document.querySelector(".mon-detail-windows");
    expect(windows, "the uptime windows list").toBeTruthy();
    expect(document.body.textContent).toContain("No uptime data");
    // Scoped to the windows list rather than the whole page. This assertion
    // used to scan document.body, which meant it also read the heartbeat
    // panel — and the moment that panel gained a chrome reading of "100.00%",
    // an unrelated correct number tripped a test about a different panel.
    expect(windows?.textContent).not.toContain("0%");
    expect(document.body.textContent).toContain("no checks");
  });

  it("still renders a real 0 percent as a number", () => {
    view({ windows: [window_({ total: 10, up: 0, down: 10, uptime: 0 })] });
    expect(screen.getByText("0%")).toBeTruthy();
  });
});

describe("incidents", () => {
  it("distinguishes an ongoing incident from a resolved one", () => {
    view({
      incidents: [
        incident({ resolved: false, resolvedAt: null, durationS: 900 }),
      ],
    });
    expect(document.body.textContent).toContain("15 min and counting");
    const item = document.querySelector(".inc-row");
    expect(item?.getAttribute("data-state")).toBe("open");
  });

  it("reports how long a resolved incident lasted, and when it came back", () => {
    view({ incidents: [incident({ durationS: 3600 })] });
    expect(document.body.textContent).toContain("Resolved");
    expect(document.body.textContent).toContain("1 h");
    expect(document.body.textContent).toContain("Recovered at");
  });

  it("offers the ack control here too, not only on the incidents screen", () => {
    /*
     * The plumbing test, and it exists because a mutation found the hole: the
     * detail page can accept `onAck` and quietly not forward it, and every
     * other assertion in this file would still pass.
     *
     * It matters because this is the screen an alert link lands on. SUB-81
     * made ack the control that stops the escalating repeat ladder, and a
     * control one navigation further away than the page you arrive at is one
     * nobody uses at 03:00 — which leaves the repeats running.
     */
    const onAck = vi.fn();
    view({
      incidents: [incident({ resolved: false, resolvedAt: null })],
      onAck,
    });
    const button = screen.getByRole("button", { name: /mute repeat/i });
    fireEvent.click(button);
    expect(onAck).toHaveBeenCalledWith("1");
  });

  it("keeps an acked incident visibly open on this page as well", () => {
    // The ticket's central requirement, asserted on the surface that already
    // existed rather than only on the new screen.
    view({
      incidents: [
        incident({
          resolved: false,
          resolvedAt: null,
          acked: true,
          ackedAt: 1_699_900_300_000,
        }),
      ],
      onAck: () => {},
    });
    expect(document.querySelector(".inc-row")?.getAttribute("data-state")).toBe(
      "acked",
    );
    expect(document.body.textContent).toMatch(/still down/i);
    expect(document.body.textContent).not.toMatch(/Recovered|Resolved/);
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
    expect(document.body.textContent).toContain("start time unknown");
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
    expect(document.body.textContent).not.toContain(
      "Nothing has gone wrong yet",
    );
  });

  it("still shows the live status when the extra panels failed", () => {
    // The monitor comes from the stream, not from the failed request. Losing
    // incident history must not blank out the answer the page exists to give.
    view({
      monitor: monitor("down", { error: "timeout" }),
      error: new Error("HTTP 500"),
    });
    expect(screen.getByText("Down")).toBeTruthy();
  });
});

describe("the stale signal", () => {
  it("carries the same attribute the dashboard uses, so one CSS rule covers both", () => {
    view({ stale: true });
    expect(
      document.querySelector(".mon-detail")?.getAttribute("data-conn"),
    ).toBe("stale");
  });

  it("is live by default", () => {
    view();
    expect(
      document.querySelector(".mon-detail")?.getAttribute("data-conn"),
    ).toBe("live");
  });
});
