// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { IncidentsView } from "./IncidentsView";
import { IncidentStoryItem } from "./IncidentStoryItem";
import { MonitorDetail } from "../monitors/MonitorDetail";
import type { Incident } from "../monitors/detail";
import type { Monitor } from "../monitors/types";

/**
 * SUB-34, the half that is markup.
 *
 * The rule under test is not "the acked row looks different". It is stronger
 * and it is the one a redesign could quietly break: **an acknowledged incident
 * must be distinguishable from a resolved one by a sighted reader AND by a
 * screen reader, from the text alone.**
 *
 * That pairing is why the assertions come in twos throughout this file. jsdom
 * applies no CSS, so a treatment that lived only in a stylesheet would be
 * invisible here — which is exactly the point. Anything this file can see is
 * something a reader with stylesheets off, a failed stylesheet, or a screen
 * reader also gets. The colour is a second voice; these tests assert the
 * first.
 *
 * `visibleOnly` is borrowed from `stale-tense.test.tsx` for the reason
 * documented there.
 */

afterEach(cleanup);

const T0 = 1_700_000_000_000;
const NOW = T0 + 720_000;

const incident = (over: Partial<Incident> = {}): Incident => ({
  id: "1",
  monitorId: "7",
  startedAt: T0,
  confirmedAt: T0 + 60_000,
  resolvedAt: null,
  ackedAt: null,
  confirmed: true,
  resolved: false,
  acked: false,
  durationS: 720,
  cause: "dns",
  lastError: "lookup api.example.com: no such host",
  ...over,
});

/** Text a sighted reader can actually see: `sr-only` text is excluded. */
function visibleOnly(el: Element): string {
  const clone = el.cloneNode(true) as Element;
  clone.querySelectorAll(".sr-only, .hb-sr-only").forEach((n) => n.remove());
  return (clone.textContent ?? "").trim();
}

/** Text only a screen reader gets. */
function srOnly(el: Element): string {
  return [...el.querySelectorAll(".sr-only")]
    .map((n) => n.textContent ?? "")
    .join(" ");
}

const row = (over: Partial<Incident> = {}, props = {}) =>
  render(
    <ul>
      <IncidentStoryItem
        incident={incident(over)}
        now={NOW}
        subject="api"
        {...props}
      />
    </ul>,
  );

describe("acknowledged is not resolved — for the eye", () => {
  it("shows an acked incident as still down, in words", () => {
    const { container } = row({ acked: true, ackedAt: T0 + 420_000 });
    const seen = visibleOnly(container.querySelector(".inc-row")!);
    expect(seen).toMatch(/still down/i);
    // And it must not borrow any of the vocabulary of a recovery.
    expect(seen).not.toMatch(/Recovered|Resolved/i);
  });

  it("shows a resolved incident as resolved, with no ack language", () => {
    const { container } = row({
      resolved: true,
      resolvedAt: T0 + 720_000,
      acked: true,
      ackedAt: T0 + 420_000,
    });
    const seen = visibleOnly(container.querySelector(".inc-row")!);
    expect(seen).toMatch(/Resolved/);
    // The incident was acked, and it is over. Leading with the ack would put
    // a live-response state on a row that is history.
    expect(seen).not.toMatch(/still down/i);
  });

  it("gives the three states three different data-state values", () => {
    // The hook a stylesheet keys off. Asserted so a refactor cannot collapse
    // acked into resolved in the markup and leave the CSS looking correct.
    for (const [over, state] of [
      [{}, "open"],
      [{ acked: true, ackedAt: T0 }, "acked"],
      [{ resolved: true, resolvedAt: T0 + 1 }, "resolved"],
    ] as const) {
      const { container } = row(over);
      expect(
        container.querySelector(".inc-row")?.getAttribute("data-state"),
      ).toBe(state);
      cleanup();
    }
  });

  it("keeps the service and the response in separate columns", () => {
    /*
     * The structural half of the rule. The lamp and the tint answer "is it
     * broken"; the chip answers "is anybody on it". One combined word is how
     * "acked" comes to read as "fixed", so the two are separate elements and
     * a refactor that merges them fails here.
     */
    const { container } = row({ acked: true, ackedAt: T0 });
    expect(container.querySelector(".inc-led")).not.toBeNull();
    const ack = container.querySelector(".inc-col-ack")!;
    expect(ack.textContent).toMatch(/still down/i);
  });
});

