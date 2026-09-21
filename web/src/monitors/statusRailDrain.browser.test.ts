/**
 * The dashboard's status rail, draining in a real browser (SUB-140).
 *
 * This file exists because of a mechanism difference that no unit test can
 * see. The incidents screen draws its rail as a `::before` and drains it with
 * `filter: saturate(.18)`; the dashboard draws the same mark as
 * `border-left-color` on the row's first cell, on the card and on the compact
 * line — elements that also carry the monitor's name, its latency and its
 * failure reason. A filter there would desaturate all of that text too, so the
 * dashboard drains by *colour* instead, to a token whose value is the
 * `saturate(.18)` of its base.
 *
 * That equivalence is the thing that can rot. `--down-drained` is a flat hex
 * written down once; if anybody edits `--down`, or changes the drain strength
 * in `connection.css`, the two halves of the screen silently withdraw by
 * different amounts and the row stops looking like one object. So the drained
 * border is not compared against a hard-coded expectation — it is compared
 * against what Chromium's own `saturate(.18)` paints on the live base colour,
 * sampled off a canvas. Both halves move together or this fails.
 *
 * jsdom can answer none of it: it applies no stylesheet, so every border
 * colour is the empty string and every filter is `none`.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { LAYOUT_STORAGE_KEY } from "../shell/preferences";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { serveBuild, type Server } from "../layout/harness/server";
import type { MonitorStatus } from "./types";

let server: Server;
let browser: Browser;

/**
 * Every status the product can render, from the type that defines them.
 *
 * Imported rather than retyped so that adding a sixth status to
 * `MonitorStatus` makes this file a compile error until somebody decides
 * whether the new state is a claim that should drain. A hand-copied list
 * would simply go on measuring five.
 */
const ALL_STATUSES = [
  "up",
  "down",
  "warning",
  "pending",
  "paused",
  "waiting",
] as const satisfies readonly MonitorStatus[];

/**
 * The compile-time half of the exhaustiveness check: this line fails to type
 * unless `ALL_STATUSES` names every member of the union, in either direction.
 */
type Missing = Exclude<MonitorStatus, (typeof ALL_STATUSES)[number]>;
type Extra = Exclude<(typeof ALL_STATUSES)[number], MonitorStatus>;
const EVERY_STATUS_HAS_A_POLICY: [Missing, Extra] extends [never, never]
  ? true
  : never = true;
void EVERY_STATUS_HAS_A_POLICY;

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/**
 * One screen, in one layout and one theme, fonts settled.
 *
 * `route` defaults to the dashboard. The detail page is reached by its real
 * URL (`/monitors/1`, the fixture's down monitor) rather than by clicking
 * through a row: the pill's border is what is under test, and a click path
 * adds a way for the test to fail that has nothing to do with the border.
 */
