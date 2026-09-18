/**
 * The expanded incident row, measured in a real browser.
 *
 * Every claim in this file is about a *rendered size or position*, which is
 * precisely what jsdom cannot answer: it lays nothing out, reports every width
 * as zero and every `getBoundingClientRect()` as the origin. The three defects
 * this ticket fixed were all invisible to the unit suite for that reason — a
 * time column 2.45px too narrow, a grid splitting itself into equal halves
 * neither occupant wanted, and an action bar that never moved when the row
 * opened. A unit test asserting the class names would have passed throughout.
 *
 * The harness serves per-monitor incidents but not `GET /api/v1/incidents`,
 * which is the request the Incidents screen makes. Rather than change the
 * harness, the fixture is injected by intercepting that one route — which also
 * pins the *content*: the longest failure kind, a timeline whose steps are
 * whole sentences, and a captured error long enough to wrap.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

let server: Server;
let browser: Browser;

const NOW = Date.now();
const ago = (ms: number) => new Date(NOW - ms).toISOString();

/**
 * Three open incidents, pinned to the worst content each field can hold.
 *
 * `01:59 PM`-shaped clocks rather than a 24-hour reading, because the screen
 * renders a 12-hour clock and the meridiem suffix is what overflowed the
 * column. A duration in the `7 h 24 min` shape for the same reason: it is the
 * widest form `formatDuration` produces below a day.
 */
const OPEN_INCIDENTS = {
  incidents: [
    {
      id: 42,
      monitor_id: 1,
      started_at: ago(7 * 3_600_000 + 5 * 60_000),
      confirmed_at: ago(7 * 3_600_000 + 4 * 60_000),
      confirmed: true,
      resolved: false,
      acked: false,
      duration_s: 7 * 3600 + 5 * 60,
      cause: "Connection refused",
      last_error:
        "request failed: tls: server selected unsupported protocol version 301",
    },
    {
      id: 43,
      monitor_id: 3,
      started_at: ago(7 * 3_600_000 + 24 * 60_000),
      confirmed_at: ago(7 * 3_600_000 + 23 * 60_000),
      confirmed: true,
      resolved: false,
      acked: false,
      duration_s: 7 * 3600 + 24 * 60,
      cause: "TLS failure",
      last_error:
        "TLS certificate verification failed: x509: certificate signed by unknown authority",
    },
  ],
};

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/**
 * The Incidents screen with the first row expanded, in one theme at one width.
 *
 * The theme is seeded into `localStorage` before the app is loaded, not
 * toggled afterwards: the owner reviews this screen in dark, and a check run
 * only in light is a check of half the product. The two themes swap every
 * surface and every ink, so "the tray is distinguishable from the row" is a
 * different measurement in each.
 */