describe("acknowledged is not resolved — for the ear", () => {
  it("tells a screen reader the same sentence the eye is shown", () => {
    /*
     * The pairing DESIGN.md §9.1 exists for. In this product's list layouts
     * the status word is often `sr-only`, and the classic failure is a state
     * that is a colour on screen and a different word for the ear. Here both
     * come from one `incidentStory` call, and this assertion keeps that true.
     */
    const { container } = row({ acked: true, ackedAt: T0 + 420_000 });
    const heard = srOnly(container.querySelector(".inc-row")!);
    expect(heard).toMatch(/Acknowledged/);
    expect(heard).toMatch(/still down/i);
    expect(heard).not.toMatch(/Recovered|Resolved/i);
  });

  it("never lets a screen reader hear an acked incident as finished", () => {
    const { container } = row({ acked: true, ackedAt: T0 + 420_000 });
    expect(srOnly(container.querySelector(".inc-row")!)).toContain(
      "and counting",
    );
  });

  it("does not make the eye and the ear read two different states", () => {
    for (const over of [
      {},
      { acked: true, ackedAt: T0 + 60_000 },
      { resolved: true, resolvedAt: T0 + 720_000 },
    ]) {
      const { container } = row(over);
      const item = container.querySelector(".inc-row")!;
      const seenDown = /still down|Unacked/i.test(visibleOnly(item));
      const heardDown = /still down|Down since/i.test(srOnly(item));
      expect(seenDown).toBe(heardDown);
      cleanup();
    }
  });
});

describe("the row is a story, not a log line", () => {
  it("shows the kind, the start time and the duration as columns", () => {
    const { container } = row();
    expect(container.querySelector(".inc-col-kind")?.textContent).toContain(
      "DNS failure",
    );
    expect(container.querySelector(".inc-col-dur")?.textContent).toContain(
      "12 min",
    );
    expect(container.querySelector(".inc-col-time")?.textContent).toBeTruthy();
  });

  it("prints the cause in words, never as the database key", () => {
    const { container } = row({ cause: "dns" });
    const kind = container.querySelector(".inc-col-kind")!;
    expect(kind.textContent).toContain("DNS failure");
    expect(kind.textContent).not.toMatch(/^dns$/);
  });
});

