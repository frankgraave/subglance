/**
 * The notification channel rows: every row the same height (SUB-172).
 *
 * The channel rows are the monitors inventory's rows (`.inv-row`,
 * `.inv-col`), so they inherit the fixed value slot that holds those rows at
 * one height (DESIGN.md §8.10). What they do not inherit is the test:
 * `inventory-rows.browser.test.ts` opens `/monitors` only, and this page has
 * its own name line (chips for Default, quiet hours and Disabled, and no
 * link), its own columns (Type and Added) and its own wrap, at a container
 * width of 638px rather than 836.
 *
 * Asserted as relationships, as the inventory's test is: at each width every
 * row is the height of every other row, and each column legend is at the same
 * place in every row. What the height is belongs to the font and the tokens.
 *
 * The fixture is a channel of every type this build knows plus one it does
 * not, pushed to every shape a row can take: the default, disabled, quiet
 * hours (held and dropped), two chips at once, a name that ends in an
 * ellipsis, and every delivery history the Delivery column can draw (SUB-180):
 * delivered, failed, retrying with the widest count beside it, none in the
 * window, and a record the server did not send. Routing rules are not a
 * shape: this page does not draw them on a channel row.
 *
 * A viewer gets the same page without the actions, so both roles are
 * measured.
 *
 * Does not run with `npm test`: needs a built bundle and a browser.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import type { ApiChannel } from "../notifications/channels";

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

const DAY = 86_400_000;
const ago = (days: number) => new Date(Date.now() - days * DAY).toISOString();

const none = { state: "none", window_days: 30, failed: 0, pending: 0, retrying: 0, last_error: "" };
const delivered = { ...none, state: "delivered", last_delivered_at: ago(1) };
const failed = {
  ...none,
  state: "failed",
  last_failed_at: ago(0),
  failed: 11,
  last_error: "gave up after 5 attempts: endpoint rejected the alert (404)",
};
const retrying = { ...none, state: "retrying", pending: 128, retrying: 128, last_error: "endpoint returned 503" };

/** One channel per row shape, masked the way the API masks them. */
const CHANNELS: ApiChannel[] = [
  // The default, with quiet hours: two chips on one name line.
  {
    id: 1,
    name: "Ops mailing list",
    type: "email",
    config: {
      to: "ops@acme-corporation.example, platform-oncall@acme-corporation.example",
      from: "subglance@acme-corporation.example",
      host: "smtp.acme-corporation.example",
      port: "587",
    },
    delivery: delivered,
    enabled: true,
    is_default: true,
    quiet_hours: { start: "23:00", end: "07:00", timezone: "Europe/Amsterdam", during: "hold" },
    created_at: ago(40),
  },
  // A name too long for any width: it has to end in an ellipsis.
  {
    id: 2,
    name: "platform-oncall-primary-escalation-for-the-payments-and-billing-services",
    type: "slack",
    config: { url: "****0f3a" },
    delivery: failed,
    enabled: true,
    created_at: ago(30),
  },
  // Disabled: the dashed row and the Disabled chip.
  { id: 3, name: "Release announcements", type: "discord", config: { url: "****d1sc" }, enabled: false, created_at: ago(20), delivery: none },
  // Disabled with quiet hours that drop: two chips again, on a dimmed row.
  {
    id: 4,
    name: "Weekend pager",
    type: "telegram",
    config: { bot_token: "****9xQ2", chat_id: "-1001234567890" },
    delivery: none,
    enabled: false,
    quiet_hours: { start: "22:00", end: "06:30", timezone: "UTC", during: "drop" },
    created_at: ago(5),
  },
  { id: 5, name: "Phone push", type: "ntfy", config: { url: "https://ntfy.example", topic: "****opic" }, enabled: true, created_at: ago(4), delivery: retrying },
  // No creation date, and a delivered history.
  { id: 6, name: "Home server", type: "gotify", config: { url: "****otfy" }, enabled: true, delivery: delivered },
  { id: 7, name: "On-call phones", type: "sms", config: { provider: "twilio", numbers: "+31600000001\n+31600000002\n+31600000003" }, enabled: true, created_at: ago(2), delivery: failed },
  { id: 8, name: "Status page webhook", type: "webhook", config: { url: "****hook" }, enabled: true, created_at: ago(1), delivery: delivered },
  // A type this build does not know: "Unknown type", the widest type label.
  // No delivery record either, as an older server sends: the cell is empty.
  { id: 9, name: "PagerDuty escalation", type: "pagerduty", config: { routing_key: "****ab19" }, enabled: true, created_at: ago(0) },
];

/** Where the channel list wraps its rows (notifications.css). */
const WRAP = 638;

