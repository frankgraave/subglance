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

  it("leaves the name to the masthead and leads with the state", () => {
    /*
     * The name is the page's title, and every page's `h1` is the masthead's
     * (SUB-207). A heading here would be the second `h1` on the one page
     * that used to draw its own. What stays is the block the name used to
     * head: the state first, then the address, grouped in one header so the
     * facts about one monitor do not read as unrelated rows.
     *
     * The article is still named by the monitor, so the region says which
     * one it is about without a heading inside it.
     */
    const { container } = view({ monitor: monitor("up") });
    expect(container.querySelectorAll("h1")).toHaveLength(0);
    const article = screen.getByRole("article", { name: "api.example.com" });
    const head = article.querySelector(".mon-detail-head");
    expect(head?.firstElementChild?.classList.contains("mon-detail-status"), "the state leads the block").toBe(true);
    expect(head?.querySelector(".mon-detail-target"), "the address is in the same block").not.toBeNull();
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
    expect(document.body.textContent).toContain("3 of 1000 confirmed down");
  });

  it("does not print 100% above a count of confirmed-down checks", () => {
    // The demo's "Marketing site": 21 of 43,138 down is 99.951%, which used
    // to round to "100%" right above the line that counts the 21.
    view({
      windows: [
        window_({
          window: "30d",
          total: 43_138,
          up: 43_117,
          down: 21,
          uptime: (43_117 / 43_138) * 100,
        }),
      ],
    });
    const windows = document.querySelector(".mon-detail-windows");
    expect(windows?.textContent).toContain("21 of 43138 confirmed down");
    expect(windows?.textContent).toContain("99.95%");
    expect(windows?.textContent).not.toContain("100.00%");
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
    expect(document.body.textContent).toContain("no eligible checks");
  });

  it("marks a window longer than the monitor's life as a warning value", () => {
    // Added two days before NOW: the 24h figure is whole, the 30d one is not.
    view({
      monitor: monitor("up", { createdAt: NOW - 2 * 86_400_000 }),
      windows: [
        window_({ window: "24h", windowS: 86400, uptime: 100 }),
        window_({ window: "30d", windowS: 30 * 86400, uptime: 100 }),
      ],
    });
    const cells = [...document.querySelectorAll(".mon-detail-window")];
    const reading = (label: string) =>
      cells
        .find((c) => c.querySelector("dt")?.textContent === label)
        ?.querySelector(".value");
    expect(reading("24h")?.getAttribute("data-warn")).toBeNull();
    const month = reading("30d");
    expect(month?.getAttribute("data-warn")).toBe("true");
    // The caveat is information, so it has an accessible name, not only a tint.
    expect(
      screen.getByRole("img", { name: /added 2 d ago, so this covers 2 d/ }),
    ).toBeTruthy();
  });

  it("puts no caveat on a monitor whose age is unknown", () => {
    view({
      monitor: monitor("up", { createdAt: null }),
      windows: [window_({ window: "30d", windowS: 30 * 86400, uptime: 100 })],
    });
    expect(document.querySelector("[data-warn]")).toBeNull();
  });

  it("still renders a real 0 percent as a number", () => {
    view({ windows: [window_({ total: 10, up: 0, down: 10, uptime: 0 })] });
    expect(screen.getByText("0.00%")).toBeTruthy();
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

  it("keeps the last figures when a refresh fails, and says so", () => {
    // The detail read is polled. One failed poll must not replace an answer
    // that was on screen a minute ago with no answer at all.
    view({
      windows: [window_({ window: "7d", total: 1000, down: 3, uptime: 99.7 })],
      incidents: [incident()],
      loaded: true,
      error: new Error("HTTP 502"),
    });
    const alerts = [...document.querySelectorAll('[role="alert"]')].map((a) => a.textContent);
    expect(alerts).toEqual([
      "Could not refresh uptime: HTTP 502. Showing the last loaded figures.",
      "Could not refresh incidents: HTTP 502. Showing the last loaded list.",
    ]);
    expect(screen.getByText("7d")).toBeTruthy();
    expect(document.querySelectorAll(".inc-list li")).toHaveLength(1);
    expect(document.body.textContent).not.toContain("Could not load");
  });

  it("does not claim old figures on a failed first load", () => {
    view({ error: new Error("HTTP 502") });
    expect(document.body.textContent).toContain("Could not load uptime: HTTP 502");
    expect(document.body.textContent).not.toContain("Showing the last loaded");
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

it("withdraws the maintenance claim when the stream goes stale", () => {
  const maintained = monitor("down", { maintenance: true });
  const props = { monitor: maintained, windows: [], incidents: [], now: NOW, beatWidth: WIDTH };
  const { rerender } = render(<MonitorDetail {...props} />);
  expect(screen.getByText("Scheduled maintenance — checks continue; alerts suppressed.")).toBeTruthy();
  rerender(<MonitorDetail {...props} stale />);
  expect(screen.getByText("Scheduled maintenance when we last heard — checks continued; alerts were suppressed.")).toBeTruthy();
  expect(screen.queryByText(/checks continue;/)).toBeNull();
});

it("counts warning checks separately in the detail legend", () => {
  view({monitor:monitor("down", {beats:[
    {ts:NOW-180_000,ok:true,assessment:"up",latencyMs:100},
    {ts:NOW-120_000,ok:false,assessment:"warning",latencyMs:100},
    {ts:NOW-60_000,ok:false,assessment:"down",latencyMs:100},
  ]})});
  const items=Array.from(document.querySelectorAll('.legend-item'));
  const value=(label:string)=>items.find(item=>item.querySelector('dt')?.textContent===label)?.querySelector('dd')?.textContent;
  expect(value("Passed")).toBe("1");
  expect(value("Failed")).toBe("1");
  expect(value("Warnings (unconfirmed)")).toBe("1");
});

describe("the page's order (SUB-184)", () => {
  it("puts the summaries before the raw failed checks", () => {
    // The overview used to sit under the failure list: a monitor down for
    // forty minutes pushed Uptime, Latency and Incidents six thousand pixels
    // down the page.
    view({
      windows: [window_()],
      responseHistory: { heartbeats: [] },
      latency: { window: "24h", onWindowChange: () => {} },
    });
    const headings = [...document.querySelectorAll("h2")].map((h) => h.textContent);
    expect(headings).toEqual(["Recent checks", "Uptime", "Latency", "Incidents", "Failure responses"]);
  });

  it("keeps how uptime is counted one press away, under the figures", () => {
    view({ windows: [window_()] });
    const note = document.querySelector<HTMLDetailsElement>(".mon-detail-uptime-note")!;
    expect(note.tagName).toBe("DETAILS");
    expect(note.open).toBe(false);
    expect(note.querySelector("summary")?.textContent).toBe("How uptime is counted");
    expect(note.textContent).toContain("This is a sample ratio, not elapsed time.");
    // Under the figures, not above them: the numbers are what the card is for.
    const figures = document.querySelector(".mon-detail-windows")!;
    expect(figures.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("draws every card's no-data finding in one face", async () => {
    view({
      windows: [],
      incidents: [],
      responseHistory: { heartbeats: [] },
      latency: { window: "24h", onWindowChange: () => {} },
    });
    // The failure card loads as its own chunk, after the rest of the page.
    await screen.findByText("No failed checks in the recent history.");
    const empties = [
      "No uptime data yet.",
      "Nothing has gone wrong yet.",
      "No failed checks in the recent history.",
      "No checks in the last 24h.",
    ].map((text) => screen.getByText(text).className);
    expect(empties).toEqual(Array(4).fill("mon-detail-empty"));
  });
});