describe("the detail expands inline, not into a side panel", () => {
  it("is closed until the row is opened", () => {
    const { container } = row();
    expect(container.querySelector(".inc-detail")).toBeNull();
    expect(
      container.querySelector(".inc-line")?.getAttribute("aria-expanded"),
    ).toBe("false");
  });

  it("opens in place, inside the same row element", () => {
    /*
     * The structural assertion behind the design decision. A 320px side panel
     * cannot hold an error string and a timeline without wrapping both into
     * mush, and choosing a row would reflow the whole page under the reader's
     * eye. "In place" means the detail is a child of the row — asserted, so a
     * refactor cannot quietly lift it into a sibling panel.
     */
    const { container } = row();
    fireEvent.click(container.querySelector(".inc-line")!);
    const item = container.querySelector(".inc-row")!;
    expect(item.querySelector(".inc-detail")).not.toBeNull();
    expect(
      container.querySelector(".inc-line")?.getAttribute("aria-expanded"),
    ).toBe("true");
  });

  it("lets two rows be open at once, to compare two failures", () => {
    // The point of expanding in place rather than selecting: two failures a
    // minute apart are usually one event, and comparing them needs both.
    const { container } = render(
      <ul>
        <IncidentStoryItem incident={incident()} now={NOW} subject="a" />
        <IncidentStoryItem
          incident={incident({ id: "2", monitorId: "8" })}
          now={NOW}
          subject="b"
        />
      </ul>,
    );
    for (const line of container.querySelectorAll(".inc-line")) {
      fireEvent.click(line);
    }
    expect(container.querySelectorAll(".inc-detail").length).toBe(2);
  });

  it("shows the timeline and the error once open", () => {
    const { container } = row();
    fireEvent.click(container.querySelector(".inc-line")!);
    const detail = container.querySelector(".inc-detail")!;
    expect(detail.textContent).toContain("First failure observed");
    expect(detail.textContent).toContain("Not recovered");
    expect(detail.textContent).toContain("no such host");
  });

  it("says an error was not recorded rather than leaving a blank", () => {
    const { container } = row({ lastError: undefined });
    fireEvent.click(container.querySelector(".inc-line")!);
    expect(container.querySelector(".inc-detail")?.textContent).toMatch(
      /No error text was recorded/i,
    );
  });

  it("claims no response snapshot, because the API carries none", () => {
    // A panel headed "Response snapshot" over invented content would be worse
    // than its absence on a tool whose value is that what it shows is true.
    const { container } = row();
    fireEvent.click(container.querySelector(".inc-line")!);
    expect(container.querySelector(".inc-detail")?.textContent).not.toMatch(
      /Response snapshot/i,
    );
  });

  it("is a real button, so a keyboard reaches it without a handler", () => {
    const { container } = row();
    const line = container.querySelector(".inc-line")!;
    expect(line.tagName).toBe("BUTTON");
    expect(line.getAttribute("aria-controls")).toBeTruthy();
  });
});

describe("the acknowledge control", () => {
  it("is offered on the row, not behind a detail screen", () => {
    const onAck = vi.fn();
    row({}, { onAck });
    fireEvent.click(screen.getByRole("button", { name: /mute repeat/i }));
    expect(onAck).toHaveBeenCalledWith("1");
  });

  it("promises muting before the click, because ack really does mute", () => {
    /*
     * SUB-81 shipped the escalating repeat ladder and ack stops it, so the
     * label states that consequence rather than the bare verb. "Acknowledge"
     * alone invites the reading "mark this done", which is the one label that
     * could make a still-broken service look handled.
     */
    row({}, { onAck: () => {} });
    const button = screen.getByRole("button", { name: /mute repeat/i });
    expect(button.textContent).toMatch(/mute repeat alerts/i);
    expect(button.getAttribute("title")).toMatch(/stays open/i);
  });

  it("keeps the incident visibly open after acking", () => {
    // The most important requirement of the ticket, asserted on the state the
    // row reports *and* on the words beside the now-disabled control.
    row({ acked: true, ackedAt: T0 + 60_000 }, { onAck: () => {} });
    const button = screen.getByRole("button", { name: /^Repeat alerts muted$/ });
    expect(button.hasAttribute("disabled")).toBe(true);
    expect(document.querySelector(".inc-row")?.getAttribute("data-state")).toBe(
      "acked",
    );
    expect(document.querySelector(".inc-col-ack")?.textContent).toMatch(
      /still down/i,
    );
  });

  it("is not offered on a resolved incident", () => {
    row({ resolved: true, resolvedAt: T0 + 720_000 }, { onAck: () => {} });
    expect(screen.queryByRole("button", { name: /mute/i })).toBeNull();
  });

  it("is absent entirely when the viewer may not write", () => {
    // A button that always answers 403 is worse than no button.
    row({});
    expect(screen.queryByRole("button", { name: /mute/i })).toBeNull();
  });
});

