/**
 * The monitors inventory: every row the same height (SUB-167).
 *
 * A list whose rows are different heights reads as a list of different kinds
 * of thing. On this screen they are one kind of thing, and the page exists so
 * the eye can run straight down a column; a row that is 4px taller because
 * its channel cell holds a chip instead of a word breaks that run for no
 * reason a reader can account for.
 *
 * Asserted as a relationship, not as a number: at each width, every row is
 * the height of every other row. What that height is belongs to the font and
 * the tokens, and changes with them; that the rows agree must not.
 *
 * The fixture is the harness's four monitors with their cells pushed to every
 * shape a row can take, because a layout that holds for four identical rows
 * proves nothing: a known channel list, a known-empty one (\"none\", a dim
 * zero), one alerting through the default, one whose channels are unknown
 * (\"not loaded\", a dashed chip), a paused row with its Paused chip, a push
 * monitor whose timeout is \"n/a\", no tags and several. Two widths are on
 * each side of the container breakpoint, where the row wraps to two lines,
 * plus the phone.
 *
 * A viewer gets the same page without the checkboxes and the actions, so
 * both roles are measured.
 *
 * Does not run with `npm test`: needs a built bundle and a browser.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import type { ApiMonitor } from "../monitors/types";

let server: Server;
let browser: Browser;

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/** Each harness monitor, given a different channels cell and tag count. */
function varied(monitors: ApiMonitor[]): ApiMonitor[] {
  const [first, second, third, fourth] = monitors;
  return [
    // A long attached list plus a rule-routed channel: the widest text cell.
    {
      ...first!,
      channels: [
        { id: 1, name: "ops-pager" },
        { id: 2, name: "payments-oncall-email" },
      ],
      rule_channels: [
        {
          rule_id: 1,
          tag_key: "team",
          tag_value: "payments-platform",
          channels: [{ id: 3, name: "payments-slack" }],
        },
      ],
    },
    // Known and empty, with a default standing in: "(default)" text.
    { ...second!, channels: [], rule_channels: [], default_channel: { id: 1, name: "ops-pager" } },
    // Paused, with its chip, and known-empty with no default: the dim "none".
    { ...third!, channels: [], rule_channels: [] },
    // A push monitor, whose timeout is "n/a", with no tags at all and no
    // `channels` field: "not loaded", the dashed chip.
    {
      ...fourth!,
      type: "push",
      target: "",
      timeout_s: null as unknown as number,
      push_interval_s: 3600,
      push_grace_s: 300,
      push_token_prefix: "sgp_4f2a",
      tags: undefined,
    },
  ];
}

async function openInventory(width: number, role: "admin" | "viewer"): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.setRequestInterception(true);
  page.on("request", async (request) => {
    const url = new URL(request.url());
    if (url.origin !== server.url) {
      await request.abort("blockedbyclient");
    } else if (url.pathname === "/api/v1/auth/me") {
      await request.respond({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: 1, email: "operator@example.com", role, created_at: new Date().toISOString() }),
      });
    } else if (url.pathname === "/api/v1/monitors" && request.method() === "GET") {
      const response = await fetch(request.url());
      const body = (await response.json()) as { monitors: ApiMonitor[] };
      await request.respond({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ monitors: varied(body.monitors) }),
      });
    } else {
      await request.continue();
    }
  });
  await page.goto(server.url + "/monitors", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".inv-row", { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  return page;
}

type RowMeasure = {
  name: string;
  height: number;
  wrapped: boolean;
  /** Each column legend's top, in page coordinates. */
  labelTops: number[];
  /** The tallest legend's height: two legends closer than this share a line. */
  labelHeight: number;
  /** Each column legend's position relative to its own row's corner. */
  labelOffsets: string[];
};

function measureRows(page: Page): Promise<RowMeasure[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>(".inv-row")].map((row) => {
      const main = row.querySelector<HTMLElement>(".inv-main")!.getBoundingClientRect();
      const meta = row.querySelector<HTMLElement>(".inv-meta")!.getBoundingClientRect();
      const labels = [...row.querySelectorAll<HTMLElement>(".inv-label")];
      return {
        name: (row.querySelector(".inv-name a")?.textContent ?? "").trim(),
        height: Math.round(row.getBoundingClientRect().height * 10) / 10,
        // The meta row sits under the name once the container is too narrow.
        wrapped: meta.top >= main.bottom - 1,
        labelTops: labels.map((label) => Math.round(label.getBoundingClientRect().top * 10) / 10),
        labelHeight: Math.max(...labels.map((label) => label.getBoundingClientRect().height)),
        labelOffsets: labels.map((label) => {
          const box = label.getBoundingClientRect();
          const corner = row.getBoundingClientRect();
          return `${Math.round(box.left - corner.left)},${Math.round(box.top - corner.top)}`;
        }),
      };
    }),
  );
}

/**
 * Every settings value that breaks its slot: a value box outside the slot
 * track on either axis, text that runs onto a second line, or content that
 * overflows a box which does not clip it. An ellipsis passes: its text is one
 * line and the element that overflows is the one that clips it.
 */
