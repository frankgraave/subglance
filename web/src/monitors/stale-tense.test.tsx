// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { Dashboard } from "./Dashboard";
import { MonitorDetail } from "./MonitorDetail";
import { MonitorCardList } from "./MonitorCardList";
import { MonitorCompactList } from "./MonitorCompactList";
import { MonitorTable } from "./MonitorTable";
import { STATUS_LABEL, STATUS_LABEL_LAST_KNOWN } from "./format";
import { StatusWall } from "../wall/StatusWall";
import { IncidentsView } from "../incidents/IncidentsView";
import type { Monitor, MonitorStatus } from "./types";

/**
 * SUB-111: no view asserts a status in the present tense once the stream is
 * dead — and every view still lets you read what the status *was*.
 *
 * The defect this file exists to keep dead: `MonitorDetail` printed
 * `STATUS_LABEL[status]` unconditionally, so the page said "Up" for as long as
 * the tab stayed open after SubGlance went deaf. DESIGN.md §6 already drained
 * the colour around that word; the word itself was exempt, which is the wrong
 * way round — a word is a louder claim than a hue.
 *
 * **Why this is one file across five layouts rather than five assertions in
 * five files.** The fix is a rule about the product, not about a component:
 * "when it stops knowing, it stops asserting" has to hold on whichever screen
 * the reader happens to have open. A per-component test would have let the
 * next layout ship without the treatment and stay green.
 *
 * **Why the assertions read the whole text, `sr-only` included.** Outside the
 * detail view the status word is a visually hidden label on the lamp, so a
 * treatment that only dimmed pixels would leave a screen reader hearing "Up"
 * with nothing on screen to contradict it — the same lie, told only to the
 * readers DESIGN.md §2.3 exists for. `visibleOnly` below is used for the
 * opposite direction: proving the word a sighted reader sees also changed.
 *
 * **Why this is jsdom and not a `.browser.test.ts`.** The behaviour under test
 * is *what the markup says*, and markup is exactly what jsdom can read. The
 * CSS half of the treatment — the word receding, the gap taking full ink —
 * genuinely is invisible here, and it is deliberately not asserted: it is a
 * matter of emphasis on top of a fix that must hold with stylesheets disabled,
 * a stylesheet that failed to load, or a reader who overrides colours.
 */

afterEach(cleanup);

const T0 = 1_700_000_000_000;
/** Four minutes after the newest check, so the silence is a real duration. */
const NOW = T0 + 240_000;
const WIDTH = 168;

const ALL: MonitorStatus[] = ["up", "down", "pending", "paused", "waiting"];

/**
 * Names and targets that contain no status word.
 *
 * Load-bearing: a fixture called `api-down` would satisfy "the past-tense
 * word is present" by accident and, worse, would trip the present-tense
 * assertion forever. The same trap `a11y-lists.test.tsx` documents.
 */
const IDS: Record<MonitorStatus, string> = {
  up: "alpha",
  down: "bravo",
  pending: "charlie",
  paused: "delta",
  waiting: "echo",
};

function monitor(status: MonitorStatus): Monitor {
  return {
    id: IDS[status],
    name: `service-${IDS[status]}`,
    status,
    target: `https://${IDS[status]}.example.com`,
    latencyMs: status === "down" ? null : 120,
    uptime24h: 99.5,
    beats: [{ ts: T0, ok: status !== "down", latencyMs: 120 }],
    lastCheck: T0,
    tags: {},
  };
}

const everyStatus = () => ALL.map(monitor);

/** Text a sighted reader can actually see: `sr-only` text is excluded. */
function visibleOnly(el: Element): string {
  const clone = el.cloneNode(true) as Element;
  clone.querySelectorAll(".sr-only, .hb-sr-only").forEach((n) => n.remove());
  return clone.textContent ?? "";
}

/**
 * A present-tense status claim, as a pattern.
 *
 * Anchored on the capital letter, which is why `STATUS_LABEL_LAST_KNOWN`
 * spells its entries "Was up" and not "Was Up": the past-tense phrasing has to
 * be *unmatchable* by this expression, or the guard would flag its own fix.
 * `\b` on the right so "Upstream" in a monitor's name — or "Downtime" in a
 * panel heading — is not read as an assertion.
 */
const PRESENT_TENSE = new RegExp(
  `\\b(${Object.values(STATUS_LABEL).join("|")})\\b`,
  "g",
);

/**
 * Every present-tense status claim in a rendered tree, `sr-only` included.
 *
 * Walks the text nodes one at a time rather than scanning `textContent`, and
 * that is not a style choice: `textContent` welds adjacent elements together,
 * so a monitor named `service-alpha` followed by the word `Up` concatenates to
 * `service-alphaUp` and the word boundary the pattern relies on disappears.
 * An earlier draft of this file scanned the joined string and reported a clean
 * dashboard as having no status words at all — a guard that passes because it
 * can no longer see its subject.
 */