describe("the incidents screen", () => {
  const view = (props: Partial<Parameters<typeof IncidentsView>[0]> = {}) =>
    render(
      <IncidentsView
        incidents={[]}
        now={NOW}
        names={{ "7": "api" }}
        {...props}
      />,
    );

  it("names the monitor, so nobody has to look up an id", () => {
    view({ incidents: [incident()] });
    expect(document.body.textContent).toContain("api");
    expect(document.body.textContent).not.toContain("Monitor 7");
  });

  it("falls back to the id rather than showing a blank subject", () => {
    view({ incidents: [incident({ monitorId: "99" })], names: {} });
    expect(document.body.textContent).toContain("Monitor 99");
  });

  it("lists newest first, which is what you want at 03:00", () => {
    view({
      incidents: [
        incident({ id: "old", startedAt: T0 - 60 * 60_000, monitorId: "7" }),
        incident({ id: "new", startedAt: T0, monitorId: "7" }),
      ],
    });
    const times = [...document.querySelectorAll(".inc-col-time")].map(
      (n) => n.textContent ?? "",
    );
    /*
     * Asserting the order, not just the count.
     *
     * Counting two rows passes just as happily when they render reversed,
     * which makes this test a witness to nothing — and the ordering tests in
     * cluster.test.ts exercise `clusterIncidents`, not what the view does with
     * what it returns.
     */
    expect(times.length).toBe(2);
    const clock = (at: number) =>
      new Date(at).toLocaleTimeString(undefined, {
        hour: "2-digit",
        minute: "2-digit",
      });
    expect(times[0]).toBe(clock(T0));
    expect(times[1]).toBe(clock(T0 - 60 * 60_000));
  });

  it("states the open count and the acked count separately", () => {
    /*
     * An acked incident is still open, so folding it into one number would be
     * the same conflation the rows fight. And the count sits beside the list
     * it is the length of — DESIGN.md §12 records why a fabricated badge is
     * indistinguishable from a real alert on a monitoring tool.
     */
    view({
      incidents: [
        incident(),
        incident({ id: "2", acked: true, ackedAt: T0, monitorId: "7" }),
      ],
    });
    expect(document.body.textContent).toContain("2 open");
    expect(document.body.textContent).toContain("1 acknowledged");
  });

  it("reports a load failure as an alert", () => {
    view({ error: new Error("HTTP 500") });
    expect(document.querySelectorAll('[role="alert"]').length).toBeGreaterThan(
      0,
    );
    expect(document.body.textContent).toContain("HTTP 500");
  });

  it("reports a failed acknowledgement instead of silently doing nothing", () => {
    view({ incidents: [incident()], ackError: new Error("HTTP 403") });
    expect(document.body.textContent).toContain("Could not acknowledge");
    expect(document.body.textContent).toContain("HTTP 403");
  });

  it("stops claiming an outage is ongoing once the stream is dead", () => {
    view({ incidents: [incident()], stale: true });
    const item = document.querySelector(".inc-row")!;
    expect(visibleOnly(item)).not.toContain("Unacked");
    expect(srOnly(item)).toContain("Was down");
  });
});

describe("the empty state is the good news, with its proof", () => {
  it("states the fact at headline weight and quantifies it", () => {
    /*
     * The mockup's shape: the headline is the fact, the helper line is the
     * evidence that the silence was measured rather than the result of a
     * poller that died. No call to action, because there is nothing to do.
     */
    render(<IncidentsView incidents={[]} resolved={[]} now={NOW} monitorCount={6} />);
    expect(document.querySelector(".mon-detail-empty")?.textContent).toMatch(
      /Nothing is broken right now/,
    );
    expect(document.body.textContent).toContain("6 monitors");
  });

  it("offers nothing to click, because there is nothing to do", () => {
    render(<IncidentsView incidents={[]} resolved={[]} now={NOW} monitorCount={6} />);
    expect(document.querySelectorAll("button").length).toBe(0);
  });
});

