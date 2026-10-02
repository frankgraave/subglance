/**
 * The incident rows show the demo estate's names whole, and their columns
 * line up with and without the mute control (SUB-194).
 *
 * An incident row is read to find out what is broken, and the monitor's name
 * is the only thing that tells one row from the next. The line used to give
 * four fixed columns 544px before the name got anything: at 1440 beside the
 * expanded sidebar a name had 130px, at 1024 it had 14, and at 820 none at
 * all. A row without the mute control also took the control's width for
 * itself, so its columns sat 58px right of the row above. This file holds
 * the name, the error and the columns in place.
 *
 * The fixture is the estate `make seed` creates, read out of
 * `cmd/seed/catalogue.go`: every seed monitor gets an incident, with the
 * seed's own failure texts, so every name the demo shows is measured and a
 * longer one added later is measured too. Each list mixes rows that offer the
 * mute control with rows that do not (a warning nobody was paged for), which
 * is the case the columns used to disagree on.
 *
 * 820 is a tablet beside the expanded sidebar, 1024 a small laptop, 1440 the
 * desktop. Someone who can edit, because the mute control is theirs.
 *
 * Does not run with `npm test`: needs a built bundle and a browser.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { seedEstate, seedFailures } from "./harness/seed";
import { serveBuild, type Server } from "./harness/server";
import type { ApiIncident } from "../monitors/detail";

const ESTATE = seedEstate();
const FAILURES = seedFailures();
const NOW = Date.now();
const MINUTE = 60_000;
const iso = (at: number) => new Date(at).toISOString();

/**
 * One incident per seed monitor, cycling through the seed's failure texts.
 *
 * Even monitors are open, odd ones resolved, so both cards on the incidents
 * screen hold seed names. Every third open one is an unconfirmed warning,
 * which has no mute control: those are the rows whose columns used to shift.
 * The start times are minutes apart, never 60 seconds, so no cluster forms
 * and every incident is drawn as its own row.
 */
const INCIDENTS: ApiIncident[] = ESTATE.map((monitor, i) => {
  const failure = FAILURES[i % FAILURES.length]!;
  const startedAt = NOW - (i + 1) * 7 * MINUTE;
  const resolved = i % 2 === 1;
  const confirmed = resolved || i % 3 !== 0;
  return {
    id: 1000 + i,
    monitor_id: Number(monitor.id),
    started_at: iso(startedAt),
    ...(confirmed ? { confirmed_at: iso(startedAt + MINUTE) } : {}),
    ...(resolved ? { resolved_at: iso(startedAt + 3 * MINUTE) } : {}),
    confirmed,
    resolved,
    acked: false,
    duration_s: resolved ? 180 : Math.round((NOW - startedAt) / 1000),
    cause: failure.cause,
    last_error: failure.message,
  };
});

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