function presentTenseClaims(root: Element): string[] {
  const walker = root.ownerDocument.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const found: string[] = [];
  for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
    for (const match of (node.textContent ?? "").matchAll(PRESENT_TENSE)) {
      found.push(match[0]);
    }
  }
  return found;
}

/**
 * The five layouts, each rendered twice: once live, once with a dead stream.
 *
 * Built as a table so that adding a sixth view without a stale story is a
 * compile error here rather than a silent gap in the guarantee.
 */
const views: {
  name: string;
  render: (stale: boolean) => { container: HTMLElement };
}[] = [
  {
    name: "the detail view",
    render: (stale) =>
      render(
        <MonitorDetail
          monitor={monitor("up")}
          windows={[]}
          incidents={[]}
          now={NOW}
          beatWidth={720}
          stale={stale}
        />,
      ),
  },
  {
    name: "the rows layout",
    render: (stale) =>
      render(
        <MonitorTable
          monitors={everyStatus()}
          beatWidth={WIDTH}
          stale={stale}
        />,
      ),
  },
  {
    name: "the cards layout",
    render: (stale) =>
      render(
        <MonitorCardList
          monitors={everyStatus()}
          beatWidth={WIDTH}
          stale={stale}
        />,
      ),
  },
  {
    name: "the compact layout",
    render: (stale) =>
      render(<MonitorCompactList monitors={everyStatus()} stale={stale} />),
  },
  {
    name: "the status wall",
    render: (stale) =>
      render(<StatusWall monitors={everyStatus()} stale={stale} now={NOW} />),
  },
];

/**
 * The incidents screen, guarded separately.
 *
 * It is the sixth view and it does not fit the table above, because it states
 * an *incident's* state rather than a monitor's: its present-tense claim is
 * "Down now", not "Up", and it has no last-known monitor status to print.
 * The rule is the same one though — when the stream dies, nothing on the page
 * is allowed to assert a state in the present tense — so it gets the same two
 * assertions rather than an exemption.
 */
describe("the incidents screen stops asserting when the stream dies", () => {
  const openIncident = {
    id: "1",
    monitorId: "alpha",
    startedAt: T0,
    confirmedAt: T0 + 60_000,
    resolvedAt: null,
    ackedAt: null,
    confirmed: true,
    resolved: false,
    acked: false,
    durationS: 720,
  };

  const screenAt = (stale: boolean) =>
    render(
      <IncidentsView
        incidents={[openIncident]}
        now={NOW}
        names={{ alpha: "service-alpha" }}
        stale={stale}
      />,
    );

  it("drops the present tense once the stream is dead", () => {
    const { container } = screenAt(true);
    const text = container.textContent ?? "";
    expect(text, "an incidents list still claiming an outage is live").not.toContain(
      "Down since",
    );
    expect(text).not.toContain("and counting");
  });

  it("still says what was happening when we lost contact", () => {
    // The other half, and the half the obvious fix breaks: blanking the row
    // would destroy the most useful thing left on a dead screen.
    const { container } = screenAt(true);
    expect(container.textContent).toContain("Was down");
  });

  it("is unchanged while the stream is alive", () => {
    const { container } = screenAt(false);
    expect(container.textContent).toContain("Down since");
    expect(container.textContent).toContain("and counting");
    expect(container.textContent).not.toContain("Was down");
  });
});

describe("a dead stream leaves no status in the present tense", () => {
  for (const view of views) {
    it(`${view.name} states every status in the past tense`, () => {
      const { container } = view.render(true);
      expect(
        presentTenseClaims(container),
        `${view.name} still claims a status is true right now`,
      ).toEqual([]);
    });

    it(`${view.name} still says what the status was`, () => {
      // The other half of the ticket, and the half the obvious fix breaks:
      // replacing the word with "Status stale" would pass the assertion above
      // and destroy the most useful thing left on a dead screen.
      const { container } = view.render(true);
      const text = container.textContent ?? "";
      // The detail view renders one monitor; the list layouts render all five.
      const shown: MonitorStatus[] =
        view.name === "the detail view" ? ["up"] : ALL;
      for (const status of shown) {
        expect(
          text,
          `${view.name} lost the last known "${status}"`,
        ).toContain(STATUS_LABEL_LAST_KNOWN[status]);
      }
    });

    it(`${view.name} is unchanged while the stream is alive`, () => {
      // The treatment must not leak into the healthy case: a dashboard that
      // says "Was up" on a working connection is a different kind of wrong.
      const { container } = view.render(false);
      const text = container.textContent ?? "";
      for (const phrase of Object.values(STATUS_LABEL_LAST_KNOWN)) {
        expect(text, `${view.name} hedges on a live stream`).not.toContain(
          phrase,
        );
      }
      expect(presentTenseClaims(container).length).toBeGreaterThan(0);
    });
  }
});