async function openExpanded(theme: "dark" | "light", width: number): Promise<Page> {
  const page = await browser.newPage();
  await page.setRequestInterception(true);
  page.on("request", (req) => {
    if (new URL(req.url()).pathname === "/api/v1/incidents") {
      void req.respond({
        status: 200,
        contentType: "application/json; charset=utf-8",
        body: JSON.stringify(OPEN_INCIDENTS),
      });
      return;
    }
    void req.continue();
  });
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + "/blank-for-storage", { waitUntil: "domcontentloaded" });
  await page.evaluate(
    (key: string, value: string) => window.localStorage.setItem(key, value),
    THEME_STORAGE_KEY,
    theme,
  );
  await page.goto(server.url + "/incidents", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".inc-row", { timeout: 15_000 });
  await page.click(".inc-row .inc-line");
  await page.waitForSelector(".inc-detail", { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  return page;
}

/** Every timeline time cell in the open row, with the line count it rendered. */
const TIMES = `(() => {
  const row = document.querySelector(".inc-row");
  return Array.from(row.querySelectorAll(".inc-tl-at")).map((at) => {
    const box = at.getBoundingClientRect();
    const lead = parseFloat(window.getComputedStyle(at).lineHeight);
    return {
      text: (at.textContent || "").trim(),
      width: Math.round(box.width),
      lines: Math.round(box.height / lead),
      overflows: at.scrollWidth > at.clientWidth + 1,
    };
  });
})()`;

describe("the expanded incident row", () => {
  for (const theme of ["dark", "light"] as const) {
    it(`keeps every timeline time on one line, so they form a column (${theme})`, async () => {
      const page = await openExpanded(theme, 1440);
      try {
        const times = (await page.evaluate(TIMES)) as {
          text: string;
          width: number;
          lines: number;
          overflows: boolean;
        }[];
        expect(times.length, "the fixture must render a timeline").toBeGreaterThan(2);
        // At least one real clock reading, or this measures only em dashes.
        expect(times.some((t) => /\d/.test(t.text))).toBe(true);
        for (const time of times) {
          expect(
            time.lines,
            `timeline time "${time.text}" wrapped onto ${time.lines} lines in ${time.width}px; ` +
              `a wrapped time destroys the column the fixed width exists to create`,
          ).toBe(1);
          expect(time.overflows, `timeline time "${time.text}" overflowed its cell`).toBe(
            false,
          );
        }
      } finally {
        await page.close();
      }
    }, 60_000);

    it(`gives the captured error more room than the timeline it cannot shrink (${theme})`, async () => {
      const page = await openExpanded(theme, 1440);
      try {
        const layout = (await page.evaluate(`(() => {
          const row = document.querySelector(".inc-row");
          const cols = Array.from(row.querySelectorAll(".inc-detail-col"));
          const box = (el) => {
            const r = el.getBoundingClientRect();
            return { x: Math.round(r.x), width: Math.round(r.width), right: Math.round(r.right) };
          };
          const grid = row.querySelector(".inc-detail-grid");
          return {
            columns: cols.map(box),
            grid: box(grid),
            timelineNeeds: Math.ceil(
              Math.max(...Array.from(row.querySelectorAll(".inc-tl-step")).map(
                (s) => s.scrollWidth,
              )),
            ),
          };
        })()`)) as {
          columns: { x: number; width: number; right: number }[];
          grid: { x: number; width: number; right: number };
          timelineNeeds: number;
        };

        expect(layout.columns.length, "the detail must have two sections").toBe(2);
        const [timeline, error] = layout.columns;

        // The defect: two equal halves, 398px each at 1440, which starved the
        // elastic column and over-served the rigid one.
        expect(
          error.width > timeline.width,
          `the error column (${error.width}px) must be wider than the timeline ` +
            `(${timeline.width}px): the timeline stops growing at its content ` +
            `(${layout.timelineNeeds}px) while the error string wraps for every pixel it is denied`,
        ).toBe(true);

        // And the pair must actually fill the panel, which is the "empty space
        // on the right" the owner saw stated as a measurement.
        const slack = layout.grid.right - error.right;
        expect(
          slack,
          `${slack}px of the detail panel was left unpainted on the right; the ` +
            `elastic column must consume the remainder`,
        ).toBeLessThanOrEqual(2);
      } finally {
        await page.close();
      }
    }, 60_000);

    it(`moves the mute control below the detail, so open and closed differ (${theme})`, async () => {
      const page = await openExpanded(theme, 1440);
      try {
        const geometry = (await page.evaluate(`(() => {
          const rows = Array.from(document.querySelectorAll(".inc-row"));
          const open = rows.find((r) => r.dataset.open === "true");
          const closed = rows.find((r) => r.dataset.open !== "true");
          const offset = (row) => {
            if (!row) return null;
            const act = row.querySelector(".inc-act");
            const detail = row.querySelector(".inc-detail");
            if (!act) return null;
            const r = row.getBoundingClientRect();
            const a = act.getBoundingClientRect();
            return {
              fromTop: Math.round(a.top - r.top),
              belowDetail: detail
                ? Math.round(a.top) >= Math.round(detail.getBoundingClientRect().bottom) - 1
                : null,
              fill: window.getComputedStyle(act).backgroundColor,
            };
          };
          return { open: offset(open), closed: offset(closed) };
        })()`)) as {
          open: { fromTop: number; belowDetail: boolean | null; fill: string } | null;
          closed: { fromTop: number; belowDetail: boolean | null; fill: string } | null;
        };

        expect(geometry.open, "an open row with an action bar").not.toBeNull();
        expect(geometry.closed, "a closed row with an action bar").not.toBeNull();
        expect(
          geometry.open!.belowDetail,
          "the mute control must sit below the expanded detail, not above it",
        ).toBe(true);
        // The defect in one number: identical offsets meant the two states drew
        // the control in the same place and did not read as different states.
        expect(
          geometry.open!.fromTop,
          `the mute control sits ${geometry.open!.fromTop}px into an open row and ` +
            `${geometry.closed!.fromTop}px into a closed one; identical offsets are ` +
            `why the expanded and collapsed states did not read as different`,
        ).toBeGreaterThan(geometry.closed!.fromTop);
        expect(
          geometry.open!.fill,
          "the open row's action bar must be anchored on a fill, not floating",
        ).not.toBe("rgba(0, 0, 0, 0)");
      } finally {
        await page.close();
      }
    }, 60_000);

    it(`marks an open incident with an edge and no panel-wide fill (${theme})`, async () => {
      const page = await openExpanded(theme, 1440);
      try {
        const paint = (await page.evaluate(`(() => {
          const row = document.querySelector('.inc-row[data-state="open"]');
          const rail = window.getComputedStyle(row, "::before");
          const detail = row.querySelector(".inc-detail");
          /*
           * A token read off :root is authored form (\`#2c1017\`) while
           * getComputedStyle reports \`rgb(44, 16, 23)\`. Comparing the two
           * directly can never fail, which is not a passing test — it is no
           * test. Painting the token onto a throwaway element and reading it
           * back puts both sides in the engine's own form.
           */
          const resolve = (token) => {
            const probe = document.createElement("div");
            probe.style.backgroundColor = "var(" + token + ")";
            document.body.appendChild(probe);
            const value = window.getComputedStyle(probe).backgroundColor;
            probe.remove();
            return value;
          };
          return {
            rowFill: window.getComputedStyle(row).backgroundColor,
            detailFill: window.getComputedStyle(detail).backgroundColor,
            railWidth: Math.round(parseFloat(rail.width)),
            railFill: rail.backgroundColor,
            downFill: resolve("--down"),
            dimFill: resolve("--down-dim"),
            words: Array.from(row.querySelectorAll(".chip, .sr-only"))
              .map((el) => (el.textContent || "").trim())
              .filter(Boolean),
            visibleStatus:
              (row.querySelector(".inc-col-ack")?.textContent || "").trim(),
            detailArea: (() => {
              const r = detail.getBoundingClientRect();
              return Math.round(r.width * r.height);
            })(),
          };
        })()`)) as Record<string, any>;

        // The tokens this test names must actually exist.
        //
        // `resolve()` paints `var(--token)` onto a probe and reads it back, so
        // a token that has been deleted resolves to `rgba(0, 0, 0, 0)` rather
        // than throwing — and every assertion below is `not.toBe(fill)`, which
        // a transparent fill passes trivially. The check would go on reporting
        // green while measuring nothing.
        //
        // The repo's guard cannot cover this: `tokens.test.ts` walks web/src
        // but its `sourceFiles()` skips anything matching `.test.`, so a
        // `var()` naming a dead token inside a test file is invisible to it.
        //
        // `--down-deep` was on this list and has been removed from it, which
        // is the outcome the note above predicted: SUB-140 took the resting
        // red fill off the monitor rows, that was its last caller, and the
        // token went with it. This is the update that note asked for rather
        // than a deletion — `--down-dim` is still live (the icon tile and the
        // open incident row both fill with it), so the contract this test
        // exists to hold is unchanged and still measured against a real
        // colour.
        for (const [name, fill] of [["--down-dim", paint.dimFill]] as const) {
          expect(
            fill,
            `${name} resolved to nothing, so it no longer exists. The ` +
              `assertions below compare the row's fill against it and would ` +
              `pass against any colour at all — this test must be updated, ` +
              `not deleted, or the flat status fill can come back unnoticed`,
          ).not.toBe("rgba(0, 0, 0, 0)");
        }

        // The row no longer paints its status across itself and its detail.
        for (const [name, fill] of [["--down-dim", paint.dimFill]] as const) {
          expect(
            paint.rowFill,
            `an open row must not fill itself with ${name}: that fill covers the ` +
              `detail too, painting ${paint.detailArea}px² of flat status colour ` +
              `for as long as the row stays open`,
          ).not.toBe(fill);
          expect(
            paint.detailFill,
            `the expanded detail must not carry ${name} either`,
          ).not.toBe(fill);
        }

        // It marks itself with an edge instead, and the edge is real.
        //
        // Exactly 2px, not "at least". `>= 2` also admits the 12px slab this
        // ticket was opened to remove — the fill came back as a wide rail and
        // the assertion still passed. The width is `--outline-w`, which is the
        // 2px rung, so the exact number is the documented one.
        expect(
          paint.railWidth,
          `the status rail rendered ${paint.railWidth}px; an open incident ` +
            `carries the documented 2px edge, and a wider one is the panel ` +
            `fill returning under another name`,
        ).toBe(2);
        expect(
          paint.railFill,
          "the status rail must be painted in the down colour",
        ).toBe(paint.downFill);

        // DESIGN.md §9: never colour alone. The row says its state in words
        // whether or not the rail is seen.
        //
        // The visible column is asserted by its exact word, not by a pattern
        // over every chip and `.sr-only` string in the row. The broad form
        // could be satisfied entirely by text the reader never sees, or by the
        // failure chip's "Connection refused" matching /refused/ — so it would
        // have passed with the response column empty, which is the one place
        // this row states whether anybody is on it.
        expect(
          paint.visibleStatus,
          "the open row's response column must say its state in a visible " +
            "word; colour is not allowed to be the only signal",
        ).toBe("Unacked");
        expect(
          paint.words.some((w: string) => /unacked|acked|down|refused|failure/i.test(w)),
          `the open row must state its condition in words as well as colour; found ${JSON.stringify(
            paint.words,
          )}`,
        ).toBe(true);

        // And the detail is a recess, so expanded is legible without colour.
        expect(
          paint.detailFill,
          "the expanded detail must carry its own neutral fill, or an open row " +
            "differs from a closed one only in height",
        ).not.toBe("rgba(0, 0, 0, 0)");
      } finally {
        await page.close();
      }
    }, 60_000);
  }

  it("stacks the detail rather than rationing the error at tablet width", async () => {
    const page = await openExpanded("dark", 900);
    try {
      const stacked = (await page.evaluate(`(() => {
        const row = document.querySelector(".inc-row");
        const cols = Array.from(row.querySelectorAll(".inc-detail-col"));
        const boxes = cols.map((c) => c.getBoundingClientRect());
        const snap = row.querySelector(".inc-snap");
        return {
          sameLeft: Math.abs(boxes[0].x - boxes[1].x) <= 1,
          errorWidth: Math.round(snap.getBoundingClientRect().width),
          gridWidth: Math.round(row.querySelector(".inc-detail-grid").getBoundingClientRect().width),
          documentOverflows: document.documentElement.scrollWidth > window.innerWidth + 1,
        };
      })()`)) as {
        sameLeft: boolean;
        errorWidth: number;
        gridWidth: number;
        documentOverflows: boolean;
      };

      expect(
        stacked.sameLeft,
        "below the tablet rung the two detail sections must share a left edge, " +
          "or the timeline keeps its 388px measure and rations the error",
      ).toBe(true);
      expect(
        stacked.errorWidth,
        `the captured error had ${stacked.errorWidth}px of ${stacked.gridWidth}px; ` +
          `stacked, it must have essentially all of it`,
      ).toBeGreaterThanOrEqual(stacked.gridWidth - 2);
      expect(
        stacked.documentOverflows,
        "an expanded row must never push the document sideways",
      ).toBe(false);
    } finally {
      await page.close();
    }
  }, 60_000);

  it("keeps the monitor name present once the columns widen", async () => {
    // The cost of moving time and duration to rung 3: at 900px the four fixed
    // columns plus their gaps exceed the card, and the name was squeezed to
    // zero. The widths are released below the tablet rung; this is what proves
    // the release actually happens.
    const page = await openExpanded("dark", 900);
    try {
      const name = (await page.evaluate(`(() => {
        const row = document.querySelector(".inc-row");
        const el = row.querySelector(".inc-name");
        const inner = row.querySelector(".inc-line-inner");
        const r = el.getBoundingClientRect();
        const c = inner.getBoundingClientRect();
        return {
          width: Math.round(r.width),
          text: (el.textContent || "").trim(),
          insideRow: Math.round(r.right) <= Math.round(c.right) + 1,
        };
      })()`)) as { width: number; text: string; insideRow: boolean };

      expect(name.text.length, "the row must name its monitor").toBeGreaterThan(0);
      expect(
        name.width,
        `the monitor name rendered ${name.width}px wide at 900px: the fixed ` +
          `columns consumed the line and left the name nothing`,
      ).toBeGreaterThanOrEqual(80);
      expect(name.insideRow, "the name must not overflow its line").toBe(true);
    } finally {
      await page.close();
    }
  }, 60_000);

  it("drains the status rail once the stream goes stale", async () => {
    /*
     * The rail is a status colour, so §6 applies to it: when the stream dies
     * the screen stops asserting. Everything else in the row already withdrew
     * — the lamp desaturates, the numbers drop to `--ink-3`, the badge word
     * becomes "Was unacked" — and the rail was sitting through all of it at
     * full strength.
     *
     * `data-conn` is set on the container here rather than by killing the
     * stream, because the binding is already covered in the unit suite
     * (`IncidentsView` writes the attribute) and what is unproven is the CSS:
     * a rule reaching a pseudo-element through two attribute selectors is
     * exactly the kind that silently matches nothing.
     */
    const page = await openExpanded("dark", 1440);
    try {
      const drain = (await page.evaluate(`(async () => {
        const screen = document.querySelector(".mon-detail");
        const row = document.querySelector('.inc-row[data-state="open"]');
        const railFilter = () =>
          window.getComputedStyle(row, "::before").filter;
        const live = railFilter();
        screen.setAttribute("data-conn", "stale");
        /*
         * The drain is a 600ms transition, so the computed filter read on the
         * next tick is its *starting* value, not its settled one. Waiting is
         * not padding here: without it both reads return saturate(1) — the
         * rail undrained and the lamp undrained — and "the rail matches the
         * lamp" is satisfied by the two of them being equally unchanged,
         * which is the assertion passing for the wrong reason.
         */
        await new Promise((r) => setTimeout(r, 750));
        const stale = railFilter();
        const lamp = window.getComputedStyle(row.querySelector(".led")).filter;
        screen.setAttribute("data-conn", "live");
        return { live, stale, lamp, railWidth: Math.round(parseFloat(
          window.getComputedStyle(row, "::before").width)) };
      })()`)) as {
        live: string;
        stale: string;
        lamp: string;
        railWidth: number;
      };

      expect(
        drain.live,
        "a live rail asserts at full strength; that is the point of it",
      ).toBe("none");
      expect(
        drain.stale,
        `the rail kept filter "${drain.stale}" after the stream went stale; a ` +
          `saturated status colour is a claim about now, and we have stopped ` +
          `knowing (DESIGN.md §6)`,
      ).not.toBe("none");
      // Settled, not merely changed: `saturate(1)` is the transition's first
      // frame and means nothing has drained yet.
      expect(
        drain.stale,
        `the rail settled at "${drain.stale}"; the drained state is the 18% ` +
          `the lamps take, and anything at full saturation is still asserting`,
      ).not.toBe("saturate(1)");
      expect(
        drain.stale,
        "the rail must drain by the same amount as the lamp beside it, or the " +
          "row withdraws in two stages",
      ).toBe(drain.lamp);
      // Drained, not deleted: the last known state is still the most useful
      // thing on the screen, and §6's method is that nothing is hidden.
      expect(
        drain.railWidth,
        "a stale rail must still be drawn; removing it would delete the last " +
          "known state rather than stop asserting it",
      ).toBe(2);
    } finally {
      await page.close();
    }
  }, 60_000);

  it("keeps the focus ring on the disclosure inside the clipping row", async () => {
    /*
     * `.inc-row` takes `overflow: hidden` in this PR, to cut the status rail
     * and the detail tray to its rounded corners. `.inc-line` fills the row
     * edge to edge, and the global ring in `index.css` is drawn 2px *outside*
     * the control — so the row clips the left and right sides of the ring on
     * the one control that opens it, leaving a keyboard user a ring with no
     * sides. Measured, not inferred: the outward ring lands at 262–1110 while
     * the row clips at 263–1109.
     *
     * The ring is driven with a real Tab press. `HTMLElement.focus()` sets
     * `:focus` but not `:focus-visible`, so the rule under test would not
     * match at all and the check would measure an unstyled button.
     */
    const page = await openExpanded("dark", 1440);
    try {
      await page.evaluate(
        `document.querySelector('.inc-row[data-state="open"] .inc-line').focus()`,
      );
      await page.keyboard.down("Shift");
      await page.keyboard.press("Tab");
      await page.keyboard.up("Shift");
      await page.keyboard.press("Tab");

      const ring = (await page.evaluate(`(() => {
        const line = document.activeElement;
        const row = line.closest(".inc-row");
        const cs = window.getComputedStyle(line);
        const rowBox = row.getBoundingClientRect();
        const lineBox = line.getBoundingClientRect();
        const offset = parseFloat(cs.outlineOffset);
        const width = parseFloat(cs.outlineWidth);
        return {
          focused: line.className,
          keyboardRing: line.matches(":focus-visible"),
          width,
          offset,
          // Where the ring's outer edge actually lands, against the box that
          // clips it.
          ringLeft: Math.round(lineBox.left - offset - width),
          ringRight: Math.round(lineBox.right + offset + width),
          clipLeft: Math.round(rowBox.left),
          clipRight: Math.round(rowBox.right),
          rowClips: window.getComputedStyle(row).overflow === "hidden",
        };
      })()`)) as Record<string, any>;

      expect(ring.focused, "Tab must land on the disclosure").toBe("inc-line");
      expect(
        ring.keyboardRing,
        "the ring must be the keyboard one, or this measures an unstyled button",
      ).toBe(true);
      expect(
        ring.rowClips,
        "this check exists because the row clips; if it stopped clipping the " +
          "inward offset is no longer needed and this test should be revisited",
      ).toBe(true);
      expect(
        ring.width,
        "the disclosure keeps a 2px ring; it must be turned inward, not removed",
      ).toBe(2);
      expect(
        ring.ringLeft,
        `the focus ring's left edge is at ${ring.ringLeft} and the row clips ` +
          `at ${ring.clipLeft}; an outward ring on a clipping row loses its sides`,
      ).toBeGreaterThanOrEqual(ring.clipLeft);
      expect(
        ring.ringRight,
        `the focus ring's right edge is at ${ring.ringRight} and the row clips ` +
          `at ${ring.clipRight}`,
      ).toBeLessThanOrEqual(ring.clipRight);
    } finally {
      await page.close();
    }
  }, 60_000);

  it("keeps the expanded row inside a phone viewport", async () => {
    /*
     * The `@media (max-width: 640px)` branch had no browser coverage at all.
     * The suite measured 1440 and 900, and 900 is above the phone rung — so
     * the stacked `.inc-line-inner` and the wrapped columns were asserted by
     * nothing, on the layout most likely to overflow. A page that scrolls
     * sideways is the bug this file's own comment says the product already
     * learned once.
     */
    const page = await openExpanded("dark", 375);
    try {
      const phone = (await page.evaluate(`(() => {
        const row = document.querySelector(".inc-row");
        const rowBox = row.getBoundingClientRect();
        const within = (el) => {
          const r = el.getBoundingClientRect();
          return Math.round(r.left) >= Math.round(rowBox.left) - 1 &&
            Math.round(r.right) <= Math.round(rowBox.right) + 1;
        };
        const detail = row.querySelector(".inc-detail");
        const parts = Array.from(
          row.querySelectorAll(".inc-detail-col, .inc-snap, .inc-tl, .inc-line-inner"),
        );
        return {
          documentOverflows:
            document.documentElement.scrollWidth > window.innerWidth + 1,
          rowWithinViewport: Math.round(rowBox.right) <= window.innerWidth + 1,
          detailWithinRow: within(detail),
          escaping: parts
            .filter((el) => !within(el))
            .map((el) => el.className + " @ " + Math.round(el.getBoundingClientRect().right)),
          detailWidth: Math.round(detail.getBoundingClientRect().width),
          errorLines: (() => {
            const snap = row.querySelector(".inc-snap");
            const lead = parseFloat(window.getComputedStyle(snap).lineHeight);
            return Math.round(snap.getBoundingClientRect().height / lead);
          })(),
        };
      })()`)) as {
        documentOverflows: boolean;
        rowWithinViewport: boolean;
        detailWithinRow: boolean;
        escaping: string[];
        detailWidth: number;
        errorLines: number;
      };

      expect(
        phone.documentOverflows,
        "an expanded row must never push the document sideways on a phone",
      ).toBe(false);
      expect(
        phone.rowWithinViewport,
        "the incident row itself must fit the phone viewport",
      ).toBe(true);
      expect(
        phone.escaping,
        `these parts of the expanded detail rendered outside the row at 375px: ` +
          `${JSON.stringify(phone.escaping)}`,
      ).toEqual([]);
      expect(phone.detailWithinRow, "the detail must stay inside its row").toBe(true);
      // And it is still worth opening: a detail squeezed to nothing is inside
      // the row too, which is why the bound above is not the whole assertion.
      expect(
        phone.detailWidth,
        `the expanded detail was ${phone.detailWidth}px wide at a 375px ` +
          `viewport; readable content, not merely contained content`,
      ).toBeGreaterThanOrEqual(280);
      expect(
        phone.errorLines,
        "the captured error must still wrap rather than be clipped to one line",
      ).toBeGreaterThan(1);
    } finally {
      await page.close();
    }
  }, 60_000);

  it("costs a collapsed row no more height than its own line", async () => {
    /*
     * SUB-141 in one measurement.
     *
     * The mute control used to be a block in the row's column, so it claimed a
     * strip under the line: measured at 1440, a collapsed row was 108px tall
     * to carry a 60px line, 46px per incident spent on one button. The control
     * is on the line now, so the row is as tall as the line and nothing else.
     *
     * The bound is the *line* rather than a pixel count, deliberately. A
     * literal `62` would fail the day the row's padding or type rung moved,
     * for a reason that has nothing to do with this ticket; "the row is its
     * line plus its border" stays true through both and is the actual claim.
     *
     * Checked at every width the suite knows, because the phone branch wraps
     * the line to three decks and the tablet branch releases the columns —
     * either could have put the control back on a line of its own without the
     * desktop measurement noticing.
     */
    for (const width of [1440, 1280, 900, 375]) {
      const page = await openExpanded("dark", width);
      try {
        const rows = (await page.evaluate(`(() => {
          const closed = Array.from(document.querySelectorAll(".inc-row"))
            .filter((r) => r.dataset.open !== "true");
          return closed.map((row) => {
            const line = row.querySelector(".inc-line");
            const act = row.querySelector(".inc-act");
            const r = row.getBoundingClientRect();
            const l = line.getBoundingClientRect();
            const a = act ? act.getBoundingClientRect() : null;
            const cs = window.getComputedStyle(row);
            return {
              rowH: Math.round(r.height),
              lineH: Math.round(l.height),
              border:
                Math.round(parseFloat(cs.borderTopWidth) + parseFloat(cs.borderBottomWidth)),
              hasAct: act !== null,
              // The control and the line share a band, rather than the control
              // sitting entirely below it: that is what a strip *is*.
              actOnLine: a ? Math.round(a.top) < Math.round(l.bottom) : null,
              actH: a ? Math.round(a.height) : null,
              // And it is still drawn. Reclaiming the height by hiding the
              // button would satisfy every number above and defeat the point.
              actVisible: act ? window.getComputedStyle(act).display !== "none" : null,
            };
          });
        })()`)) as {
          rowH: number;
          lineH: number;
          border: number;
          hasAct: boolean;
          actOnLine: boolean | null;
          actH: number | null;
          actVisible: boolean | null;
        }[];

        expect(rows.length, "the fixture must leave closed rows to measure").toBeGreaterThan(
          0,
        );
        const withAct = rows.filter((r) => r.hasAct);
        expect(
          withAct.length,
          "at least one closed row must still offer the mute control",
        ).toBeGreaterThan(0);

        for (const row of withAct) {
          expect(
            row.rowH,
            `a collapsed row is ${row.rowH}px tall at ${width}px to carry a ` +
              `${row.lineH}px line: ${row.rowH - row.lineH - row.border}px of it is ` +
              `a strip holding nothing but the mute button, which is the height ` +
              `SUB-141 exists to reclaim`,
          ).toBeLessThanOrEqual(row.lineH + row.border);
          expect(
            row.actOnLine,
            `the mute control starts below the line at ${width}px; it must share ` +
              `the line rather than occupy a band of its own`,
          ).toBe(true);
          expect(
            row.actVisible,
            "the control must still be drawn — the height comes out of the " +
              "strip, never out of the button's presence",
          ).toBe(true);
        }
      } finally {
        await page.close();
      }
    }
  }, 120_000);

  it("draws the mute control as a square on a closed row and a labelled button on an open one", async () => {
    /*
     * The trade this ticket was warned against: buying height by making the
     * two states look the same. They must not.
     *
     * The collapsed control is the 26px square the monitors and channels rows
     * already use for a per-row action; the expanded one keeps the worded
     * button in the tray's footer that #62 argued for. Measured as a *shape*
     * difference rather than a class-name difference, because a class name is
     * exactly what a stylesheet change could leave in place while the drawing
     * became identical.
     */
    const page = await openExpanded("dark", 1440);
    try {
      const shapes = (await page.evaluate(`(() => {
        const rows = Array.from(document.querySelectorAll(".inc-row"));
        const read = (row) => {
          const btn = row.querySelector(".inc-act button");
          if (!btn) return null;
          const b = btn.getBoundingClientRect();
          return {
            width: Math.round(b.width),
            height: Math.round(b.height),
            words: (btn.textContent || "").trim(),
            name: btn.getAttribute("aria-label") || "",
            glyphs: btn.querySelectorAll("svg").length,
          };
        };
        return {
          open: read(rows.find((r) => r.dataset.open === "true")),
          closed: read(rows.find((r) => r.dataset.open !== "true")),
        };
      })()`)) as {
        open: { width: number; height: number; words: string; name: string; glyphs: number };
        closed: {
          width: number;
          height: number;
          words: string;
          name: string;
          glyphs: number;
        };
      };

      expect(shapes.open, "an open row with a mute control").not.toBeNull();
      expect(shapes.closed, "a closed row with a mute control").not.toBeNull();

      // Closed: a square carrying a glyph and no words.
      expect(
        shapes.closed.glyphs,
        "the collapsed control must carry a glyph; a button with neither word " +
          "nor shape is an empty box",
      ).toBe(1);
      expect(
        shapes.closed.words,
        `the collapsed control rendered the words "${shapes.closed.words}"; on ` +
          `the line there is no room for a label beside four columns, which is ` +
          `why the strip existed`,
      ).toBe("");
      expect(
        Math.abs(shapes.closed.width - shapes.closed.height),
        `the collapsed control is ${shapes.closed.width}x${shapes.closed.height}; ` +
          `the house per-row action is a square, and a glyph in a box shaped for ` +
          `a word sits in a pool of space and stops reading as a target`,
      ).toBeLessThanOrEqual(1);

      // Open: the words come back, and the button is visibly a different object.
      expect(
        shapes.open.words,
        "the expanded control must say what it does in words: opening a row is " +
          "how a reader who does not recognise the glyph finds out",
      ).toMatch(/mute repeat alerts|repeat alerts muted/i);
      expect(
        shapes.open.width,
        `the open row's control is ${shapes.open.width}px wide and the closed ` +
          `row's is ${shapes.closed.width}px; if the two states draw the same ` +
          `control in the same place they do not read as different states`,
      ).toBeGreaterThan(shapes.closed.width * 2);

      // And the name is the same promise in both, naming the incident.
      for (const [where, shape] of [
        ["closed", shapes.closed],
        ["open", shapes.open],
      ] as const) {
        expect(
          shape.name,
          `the ${where} control's accessible name is "${shape.name}"; it must ` +
            `state the act, or a screen reader announces an unnamed button`,
        ).toMatch(/mute repeat alerts|repeat alerts muted/i);
        expect(
          shape.name.length,
          `the ${where} control's name must also say *which* incident it mutes; ` +
            `five rows of a button called "Mute" cannot be told apart`,
        ).toBeGreaterThan("Mute repeat alerts".length);
      }
    } finally {
      await page.close();
    }
  }, 60_000);
});