describe("the resolved history", () => {
/*
 * Pinned to local noon, three days back, and then offset in hours.
 *
 * `groupByDay` keys on the *local* calendar date, so a fixture written as a
 * UTC instant lands on different days in different timezones: T0 is
 * 22:13 UTC, and east of UTC+2 the two incidents split across two local days,
 * breaking the "2 incidents · 20 min total" assertions for anyone running the
 * suite outside Europe. Noon has twelve hours of slack in both directions, so
 * the day is the same everywhere without pinning TZ for the whole run.
 */
const DAY_START = (() => {
  const d = new Date(NOW - 3 * 86_400_000);
  d.setHours(12, 0, 0, 0);
  return d.getTime();
})();

  const resolved = [
    incident({
      id: "r1",
      startedAt: DAY_START + 10_200_000,
      resolved: true,
      resolvedAt: DAY_START + 10_800_000,
      durationS: 600,
    }),
    incident({
      id: "r2",
      startedAt: DAY_START + 3600_000,
      resolved: true,
      resolvedAt: DAY_START + 4200_000,
      durationS: 600,
    }),
  ];

  it("groups by day and totals each day", () => {
    // "2 incidents · 20 min total" is the sentence someone writes in a status
    // update the next morning; adding the rows up is work the screen can do.
    render(
      <IncidentsView
        incidents={[]}
        resolved={resolved}
        now={NOW}
        names={{ "7": "api" }}
      />,
    );
    const head = document.querySelector(".inc-day-head")!;
    expect(head.textContent).toContain("2 incidents");
    expect(head.textContent).toContain("20 min total");
  });

  it("states the window it covers", () => {
    render(
      <IncidentsView
        incidents={[]}
        resolved={resolved}
        now={NOW}
        historyDays={30}
      />,
    );
    expect(document.body.textContent).toContain("Last 30 days");
  });

  it("admits when the history is incomplete rather than looking complete", () => {
    // The API has no instance-wide endpoint for resolved incidents, so this
    // card is assembled per monitor and capped. Showing a partial month as if
    // it were the whole month is the one thing a monitoring tool must not do.
    render(
      <IncidentsView
        incidents={[]}
        resolved={resolved}
        now={NOW}
        historyTruncated
      />,
    );
    expect(document.body.textContent).toMatch(/first monitors only/i);
  });
});

describe("clusters are offered without hiding the incidents", () => {
  const together = [
    incident({ id: "a", monitorId: "7", startedAt: T0 }),
    incident({ id: "b", monitorId: "8", startedAt: T0 - 20_000 }),
    incident({ id: "c", monitorId: "9", startedAt: T0 - 40_000 }),
  ];
  const names = { "7": "api", "8": "auth", "9": "cdn" };

  it("shows the count without the group being opened", () => {
    /*
     * Frank's requirement, and it holds for both readers: the visible badge
     * carries the number, and so does the control's accessible name — so
     * nobody has to expand a disclosure to find out how big it is.
     */
    render(
      <IncidentsView incidents={together} now={NOW} names={names} />,
    );
    const button = document.querySelector(".inc-cluster-line")!;
    expect(button.getAttribute("aria-label")).toContain("3 incidents");
    expect(visibleOnly(button)).toContain("3 incidents");
  });

  it("keeps every incident reachable inside the group", () => {
    render(<IncidentsView incidents={together} now={NOW} names={names} />);
    // Open by default while anything is still broken: during a live incident
    // the facts must not be behind a click.
    expect(document.querySelectorAll(".inc-row").length).toBe(3);
    for (const name of ["api", "auth", "cdn"]) {
      expect(document.body.textContent).toContain(name);
    }
  });

  it("words the grouping as an inference and not as a cause", () => {
    render(<IncidentsView incidents={together} now={NOW} names={names} />);
    const why = document.querySelector(".inc-cluster-why")!.textContent ?? "";
    expect(why).toMatch(/inferring/i);
    expect(why).toMatch(/timing/i);
    expect(why).not.toMatch(/caused by|\brelated\b/i);
  });

  it("tells a screen reader the caveat, not just the headline", () => {
    render(<IncidentsView incidents={together} now={NOW} names={names} />);
    const label =
      document.querySelector(".inc-cluster-line")?.getAttribute("aria-label") ??
      "";
    expect(label).toMatch(/inferring/i);
    expect(label).toMatch(/3 monitors/);
  });

  it("does not group when there is no pattern to state", () => {
    render(
      <IncidentsView
        incidents={[incident({ id: "lonely", startedAt: T0 })]}
        now={NOW}
        names={names}
      />,
    );
    expect(document.querySelector(".inc-cluster")).toBeNull();
    expect(document.querySelectorAll(".inc-row").length).toBe(1);
  });
});