describe("the treatment reaches the word a sighted reader sees", () => {
  it("rewrites the detail pill rather than only dimming it", () => {
    // The pill is the one place the status word is visible on every status,
    // and the line the ticket names. A CSS-only treatment would leave this
    // reading "Up" at 60% opacity, which is still a claim about now.
    const { container } = render(
      <MonitorDetail
        monitor={monitor("up")}
        windows={[]}
        incidents={[]}
        now={NOW}
        beatWidth={720}
        stale
      />,
    );
    const pill = container.querySelector(".mon-detail-status")!;
    expect(visibleOnly(pill)).toContain("Was up");
    expect(visibleOnly(pill)).not.toMatch(/\bUp\b/);
  });

  it("rewrites the visible word on a card, not just the hidden one", () => {
    // `MonitorCard` prints the lamp's label visibly — it has no column header
    // to lend the lamp meaning — so both copies of the word are on screen.
    const { container } = render(
      <MonitorCardList monitors={[monitor("down")]} beatWidth={WIDTH} stale />,
    );
    const card = container.querySelector(".mon-card")!;
    expect(visibleOnly(card)).toContain("Was down");
  });

  it("rewrites the row's visible word for statuses that print one", () => {
    const { container } = render(
      <MonitorTable monitors={[monitor("down")]} beatWidth={WIDTH} stale />,
    );
    const row = container.querySelector(".mon-row")!;
    expect(visibleOnly(row)).toContain("Was down");
  });
});

describe("the word the lamp whispers", () => {
  /*
   * The half a stylesheet cannot reach, and the reason the fix is markup.
   *
   * Every list layout hides the `up` label as `sr-only` — deliberately, so
   * 190 healthy rows do not print the same word 190 times. `connection.css`
   * desaturates the lamp beside it, and no CSS rule can touch text. Without
   * `Led` taking `stale`, a screen reader on a dead dashboard would still be
   * told "Up", with the visual withdrawal it cannot see as the only warning.
   */
  it("moves the row's hidden up label into the past tense too", () => {
    const { container } = render(
      <MonitorTable monitors={[monitor("up")]} beatWidth={WIDTH} stale />,
    );
    const row = container.querySelector(".mon-row")!;
    // Hidden from the eye, present for the ear — and past tense for both.
    expect(visibleOnly(row)).not.toMatch(/\bUp\b/);
    expect(row.textContent).toContain("Was up");
  });

  it("moves the compact line's hidden up label into the past tense too", () => {
    const { container } = render(
      <MonitorCompactList monitors={[monitor("up")]} stale />,
    );
    const line = container.querySelector(".mon-line")!;
    expect(visibleOnly(line)).not.toMatch(/\bUp\b/);
    expect(line.textContent).toContain("Was up");
  });

  it("moves the wall card's hidden label into the past tense too", () => {
    // The wall has no room for a word per card at reading distance, so its
    // label is sr-only for every status — this layout is entirely the case
    // that a CSS-only treatment misses.
    const { container } = render(
      <StatusWall monitors={[monitor("up")]} stale now={NOW} />,
    );
    const card = container.querySelector(".wall-card")!;
    expect(card.textContent).toContain("Was up");
    expect(card.textContent).not.toMatch(/\bUp\b/);
  });
});

describe("the dashboard hands the stream's state to whichever layout is up", () => {
  /*
   * The plumbing test. `LiveDashboard` already passes `stale` to `Dashboard`,
   * and `Dashboard` already sets `data-conn` — but the attribute alone was the
   * whole of the old treatment, and a `Dashboard` that set it without telling
   * the list would put a warning banner above a hundred present-tense lies.
   */
  for (const layout of ["rows", "cards", "compact"] as const) {
    it(`reaches the ${layout} layout`, () => {
      const { container } = render(
        <Dashboard
          monitors={everyStatus()}
          query=""
          onQueryChange={() => {}}
          beatWidth={WIDTH}
          layout={layout}
          stale
        />,
      );
      // Scoped past the toolbar: the status filter chips are labelled "up",
      // "down" and so on in lower case, which is a control naming a filter
      // rather than a claim about a monitor — and the guard is anchored on
      // the capital letter precisely so the two do not collide.
      expect(presentTenseClaims(container)).toEqual([]);
      expect(container.textContent).toContain("Was up");
    });
  }

  it("says the present tense on every layout while the stream is alive", () => {
    for (const layout of ["rows", "cards", "compact"] as const) {
      const { container } = render(
        <Dashboard
          monitors={everyStatus()}
          query=""
          onQueryChange={() => {}}
          beatWidth={WIDTH}
          layout={layout}
        />,
      );
      expect(
        presentTenseClaims(container).length,
        `${layout} lost its status words`,
      ).toBeGreaterThan(0);
      cleanup();
    }
  });
});