function valuesOutOfSlot(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>(".inv-col")].flatMap((col) => {
      const value = col.lastElementChild as HTMLElement;
      const label = `${col.className}: ${(value.textContent ?? "").trim()}`;
      const problems: string[] = [];

      // The slot is the column's second grid track, not the whole column:
      // the column also holds the legend and the gap above the value.
      const style = getComputedStyle(col);
      const tracks = style.gridTemplateRows.split(" ").map(parseFloat);
      const colBox = col.getBoundingClientRect();
      const left = colBox.left + parseFloat(style.borderLeftWidth) + parseFloat(style.paddingLeft);
      const right = colBox.right - parseFloat(style.borderRightWidth) - parseFloat(style.paddingRight);
      const top =
        colBox.top +
        parseFloat(style.borderTopWidth) +
        parseFloat(style.paddingTop) +
        tracks[0]! +
        (parseFloat(style.rowGap) || 0);
      const bottom = top + tracks[1]!;
      const box = value.getBoundingClientRect();
      if (
        box.left < left - 0.5 ||
        box.right > right + 0.5 ||
        box.top < top - 0.5 ||
        box.bottom > bottom + 0.5
      ) {
        problems.push("outside its slot");
      }

      // One line: every run of text inside the value lays out as one line box.
      const walker = document.createTreeWalker(value, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        if (!(node.textContent ?? "").trim()) continue;
        const range = document.createRange();
        range.selectNodeContents(node);
        const lines = new Set([...range.getClientRects()].map((rect) => Math.round(rect.top)));
        if (lines.size > 1) problems.push(`"${(node.textContent ?? "").trim()}" wraps`);
      }

      // No visible overflow: whatever is wider than its box is clipped there.
      for (const element of [value, ...value.querySelectorAll<HTMLElement>("*")]) {
        if (
          element.scrollWidth > element.clientWidth + 1 &&
          getComputedStyle(element).overflowX === "visible"
        ) {
          problems.push(`${element.className || element.tagName} overflows visibly`);
        }
      }

      return problems.length === 0 ? [] : [`${label}: ${problems.join(", ")}`];
    }),
  );
}

/*
 * 1440 and 1100 hold the one-line row; 901 and 700 are beside the expanded
 * sidebar, where the list is under 836px and the row wraps; 390 is a phone.
 */
const WIDTHS = [1440, 1100, 901, 700, 390];

describe("the monitors inventory rows", () => {
  for (const role of ["admin", "viewer"] as const) {
    for (const width of WIDTHS) {
      describe(`at ${width}px for ${role === "admin" ? "an admin" : "a viewer"}`, () => {
        let page: Page;
        let rows: RowMeasure[];

        beforeAll(async () => {
          page = await openInventory(width, role);
          rows = await measureRows(page);
        }, 30_000);

        afterAll(async () => {
          await page?.close();
        });

        it("are all one height", () => {
          expect(rows.length, "the fixture did not render four rows").toBe(4);
          const heights = Object.fromEntries(rows.map((row) => [row.name, row.height]));
          expect(
            new Set(rows.map((row) => row.height)).size,
            `row heights differ: ${JSON.stringify(heights)}`,
          ).toBe(1);
          // The rows agree on their shape, too: all one line or all wrapped.
          expect(new Set(rows.map((row) => row.wrapped)).size).toBe(1);
        });

        it("put the column legends on shared lines", () => {
          /*
           * The settings columns are read downward, and across a row they are
           * read as one band of legends over one band of values. A value that
           * is a chip is taller than a value that is a word; when the columns
           * centred on that height, the legend over the chip rode a few pixels
           * higher than its neighbours and the band stepped.
           *
           * On a one-line row that is one band. Wrapped, the columns sit on
           * several lines, and two legends are then either on the same line,
           * at exactly the same top, or on different lines, at least a legend
           * apart: never a few pixels out.
           */
          for (const row of rows) {
            const tops = [...new Set(row.labelTops)].sort((a, b) => a - b);
            const context = `${row.name}: legend tops ${JSON.stringify(row.labelTops)}`;
            if (!row.wrapped) expect(tops.length, context).toBe(1);
            for (let i = 1; i < tops.length; i++) {
              expect(tops[i]! - tops[i - 1]!, context).toBeGreaterThanOrEqual(row.labelHeight - 0.5);
            }
          }
        });

        it("show each value whole inside its slot, on one line", async () => {
          /*
           * The slot holds the row's height, so a value that wraps no longer
           * makes the row taller; it overflows the slot instead and is cut
           * through the middle of a line, which the height checks cannot see.
           * A value either fits its slot or ends in an ellipsis on one line.
           */
          expect(await valuesOutOfSlot(page)).toEqual([]);
        });

        it("put each column at the same place in every row", () => {
          /*
           * Equal heights are half of reading down a column; the other half is
           * that the column is where the eye left it in the row above. Wrapped,
           * the columns used to break wherever each row's content ran out, so
           * Tags sat on the second line of one row and the third of the next.
           */
          const shapes = Object.fromEntries(rows.map((row) => [row.name, row.labelOffsets.join(" ")]));
          expect(
            new Set(Object.values(shapes)).size,
            `legend positions differ between rows: ${JSON.stringify(shapes)}`,
          ).toBe(1);
        });
      });
    }
  }

  it("wraps below the container breakpoint and not above it", async () => {
    // Without this the height check could pass by every row being one line at
    // every width, or wrapped at every width, and prove half of what it says.
    for (const [width, wrapped] of [[1440, false], [390, true]] as const) {
      const page = await openInventory(width, "admin");
      try {
        const rows = await measureRows(page);
        expect(rows.every((row) => row.wrapped === wrapped), `at ${width}px`).toBe(true);
      } finally {
        await page.close();
      }
    }
  });
});