describe("flapping is visible as a pattern, not just as rows", () => {
  const bouncing = Array.from({ length: 4 }, (_, i) =>
    incident({ id: String(i), startedAt: T0 - i * 5 * 60_000 }),
  );

  it("explains the silence on the incidents screen", () => {
    render(
      <IncidentsView incidents={bouncing} now={T0} names={{ "7": "api" }} />,
    );
    expect(document.body.textContent).toContain("4 separate outages");
    expect(document.body.textContent).toMatch(/suppress/i);
    // And it names which monitor, because "something is flapping" is not
    // actionable on a screen listing several.
    expect(document.querySelector(".inc-churn")?.textContent).toContain("api");
  });

  it("explains the same silence on a monitor's own page", () => {
    const monitor: Monitor = {
      id: "7",
      name: "api",
      status: "down",
      target: "https://api.example.com",
      latencyMs: null,
      uptime24h: 90,
      beats: [],
      lastCheck: T0,
      tags: {},
    };
    render(
      <MonitorDetail
        monitor={monitor}
        windows={[]}
        incidents={bouncing}
        now={T0}
        beatWidth={720}
      />,
    );
    expect(document.body.textContent).toContain("4 separate outages");
    expect(document.body.textContent).toMatch(/recorded/i);
  });

  it("stays quiet for a monitor that had one bad moment", () => {
    render(
      <IncidentsView incidents={[incident()]} now={T0} names={{ "7": "api" }} />,
    );
    expect(document.querySelector(".inc-churn")).toBeNull();
  });
});

describe("the disclosure has a name, and the expanded detail keeps the tense", () => {
  /*
   * Both found in review, and both are one mistake wearing two hats: a fact
   * stated correctly in one place and contradicted a few lines away.
   */

  it("names the disclosure with the story instead of leaving it silent", () => {
    /*
     * The sentence used to sit outside the button while the button's own
     * contents were aria-hidden — and `aria-hidden` removes content from the
     * accessible name computation, so the control announced itself as an
     * unnamed button. A keyboard reader was told something could be expanded
     * but not what it was about.
     *
     * Querying by role *and* name is the assertion: this only resolves if the
     * story is inside the button.
     */
    row();
    const button = screen.getByRole("button", { name: /down since|down from/i });
    expect(button.getAttribute("aria-expanded")).toBe("false");
  });

  it("still says it only once", () => {
    // The original aria-hidden existed for a real reason: without it the story
    // is announced twice, once as prose and once as a pile of fragments.
    const { container } = row();
    expect(container.querySelectorAll(".sr-only").length).toBe(1);
    expect(
      container.querySelector(".inc-line-inner")!.getAttribute("aria-hidden"),
    ).toBe("true");
  });

  it("does not claim current status inside an expanded stale row", () => {
    /*
     * SUB-111, one level deeper. The collapsed line said "Was down from ...",
     * and opening it revealed a timeline still asserting "Not recovered" about
     * a stream that had stopped reporting.
     */
    const { container } = row({}, { stale: true });
    fireEvent.click(container.querySelector(".inc-line")!);
    const seen = container.textContent ?? "";
    expect(seen).toMatch(/not recovered when we last heard/i);
  });

  it("keeps the present tense while the stream is alive", () => {
    const { container } = row();
    fireEvent.click(container.querySelector(".inc-line")!);
    const seen = container.textContent ?? "";
    expect(seen).toMatch(/not recovered/i);
    expect(seen).not.toMatch(/when we last heard/i);
  });
});