describe("the detail pill states the silence, not the reading's age", () => {
  const pill = (stale: boolean) => {
    const { container } = render(
      <MonitorDetail
        monitor={monitor("up")}
        windows={[]}
        incidents={[]}
        now={NOW}
        beatWidth={720}
        stale={stale}
      />,
    );
    return container.querySelector(".mon-detail-status")!;
  };

  it("counts how long we have been blind once the stream is dead", () => {
    // "checked 4 min ago" is a statement about then and stays true forever;
    // "no data for 4 min" is a statement about now, and it grows. On a dead
    // stream the growing one is the fact that decides what to do next.
    expect(pill(true).textContent).toContain("no data for 4 min");
    expect(pill(true).textContent).not.toContain("checked 4 min ago");
  });

  it("dates the reading normally while the stream is alive", () => {
    expect(pill(false).textContent).toContain("checked 4 min ago");
    expect(pill(false).textContent).not.toContain("no data for");
  });

  it("moves the emphasis off the status and onto the silence", () => {
    /*
     * The other half of the treatment, and it is markup rather than a class
     * for a reason worth a test: `<strong>` means "this matters now". On a
     * live stream that is the status. Once the stream is dead the status is
     * history and the fact that decides what anyone does next is how long we
     * have been blind, so the element moves with the meaning. Stated in the
     * markup, it survives a dropped stylesheet and a reader who overrides
     * colours — which a CSS-only emphasis swap does not.
     */
    const live = pill(false).querySelector("strong")!;
    expect(live.textContent).toBe("Up");

    const stale = pill(true).querySelector("strong")!;
    expect(stale.textContent).toContain("no data for");
    // And the status word specifically is no longer the emphasised part.
    expect(stale.textContent).not.toContain("Was up");
  });
});

describe("one vocabulary, so a view cannot forget the rule", () => {
  it("gives every status a past-tense phrasing", () => {
    // A status added to `STATUS_LABEL` without one here would render
    // `undefined` on a dead stream. Guarded rather than trusted, because the
    // failure only appears on a screen nobody is watching when it appears.
    for (const status of ALL) {
      expect(STATUS_LABEL_LAST_KNOWN[status], status).toBeTruthy();
    }
    expect(Object.keys(STATUS_LABEL_LAST_KNOWN).sort()).toEqual(
      Object.keys(STATUS_LABEL).sort(),
    );
  });

  it("phrases every past-tense label so it cannot read as a present claim", () => {
    // The lower-case status word is what makes the guard above writable.
    for (const status of ALL) {
      expect(
        STATUS_LABEL_LAST_KNOWN[status],
        `"${STATUS_LABEL_LAST_KNOWN[status]}" would trip the present-tense guard`,
      ).not.toMatch(PRESENT_TENSE);
    }
  });

  it("leaves no second copy of the word list for a view to read from", () => {
    /*
     * `ledState.ts` used to hold a `LED_LABELS` table that duplicated
     * `STATUS_LABEL` character for character, and `Led` read from it. A
     * duplicated word list is how one surface ends up with a rule the other
     * has not heard of: the tense lives in `format.ts`, so a lamp reading its
     * own copy would have gone on whispering "Up" to a screen reader long
     * after the page stopped saying it out loud.
     *
     * Asserted against the source text because the failure is the *existence*
     * of the second table, which no runtime check can see once nothing
     * imports it. Resolved from the vitest root the way `Led.test.tsx`
     * resolves `led.css`.
     */
    const source = readFileSync(
      join(process.cwd(), "src/monitors/ledState.ts"),
      "utf8",
    );
    // Comments stripped first: the note left at the table's old address
    // quotes the word "Up" while explaining why it is no longer spelled
    // there, and a check that cannot tell an explanation from a declaration
    // would forbid the documentation of its own rule.
    const code = source
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/\/\/.*$/gm, "");
    expect(code).not.toMatch(/LED_LABELS/);
    for (const word of Object.values(STATUS_LABEL)) {
      expect(code, `ledState.ts re-spells "${word}"`).not.toContain(`"${word}"`);
    }
  });
});

describe("a screen reader is told the stream died, not just the monitors", () => {
  it("keeps the connection badge's own warning intact", () => {
    // The tense change is an addition to the banner, never a replacement for
    // it: the banner is what says *why* every word on the page is hedged, and
    // it carries the reconnect control.
    render(
      <Dashboard
        monitors={everyStatus()}
        query=""
        onQueryChange={() => {}}
        beatWidth={WIDTH}
        stale
        banner={<p>Connection lost — reconnecting</p>}
      />,
    );
    expect(screen.getByText(/connection lost/i)).toBeTruthy();
  });
});