async function openChannels(width: number, role: "admin" | "viewer"): Promise<Page> {
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
    } else if (url.pathname === "/api/v1/channels" && request.method() === "GET") {
      await request.respond({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ channels: CHANNELS }),
      });
    } else {
      await request.continue();
    }
  });
  await page.goto(server.url + "/notifications", { waitUntil: "domcontentloaded" });
  await page.waitForFunction((n) => document.querySelectorAll(".inv-row").length === n, { timeout: 15_000 }, CHANNELS.length);
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
  /** The name line alone, without a test result printed under it. */
  line: number;
  wrapped: boolean;
  labelTops: number[];
  labelHeight: number;
  /** Each column legend's position relative to its own row's corner. */
  labelOffsets: string[];
  /** The name text's top, relative to the row's corner. */
  nameTop: number;
};

function measureRows(page: Page): Promise<{ list: number; rows: RowMeasure[] }> {
  return page.evaluate(() => {
    const round = (n: number) => Math.round(n * 10) / 10;
    const rows = [...document.querySelectorAll<HTMLElement>(".nt-card .inv-row")].map((row) => {
      const main = row.querySelector<HTMLElement>(".inv-main")!.getBoundingClientRect();
      const meta = row.querySelector<HTMLElement>(".inv-meta")!.getBoundingClientRect();
      const labels = [...row.querySelectorAll<HTMLElement>(".inv-label")];
      const corner = row.getBoundingClientRect();
      return {
        name: (row.querySelector(".inv-name")?.firstChild?.textContent ?? "").trim(),
        height: round(corner.height),
        line: round(row.querySelector<HTMLElement>(".inv-line")!.getBoundingClientRect().height),
        wrapped: meta.top >= main.bottom - 1,
        labelTops: labels.map((label) => round(label.getBoundingClientRect().top)),
        labelHeight: Math.max(...labels.map((label) => label.getBoundingClientRect().height)),
        nameTop: (() => {
          const range = document.createRange();
          range.selectNodeContents(row.querySelector(".inv-name")!.firstChild!);
          return round(range.getBoundingClientRect().top - corner.top);
        })(),
        labelOffsets: labels.map((label) => {
          const box = label.getBoundingClientRect();
          return `${Math.round(box.left - corner.left)},${Math.round(box.top - corner.top)}`;
        }),
      };
    });
    const list = document.querySelector<HTMLElement>(".nt-card .inv-list")!.getBoundingClientRect().width;
    return { list: round(list), rows };
  });
}

/**
 * Every column value that does not fit: text that runs onto a second line, a
 * value whose pieces stack taller than its slot, or a value wider than its
 * column, which clips it mid-character.
 *
 * Lines are counted per text node. The Delivery cell (SUB-180) is a chip and
 * a moment side by side, and a range over the whole value reports the chip's
 * box and the text inside it at different tops although both sit on one
 * line; counting those as lines flagged a lone chip, which cannot wrap, as
 * wrapping. A text node that breaks still reports one rect per line, and
 * pieces that wrap under each other make the value taller than the slot.
 */
function valuesOutOfColumn(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const slot = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--size-row-sm"));
    return [...document.querySelectorAll<HTMLElement>(".nt-card .inv-col")].flatMap((col) => {
      const value = col.lastElementChild as HTMLElement;
      const label = `${col.className}: ${(value.textContent ?? "").trim()}`;
      const problems: string[] = [];
      const range = document.createRange();
      range.selectNodeContents(value);
      const text = range.getBoundingClientRect();
      const box = col.getBoundingClientRect();
      if (text.width > 0 && (text.left < box.left - 0.5 || text.right > box.right + 0.5)) {
        problems.push(`text ${Math.round(text.width)}px in a ${Math.round(box.width)}px column`);
      }
      const walker = document.createTreeWalker(value, NodeFilter.SHOW_TEXT);
      let broken = false;
      for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
        if ((node.textContent ?? "").trim() === "") continue;
        const piece = document.createRange();
        piece.selectNodeContents(node);
        const lines = new Set([...piece.getClientRects()].map((rect) => Math.round(rect.top)));
        if (lines.size > 1) broken = true;
      }
      if (broken || value.getBoundingClientRect().height > slot + 0.5) problems.push("wraps");
      return problems.length === 0 ? [] : [`${label}: ${problems.join(", ")}`];
    });
  });
}

/*
 * Beside the expanded sidebar the list is the viewport less 294px: 1440 and
 * 940 (a 646px list) hold the one-line row, 920 (626px) and 901 wrap it, just
 * either side of 638px. 390 and 320 are phones, and 320 is too narrow for the
 * two columns side by side.
 */