async function open(width: number, path: string): Promise<Page> {
  const page = await browser.newPage();
  await page.evaluateOnNewDocument(() => localStorage.setItem("subglance:sidebar", "expanded"));
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.setRequestInterception(true);
  page.on("request", async (request) => {
    const url = new URL(request.url());
    const json = (body: unknown) =>
      request.respond({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    const perMonitor = /^\/api\/v1\/monitors\/(\d+)\/incidents$/.exec(url.pathname);
    if (url.origin !== server.url) {
      await request.abort("blockedbyclient");
    } else if (url.pathname === "/api/v1/auth/me") {
      await json({ id: 1, email: "operator@example.com", role: "admin", created_at: iso(NOW) });
    } else if (url.pathname === "/api/v1/monitors" && request.method() === "GET") {
      await json({ monitors: ESTATE });
    } else if (url.pathname === "/api/v1/incidents") {
      await json({ incidents: INCIDENTS.filter((incident) => !incident.resolved) });
    } else if (url.pathname === "/api/v1/incidents/resolved") {
      await json({ incidents: INCIDENTS.filter((incident) => incident.resolved), has_more: false, days: 30 });
    } else if (perMonitor) {
      await json({ incidents: INCIDENTS.filter((incident) => incident.monitor_id === Number(perMonitor[1])) });
    } else {
      await request.continue();
    }
  });
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".inc-row", { timeout: 15_000 });
  // The bundled faces are `font-display: block`: measure the shipped face,
  // not the fallback that happened to be on screen first.
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  await page.evaluate(
    () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
  );
  return page;
}

type Line = {
  name: string;
  nameClipped: boolean;
  /** The line's own box, and where its error ends. */
  lineLeft: number;
  lineWidth: number;
  contentRight: number;
  errorClipped: boolean;
  errorRight: number;
  /** Where each number column starts, relative to the line. */
  time: number;
  dur: number;
  ack: number;
  hasButton: boolean;
};

/** Every incident line on the page, measured. */
async function lines(page: Page): Promise<Line[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>(".inc-row")].map((row) => {
      const line = row.querySelector<HTMLElement>(".inc-line")!;
      const box = line.getBoundingClientRect();
      const at = (selector: string) =>
        Math.round(row.querySelector<HTMLElement>(selector)!.getBoundingClientRect().left - box.left);
      const name = row.querySelector<HTMLElement>(".inc-name")!;
      const error = row.querySelector<HTMLElement>(".inc-sub")!;
      return {
        name: (name.textContent ?? "").trim(),
        nameClipped: name.scrollWidth > name.clientWidth,
        lineLeft: Math.round(box.left),
        lineWidth: Math.round(box.width),
        contentRight: Math.round(box.right - parseFloat(getComputedStyle(line).paddingRight)),
        errorClipped: error.scrollWidth > error.clientWidth,
        errorRight: Math.round(error.getBoundingClientRect().right),
        time: at(".inc-col-time"),
        dur: at(".inc-col-dur"),
        ack: at(".inc-col-ack"),
        hasButton: row.querySelector(".inc-act button") !== null,
      };
    }),
  );
}

describe("the seed estate in the incident rows", () => {
  it("is read from the seed catalogue", () => {
    // If the parser stops finding them, every check below passes on nothing.
    expect(ESTATE.length).toBeGreaterThanOrEqual(20);
    expect(FAILURES.length).toBeGreaterThanOrEqual(5);
    expect(INCIDENTS.some((incident) => !incident.resolved && !incident.confirmed)).toBe(true);
  });

  describe.each([820, 1024, 1440])("at %ipx", (width) => {
    it("shows every monitor name whole on the incidents screen", async () => {
      const page = await open(width, "/incidents");
      try {
        const measured = await lines(page);
        expect(measured.length, "every seed incident must render as its own row").toBe(INCIDENTS.length);
        expect(measured.filter((line) => line.nameClipped).map((line) => line.name)).toEqual([]);
      } finally {
        await page.close();
      }
    });

    it("lines the columns up with and without the mute control", async () => {
      const page = await open(width, "/incidents");
      try {
        const measured = await lines(page);
        expect(measured.some((line) => line.hasButton), "the fixture must offer the control").toBe(true);
        expect(measured.some((line) => !line.hasButton), "and must have rows without it").toBe(true);
        // One width for every line on the screen, open and history alike,
        // and every number column at one offset inside it.
        for (const key of ["lineLeft", "lineWidth", "time", "dur", "ack"] as const) {
          expect(new Set(measured.map((line) => line[key])), `${key} differs between rows`).toHaveLength(1);
        }
      } finally {
        await page.close();
      }
    });

    it("cuts an error only where the line ends", async () => {
      const page = await open(width, "/incidents");
      try {
        const measured = await lines(page);
        // An error with an ellipsis must have been given the line's whole
        // remaining width, not a column's.
        const starved = measured
          .filter((line) => line.errorClipped && line.errorRight < line.contentRight - 1)
          .map((line) => `${line.name}: error ends at ${line.errorRight}, the line at ${line.contentRight}`);
        expect(starved).toEqual([]);
        if (width >= 1440) {
          expect(measured.filter((line) => line.errorClipped).map((line) => line.name)).toEqual([]);
        }
      } finally {
        await page.close();
      }
    });

    it("shows when each outage began whole on a monitor's own page", async () => {
      // On a monitor's page the name slot holds the outage's start, a date
      // and a time, which is the widest thing the slot ever holds.
      const page = await open(width, `/monitors/${ESTATE[0]!.id}`);
      try {
        const measured = await lines(page);
        expect(measured.length).toBeGreaterThan(0);
        expect(measured.filter((line) => line.nameClipped).map((line) => line.name)).toEqual([]);
      } finally {
        await page.close();
      }
    });
  });
});