async function openDashboard(
  layout: "rows" | "cards" | "compact",
  ready: string,
  theme: "dark" | "light",
  route = "/",
): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + "/blank-for-storage", {
    waitUntil: "domcontentloaded",
  });
  await page.evaluate(
    (layoutKey: string, layoutValue: string, themeKey: string, themeValue: string) => {
      window.localStorage.setItem(layoutKey, layoutValue);
      window.localStorage.setItem(themeKey, themeValue);
    },
    LAYOUT_STORAGE_KEY,
    layout,
    THEME_STORAGE_KEY,
    theme,
  );
  await page.goto(server.url + route, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(ready, { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  return page;
}

/**
 * What Chromium paints when it applies the CSS `saturate()` filter to a
 * colour.
 *
 * Measured, not computed. A reimplementation of the luminance matrix here
 * would be a second opinion about the browser rather than a reading of it,
 * and the whole claim under test is "the drained border lands where a
 * filtered lamp lands" — so the browser has to be the one to say where that
 * is.
 *
 * The probe applies the real `filter: saturate()` CSS property to a real
 * element and screenshots it via a canvas, rather than going through an
 * `<svg><feColorMatrix type="saturate">`. That distinction is not pedantry
 * and it cost a debugging round: SVG filters interpolate in **linearRGB** by
 * default, so the same nominal amount produced rgb(144, 106, 109) where CSS
 * produces rgb(101, 66, 74) — a 43-byte gap on the red channel that looked
 * exactly like a wrong token. CSS `filter` is defined to operate in sRGB.
 * Using the same mechanism the lamp uses removes the question.
 */
const FILTERED = `((colour, amount) => {
  const canvas = document.createElement("canvas");
  canvas.width = 8;
  canvas.height = 8;
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  // Canvas2D's \`filter\` takes CSS filter syntax and rasterises it through
  // the same sRGB path the CSS property uses, which is the point: the lamp's
  // \`filter: saturate(.18)\` and this probe must not be two different
  // operations that happen to share a name.
  ctx.filter = "saturate(" + amount + ")";
  ctx.fillStyle = colour;
  ctx.fillRect(0, 0, 8, 8);
  const [r, g, b] = ctx.getImageData(4, 4, 1, 1).data;
  return [r, g, b];
})`;

/** Any CSS colour as sRGB bytes, using the browser's own conversion. */
const TO_RGB = `((colour) => {
  const canvas = document.createElement("canvas");
  canvas.width = 1;
  canvas.height = 1;
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  ctx.fillStyle = "#000";
  ctx.fillStyle = colour;
  ctx.globalCompositeOperation = "copy";
  ctx.fillRect(0, 0, 1, 1);
  const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
  return [r, g, b];
})`;

/**
 * The drain strength every mark on a stale screen shares.
 *
 * Read from the *lamp's own computed filter* rather than written here, so a
 * change to `connection.css` moves the expectation with it instead of leaving
 * this file asserting last month's number.
 */
const DRAIN_AMOUNT = `((lamp) => {
  const filter = window.getComputedStyle(lamp).filter;
  const match = /saturate\\(([\\d.]+)\\)/.exec(filter);
  return match ? Number.parseFloat(match[1]) : null;
})`;

/**
 * One layout's leading edge, live and then stale, with the lamp's drain.
 *
 * `data-conn` is set on the container directly. The binding from a dead stream
 * to that attribute is a unit concern and is already covered
 * (`LiveDashboard.test.tsx` asserts the attribute); what is unproven, and what
 * only a browser can answer, is whether the CSS reaches these borders at all
 * and what colour it paints them. A rule addressing `> :first-child` through
 * two attribute selectors is exactly the kind that silently matches nothing.
 */
function probe(
  selector: string,
  edge: "border-left-color" | "border-color",
  host = ".mon-dashboard",
): string {
  return `(async () => {
    const filtered = ${FILTERED};
    const toRgb = ${TO_RGB};
    const drainAmount = ${DRAIN_AMOUNT};
    const screen = document.querySelector(${JSON.stringify(host)});
    const target = document.querySelector(${JSON.stringify(selector)});
    if (!screen || !target) return { missing: true };
    const read = () => window.getComputedStyle(target).getPropertyValue(${JSON.stringify(edge)});

    const live = read();
    screen.setAttribute("data-conn", "stale");
    /*
     * The drain is a 600ms transition, so a read on the next tick returns its
     * FIRST frame — the undrained colour. Without this wait the live and stale
     * reads come back identical and "the edge drained" would be satisfied by
     * nothing having happened yet.
     */
    await new Promise((r) => setTimeout(r, 900));
    const stale = read();
    // The width is read WHILE STALE, which is the only moment it means
    // anything. It was read after the attribute had been put back to "live",
    // so the assertion was vacuous: a mutation adding \`border-left-width: 0\`
    // to the stale rule passed the whole file.
    const staleWidth = Math.round(
      Number.parseFloat(
        window.getComputedStyle(target).getPropertyValue(
          ${JSON.stringify(
            edge === "border-color" ? "border-top-width" : "border-left-width",
          )},
        ),
      ),
    );
    const lamp = document.querySelector(${JSON.stringify(host)} + " .led");
    const amount = lamp ? drainAmount(lamp) : null;
    const expected = amount === null ? null : filtered(live, amount);
    screen.setAttribute("data-conn", "live");
    return {
      missing: false,
      live,
      liveRgb: toRgb(live),
      stale,
      staleRgb: toRgb(stale),
      amount,
      expected,
      width: staleWidth,
    };
  })()`;
}

type Probe = {
  missing: boolean;
  live: string;
  liveRgb: [number, number, number];
  stale: string;
  staleRgb: [number, number, number];
  amount: number | null;
  expected: [number, number, number] | null;
  width: number;
};

/**
 * The three layouts that draw a leading edge, and the detail pill that draws
 * the same claim as a full border. Each names the selector its status edge is
 * actually on — which for the rows layout is the row's FIRST CELL, because a
 * `<tr>` paints no border under `border-collapse: separate`.
 */
const CARRIERS: {
  layout: "rows" | "cards" | "compact";
  ready: string;
  selector: string;
  edge: "border-left-color" | "border-color";
  what: string;
  /** The element carrying `data-conn`; the detail page has its own. */
  host?: string;
  route?: string;
}[] = [
  {
    layout: "rows",
    ready: "[data-testid^='monitor-row-']",
    selector: '.mon-row[data-status="down"] > :first-child',
    edge: "border-left-color",
    what: "the rows layout's leading edge",
  },
  {
    layout: "cards",
    ready: "[data-testid^='monitor-card-']",
    selector: '.mon-card[data-status="down"]',
    edge: "border-left-color",
    what: "the card's leading edge",
  },
  {
    layout: "compact",
    ready: "[data-testid^='monitor-line-']",
    selector: '.mon-line[data-status="down"]',
    edge: "border-left-color",
    what: "the compact line's leading edge",
  },
  /*
   * The detail page's status pill, which states the same claim as a full
   * border rather than a leading edge — so it drains with them, and the
   * `border-color` branch of `probe()` exists for it.
   *
   * It was missing here while the type and the comment both mentioned it,
   * which meant connection.css's two `.mon-detail-status` rules had no
   * browser coverage at all: the unit tests assert markup and `data-conn`,
   * and tokens.test.ts only reads the stylesheet as text (CodeRabbit, PR
   * #67). Reached by its real URL — monitor 1 is the fixture's down one.
   */
  {
    layout: "rows",
    ready: ".mon-detail-status",
    selector: '.mon-detail-status[data-status="down"]',
    edge: "border-color",
    what: "the detail page's status pill",
    host: ".mon-detail",
    route: "/monitors/1",
  },
];

describe("the status rail stops asserting when the stream dies", () => {
  for (const theme of ["dark", "light"] as const) {
    for (const carrier of CARRIERS) {
      it(`drains ${carrier.what} to the lamp's own strength (${theme})`, async () => {
        const page = await openDashboard(
          carrier.layout,
          carrier.ready,
          theme,
          carrier.route,
        );
        try {
          expect(
            await page.evaluate(() =>
              document.documentElement.getAttribute("data-theme"),
            ),
            "the theme preference must reach the document, or this measures the wrong palette",
          ).toBe(theme);

          const result = (await page.evaluate(
            probe(carrier.selector, carrier.edge, carrier.host),
          )) as Probe;

          expect(
            result.missing,
            `no down ${carrier.layout} carrier in the fixture; this test would assert nothing`,
          ).toBe(false);

          // The drain strength is read off the lamp, so a broken lamp rule
          // must not silently make this vacuous.
          expect(
            result.amount,
            "the lamp must carry a saturate() drain for the edge to be held to it",
          ).not.toBeNull();

          // 1. It actually changed. This is what SUB-140 reported: the edge
          //    sat at full strength through the entire withdrawal.
          expect(
            result.stale,
            `${carrier.what} kept ${result.live} after the stream went stale. ` +
              `A saturated status colour is a claim about now, and we have ` +
              `stopped knowing (DESIGN.md §6).`,
          ).not.toBe(result.live);

          // 2. It landed exactly where a filtered lamp lands. Measured in
          //    Chromium, not asserted: `expected` is the live colour put
          //    through the browser's own saturate() at the lamp's amount.
          //    Within one byte per channel, which is filter-chain rounding
          //    and not a decision anybody made.
          for (const [i, channel] of ["r", "g", "b"].entries()) {
            expect(
              Math.abs(result.staleRgb[i] - result.expected![i]),
              `${carrier.what} drained to rgb(${result.staleRgb.join(", ")}) ` +
                `but a lamp filtered at saturate(${result.amount}) paints ` +
                `rgb(${result.expected!.join(", ")}) — the ${channel} channel ` +
                `is off by ${Math.abs(result.staleRgb[i] - result.expected![i])}. ` +
                `The row must withdraw as one object, not in two stages.`,
            ).toBeLessThanOrEqual(1);
          }

          // 3. Drained, never deleted. §6's method is "nothing is hidden and
          //    nothing moves": removing the edge would say the monitor
          //    stopped being down, and the last known state is the most
          //    useful thing left on a stale screen.
          //    The pill's border is 1px (it frames a box); the three leading
          //    edges are the 2px status rail. Both are "still painted", which
          //    is the claim — so the expectation follows the carrier rather
          //    than hard-coding the rail's number for all four.
          const expectedWidth = carrier.edge === "border-color" ? 1 : 2;
          expect(
            result.width,
            `${carrier.what} must still be drawn at its full ${expectedWidth}px ` +
              `once stale; dropping it would delete the last known state ` +
              `rather than stop asserting it`,
          ).toBe(expectedWidth);
        } finally {
          await page.close();
        }
      }, 90_000);
    }
  }

  /*
   * WHICH statuses drain, pinned in both directions and for every status.
   *
   * This replaces a check that measured only `waiting` and `paused` as the
   * fixture happened to render them, and it is here because a mutation
   * exposed the hole: adding `waiting` to the drain rule — a straightforward
   * over-reach — passed the entire file. The harness fixture renders
   * `down`, `up`, `up`, `paused` and nothing else, so `waiting` and
   * `pending` were both absent from the DOM, and a policy about a status
   * nobody rendered was a policy no assertion could see. Worse in one
   * direction than the other: `pending` is a status this ticket decided TO
   * drain, and that half was equally unverified.
   *
   * So the rows are synthesised rather than found. Five `.mon-line`
   * elements, one per status, appended inside the real `.mon-dashboard` so
   * the real stylesheet applies to them — the computed colour is Chromium's
   * answer about the shipped CSS, not about a fixture's contents. A status
   * the product can render but the fixture omits is exactly the case that
   * needs pinning, and it is the case a DOM-only search can never reach.
   *
   * The policy (DESIGN.md §6): draining withdraws a *claim*, and a claim is
   * a present-tense statement about the monitored service.
   *
   *   down    — "this is failing"          → drains
   *   pending — "a check is in flight"     → drains
   *   up      — carries no coloured edge at all, so there is nothing to
   *             drain; asserted as unchanged so a rule growing one is caught
   *   waiting — "no data yet": a fact about our own configuration → holds
   *   paused  — "somebody switched this off": likewise → holds
   */
  const DRAINING_STATUSES = ["down", "warning", "pending"] as const;
  const HOLDING_STATUSES = ["up", "waiting", "paused"] as const;

  for (const theme of ["dark", "light"] as const) {
    it(`drains exactly the statuses that make a claim, and no others (${theme})`, async () => {
      const page = await openDashboard(
        "compact",
        "[data-testid^='monitor-line-']",
        theme,
      );
      try {
        const measured = (await page.evaluate(`(async () => {
          const filtered = ${FILTERED};
          const drainAmount = ${DRAIN_AMOUNT};
          const screen = document.querySelector(".mon-dashboard");
          const statuses = ${JSON.stringify([
            ...DRAINING_STATUSES,
            ...HOLDING_STATUSES,
          ])};

          // One synthetic line per status, inside the real dashboard so the
          // shipped stylesheet reaches it. Off-screen but NOT display:none
          // or visibility:hidden, either of which would stop the border
          // being resolved at all and make every reading below identical.
          const host = document.createElement("div");
          host.style.cssText = "position:absolute;left:-9999px;top:0";
          for (const status of statuses) {
            const line = document.createElement("div");
            line.className = "mon-line";
            line.setAttribute("data-status", status);
            line.setAttribute("data-probe", status);
            line.textContent = status;
            host.appendChild(line);
          }
          screen.appendChild(host);
          await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));

          const edge = (status) =>
            window.getComputedStyle(
              host.querySelector('[data-probe="' + status + '"]'),
            ).borderLeftColor;
          const widthOf = (status) =>
            Math.round(
              Number.parseFloat(
                window.getComputedStyle(
                  host.querySelector('[data-probe="' + status + '"]'),
                ).borderLeftWidth,
              ),
            );

          const live = {};
          for (const status of statuses) live[status] = edge(status);

          screen.setAttribute("data-conn", "stale");
          // The 600ms transition again: a read on the next frame returns the
          // undrained colour, which would make every comparison below pass
          // by nothing having happened.
          await new Promise((r) => setTimeout(r, 900));

          const stale = {};
          const staleWidth = {};
          for (const status of statuses) {
            stale[status] = edge(status);
            staleWidth[status] = widthOf(status);
          }

          const lamp = document.querySelector(".mon-dashboard .led");
          const amount = lamp ? drainAmount(lamp) : null;
          const expected = {};
          for (const status of statuses) {
            expected[status] =
              amount === null ? null : filtered(live[status], amount);
          }

          screen.setAttribute("data-conn", "live");
          host.remove();
          return { live, stale, staleWidth, expected, amount };
        })()`)) as {
          live: Record<string, string>;
          stale: Record<string, string>;
          staleWidth: Record<string, number>;
          expected: Record<string, [number, number, number] | null>;
          amount: number | null;
        };

        expect(
          measured.amount,
          "the lamp must carry a saturate() drain for the edges to be held to it",
        ).not.toBeNull();

        // Half one: every status that makes a claim actually withdraws, and
        // withdraws to the lamp's strength. `pending` is in here precisely
        // because the fixture never renders it.
        for (const status of DRAINING_STATUSES) {
          expect(
            measured.stale[status],
            `a ${status} edge kept ${measured.live[status]} after the stream ` +
              `went stale. "${status}" is a present-tense claim about the ` +
              `monitored service, and we have stopped knowing (DESIGN.md §6).`,
          ).not.toBe(measured.live[status]);
          const got = measured.stale[status];
          const want = measured.expected[status]!;
          /*
           * Per channel, within one byte — the same tolerance the per-layout
           * test above uses, rather than a byte-for-byte string compare.
           *
           * The harness resolves whatever Chromium it finds
           * (`PUPPETEER_EXECUTABLE_PATH`, else the first build in the local
           * Playwright cache), so the canvas filter path is not pinned to one
           * version and a one-byte rounding difference is a property of the
           * renderer rather than a decision anybody made. An exact compare
           * here and a ±1 compare twenty lines up was an inconsistency, not a
           * stricter standard (CodeRabbit, PR #67). One byte is still far
           * tighter than any wrong token: the nearest plausible mistake,
           * `--down-dim`, is 85 bytes away on the red channel.
           */
          const gotRgb = (/rgb\((\d+),\s*(\d+),\s*(\d+)\)/.exec(got) ?? [])
            .slice(1)
            .map(Number);
          expect(
            gotRgb.length,
            `a ${status} edge computed to "${got}", which is not an rgb() ` +
              `triple this assertion can compare`,
          ).toBe(3);
          for (const [i, channel] of ["r", "g", "b"].entries()) {
            expect(
              Math.abs(gotRgb[i] - want[i]),
              `a ${status} edge drained to ${got}, but a lamp filtered at ` +
                `saturate(${measured.amount}) paints rgb(${want.join(", ")}) ` +
                `— the ${channel} channel is off by ` +
                `${Math.abs(gotRgb[i] - want[i])}. Every mark on a stale ` +
                `screen withdraws by the same amount or the row withdraws in ` +
                `stages.`,
            ).toBeLessThanOrEqual(1);
          }
          // Drained, not deleted (§6: nothing is hidden and nothing moves).
          expect(
            measured.staleWidth[status],
            `a stale ${status} edge must still be drawn at its full 2px; ` +
              `dropping it would delete the last known state rather than ` +
              `stop asserting it`,
          ).toBe(2);
        }

        // Half two: every status that is NOT a claim keeps its edge exactly.
        // This is the half the earlier version of this file could not make,
        // and the half a widened selector trips over.
        for (const status of HOLDING_STATUSES) {
          expect(
            measured.stale[status],
            `a ${status} edge changed from ${measured.live[status]} to ` +
              `${measured.stale[status]} once the stream went stale. ` +
              `"${status}" is not a claim about the monitored service — it is ` +
              `a fact about our own configuration, and it is exactly as true ` +
              `after the stream dies as before it. Draining it invents doubt ` +
              `about the one thing we still know (DESIGN.md §6).`,
          ).toBe(measured.live[status]);
        }

        // And the two halves must together cover every status the product can
        // render. The *names* are checked against `MonitorStatus` at compile
        // time (see EVERY_STATUS_HAS_A_POLICY above the describe); this
        // asserts the count, so a status deleted from one of the two lists
        // cannot pass by simply not being measured.
        expect(
          [...DRAINING_STATUSES, ...HOLDING_STATUSES].length,
          "every status the dashboard can render needs a stated drain policy",
        ).toBe(ALL_STATUSES.length);
      } finally {
        await page.close();
      }
    }, 90_000);
  }

  it("drains the failure reason in every layout that prints one", async () => {
    /*
     * `.mon-line-error` was already in connection.css's ink-drain block and
     * `.mon-error` / `.mon-card-error` were not, so two of the three layouts
     * kept a full-strength red sentence asserting a live failure beside a
     * drained lamp. Checked as a *computed colour* rather than by reading the
     * stylesheet, because the question is which rule wins: the bare
     * `.mon-error` sets `var(--down)` and the drain selector has to out-
     * specify it.
     */
    for (const [layout, ready, selector] of [
      ["rows", "[data-testid^='monitor-row-']", ".mon-error"],
      ["cards", "[data-testid^='monitor-card-']", ".mon-card-error"],
      ["compact", "[data-testid^='monitor-line-']", ".mon-line-error"],
    ] as const) {
      const page = await openDashboard(layout, ready, "dark");
      try {
        const drain = (await page.evaluate(`(async () => {
          const screen = document.querySelector(".mon-dashboard");
          const el = document.querySelector(${JSON.stringify(selector)});
          if (!el) return { missing: true };
          const live = window.getComputedStyle(el).color;
          const down = window.getComputedStyle(document.documentElement)
            .getPropertyValue(${JSON.stringify(layout === "compact" ? "--ink-2" : "--down")}).trim();
          const ink3 = window.getComputedStyle(document.documentElement)
            .getPropertyValue("--ink-3").trim();
          screen.setAttribute("data-conn", "stale");
          await new Promise((r) => setTimeout(r, 900));
          const stale = window.getComputedStyle(el).color;
          const probe = document.createElement("span");
          document.body.appendChild(probe);
          probe.style.color = ink3;
          const ink3Rgb = window.getComputedStyle(probe).color;
          probe.style.color = down;
          const downRgb = window.getComputedStyle(probe).color;
          probe.remove();
          screen.setAttribute("data-conn", "live");
          return { missing: false, live, stale, ink3Rgb, downRgb };
        })()`)) as {
          missing: boolean;
          live: string;
          stale: string;
          ink3Rgb: string;
          downRgb: string;
        };

        expect(
          drain.missing,
          `no ${selector} in the ${layout} layout; the fixture must have a failing monitor`,
        ).toBe(false);
        // Compact's nested panel needs ink-2 for AA (DESIGN.md §2.6); the
        // other layouts keep status ink. All three must still drain to ink-3.
        expect(
          drain.live,
          `${selector} must use its documented live ink role`,
        ).toBe(drain.downRgb);
        expect(
          drain.stale,
          `${selector} stayed at ${drain.stale} once the stream went stale; a ` +
            `full-strength red sentence beside a drained lamp re-asserts in ` +
            `words what the screen just stopped asserting in colour ` +
            `(DESIGN.md §6)`,
        ).toBe(drain.ink3Rgb);
      } finally {
        await page.close();
      }
    }
  }, 120_000);

  it("keeps a word on a drained down row, so colour is never alone", async () => {
    /*
     * DESIGN.md §9 after the drain, which is the half of this ticket that is
     * easy to lose: once the edge is desaturated, what still says "down"?
     *
     * The answer has to hold in greyscale and it has to hold for a screen
     * reader, so it is asserted as text rather than as pixels — the row's
     * accessible name, and the visible failure reason printed where the
     * latency would be. Both survive the drain untouched, because neither is
     * made of colour.
     */
    const page = await openDashboard(
      "rows",
      "[data-testid^='monitor-row-']",
      "dark",
    );
    try {
      const carriers = (await page.evaluate(`(async () => {
        const screen = document.querySelector(".mon-dashboard");
        screen.setAttribute("data-conn", "stale");
        await new Promise((r) => setTimeout(r, 900));
        const row = document.querySelector('.mon-row[data-status="down"]');
        const srWord = row.querySelector(".mon-cell--led .sr-only");
        const error = row.querySelector(".mon-error");
        const heading = document.querySelector(".mon-section-title");
        const upRow = document.querySelector('.mon-row[data-status="up"]');
        const upWord = upRow
          ? upRow.querySelector(".mon-cell--led .sr-only")
          : null;
        const result = {
          word: (srWord?.textContent ?? "").trim(),
          error: (error?.textContent ?? "").trim(),
          errorPainted: error ? error.getBoundingClientRect().width > 2 : false,
          heading: (heading?.textContent ?? "").trim(),
          // Position: a down row still sorts above the healthy ones.
          firstRowIsDown:
            document.querySelector(".mon-row")?.getAttribute("data-status") === "down",
          // The comparison §9 actually has to survive in the drained state:
          // a down row against an up row, with the hue no longer carrying it.
          upWord: (upWord?.textContent ?? "").trim(),
          // An up row has no coloured edge at rest and must gain none here.
          upHasError: upRow ? upRow.querySelector(".mon-error") !== null : null,
          upIsBelow: upRow
            ? row.compareDocumentPosition(upRow) &
                Node.DOCUMENT_POSITION_FOLLOWING
              ? true
              : false
            : null,
        };
        screen.setAttribute("data-conn", "live");
        return result;
      })()`)) as {
        word: string;
        error: string;
        errorPainted: boolean;
        heading: string;
        firstRowIsDown: boolean;
        upWord: string;
        upHasError: boolean | null;
        upIsBelow: boolean | null;
      };

      /*
       * The word, which the drain must not have taken with it.
       *
       * Only its *presence* is asserted here, not its tense. The tense lives
       * in the markup (`statusWord`, driven by the store) and this file
       * drives staleness by setting `data-conn` on the container, which
       * changes what CSS applies and cannot re-render React — so a
       * past-tense assertion here would be testing the harness, not the
       * product. `stale-tense.test.tsx` renders a genuinely stale store and
       * asserts "Was down" on every layout; that is its job. What is this
       * file's job is that the drained row still has a word at all.
       */
      expect(
        carriers.word,
        "a drained row must still say its last known state in words, or the " +
          "drain has removed the only signal a screen reader had",
      ).toMatch(/down/i);
      // The failure reason, in words and actually painted.
      expect(
        carriers.error.length,
        "the failure reason must survive the drain: it is the carrier that " +
          "works in greyscale",
      ).toBeGreaterThan(0);
      expect(carriers.errorPainted, "the failure reason must be visible").toBe(
        true,
      );
      // Position, which is a carrier no filter can touch.
      expect(
        carriers.firstRowIsDown,
        "a down row must still sort to the top of a stale list",
      ).toBe(true);
      expect(
        carriers.heading,
        "the counted section heading must survive the drain",
      ).toMatch(/needs attention/i);

      /*
       * And the comparison §9 actually has to survive: a drained DOWN row
       * against an UP row, with the hue no longer doing the work.
       *
       * The three assertions above each say "the down row still has X". None
       * of them says X distinguishes it from a healthy row — a carrier both
       * rows share is not a carrier. So the up row is measured too.
       */
      expect(
        carriers.upWord,
        "the fixture must contain an up row, or there is nothing to " +
          "distinguish the drained down row from",
      ).not.toBe("");
      expect(
        carriers.upWord.toLowerCase(),
        `both rows announce "${carriers.word}" / "${carriers.upWord}"; if the ` +
          `words match, the status word is not a carrier and a stale screen ` +
          `has only the drained hue left (DESIGN.md §9)`,
      ).not.toBe(carriers.word.toLowerCase());
      expect(
        carriers.upHasError,
        "an up row must not print a failure reason, or that carrier does not " +
          "separate down from up either",
      ).toBe(false);
      expect(
        carriers.upIsBelow,
        "position is the first carrier §2.3 lists: a down row must still sort " +
          "above a healthy one on a stale screen",
      ).toBe(true);
    } finally {
      await page.close();
    }
  }, 90_000);
});