const WIDTHS = [1440, 940, 920, 901, 390, 320];

describe("the notification channel rows", () => {
  for (const role of ["admin", "viewer"] as const) {
    for (const width of WIDTHS) {
      describe(`at ${width}px for ${role === "admin" ? "an admin" : "a viewer"}`, () => {
        let page: Page;
        let list: number;
        let rows: RowMeasure[];

        beforeAll(async () => {
          page = await openChannels(width, role);
          ({ list, rows } = await measureRows(page));
        }, 30_000);

        afterAll(async () => {
          await page?.close();
        });

        it("are all one height", () => {
          expect(rows.length, "the fixture did not render every channel").toBe(CHANNELS.length);
          const heights = Object.fromEntries(rows.map((row) => [row.name, row.height]));
          expect(
            new Set(rows.map((row) => row.height)).size,
            `row heights differ in a ${list}px list: ${JSON.stringify(heights)}`,
          ).toBe(1);
          // All one line or all wrapped, and which one is the list's width.
          expect(new Set(rows.map((row) => row.wrapped)).size).toBe(1);
          expect(rows[0]!.wrapped, `a ${list}px list`).toBe(list < WRAP);
        });

        it("put the column legends on shared lines", () => {
          for (const row of rows) {
            const tops = [...new Set(row.labelTops)].sort((a, b) => a - b);
            const context = `${row.name}: legend tops ${JSON.stringify(row.labelTops)}`;
            if (!row.wrapped) expect(tops.length, context).toBe(1);
            for (let i = 1; i < tops.length; i++) {
              expect(tops[i]! - tops[i - 1]!, context).toBeGreaterThanOrEqual(row.labelHeight - 0.5);
            }
          }
        });

        it("put the name at the same place in every row", () => {
          /*
           * The name line holds chips (Default, quiet hours, Disabled), and a
           * chip is taller than a line of row text: the line grew around it,
           * so a row with a chip stood 2px taller once wrapped, and its name
           * and legends sat 2px lower than in the row above. The name line is
           * now a 24px slot like the value slots, and holds the chip.
           *
           * Within half a pixel rather than exact: a chip centred on the line
           * moves the text's baseline by a tenth of a pixel, which renders on
           * the same device pixel and is not what this checks for.
           */
          const tops = rows.map((row) => row.nameTop);
          const context = `name tops differ: ${JSON.stringify(Object.fromEntries(rows.map((row) => [row.name, row.nameTop])))}`;
          expect(Math.max(...tops) - Math.min(...tops), context).toBeLessThanOrEqual(0.5);
        });

        it("show each value whole inside its column, on one line", async () => {
          /*
           * The other way to hold rows at one height is to cut what does not
           * fit. A type or a date that wraps, or spills out of its column,
           * would pass the checks above and still be wrong.
           */
          expect(await valuesOutOfColumn(page)).toEqual([]);
        });

        it("put each column at the same place in every row", () => {
          const shapes = Object.fromEntries(rows.map((row) => [row.name, row.labelOffsets.join(" ")]));
          expect(
            new Set(Object.values(shapes)).size,
            `legend positions differ between rows in a ${list}px list: ${JSON.stringify(shapes)}`,
          ).toBe(1);
        });
      });
    }
  }

  it("measures the list on both sides of the wrap", async () => {
    // Without this every width could land on one side of 638px and the
    // height checks would prove half of what they say.
    const sides = new Set<boolean>();
    for (const width of WIDTHS) {
      const page = await openChannels(width, "admin");
      try {
        const { list } = await measureRows(page);
        sides.add(list < WRAP);
      } finally {
        await page.close();
      }
    }
    expect([...sides].sort()).toEqual([false, true]);
  }, 60_000);

  it("keep a tested row's name line at the height of the others", async () => {
    /*
     * A test prints its result under the row, which is meant to make that
     * row taller: the result is about that channel and stays beside it. What
     * must not grow is the line above it, where a pass or fail chip joins the
     * name. The harness answers every test as a refusal, so this is the
     * failed chip.
     */
    for (const width of [1440, 390]) {
      const page = await openChannels(width, "admin");
      try {
        await page.click(".inv-row .nt-act-test");
        await page.waitForSelector(".inv-row .inv-result", { timeout: 5_000 });
        const { rows } = await measureRows(page);
        const lines = Object.fromEntries(rows.map((row) => [row.name, row.line]));
        expect(new Set(rows.map((row) => row.line)).size, `at ${width}px: ${JSON.stringify(lines)}`).toBe(1);
      } finally {
        await page.close();
      }
    }
  }, 60_000);
});
