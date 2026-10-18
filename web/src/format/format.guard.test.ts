import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { UPTIME_DECIMALS } from "./format";

/*
 * Every date, time, count and percentage on screen is written by
 * `format/format.ts`. The app had about a dozen `toLocale*` calls, each with
 * its own options, and four date shapes on one settings page; this fails on
 * the next one instead of leaving it for a review to notice.
 */

const webSrc = join(dirname(fileURLToPath(import.meta.url)), "..");
const HOME = join("format", "format.ts");

/** Every non-test .ts/.tsx under web/src, as [path relative to web/src, source]. */
function sources(dir = webSrc): [string, string][] {
  return readdirSync(dir).flatMap((entry) => {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) return sources(path);
    if (!/\.tsx?$/.test(entry) || entry.includes(".test.")) return [];
    return [[relative(webSrc, path), readFileSync(path, "utf8")] as [string, string]];
  });
}

/**
 * The shapes of a formatter written in place: a locale call, an `Intl`
 * date or number formatter, a clock face assembled from a Date's hours, a
 * percentage built from `toFixed`, and an API timestamp put into text as it
 * came off the wire.
 *
 * The last one formats nothing, which is how it got past the others: the
 * maintenance list printed `2026-10-01T20:00:00Z → 2026-10-01T21:00:00Z`,
 * in UTC and in ISO, with no call for a scan to see. A field named `*_at`
 * inside `${…}` or as a JSX child is that shape; one handed to a prop
 * (`value={x.started_at}`, `dateTime={…}`) is not, since the component it
 * goes to does the writing.
 */
const SHAPES = [
  /\.toLocale(?:Date|Time)?String\(/g,
  /\bIntl\.(?:DateTimeFormat|NumberFormat|RelativeTimeFormat)\b/g,
  /\.get(?:UTC)?Hours\(\)/g,
  /\.toFixed\([^)]*\)\}?%/g,
  /(?<![=\w])\$?\{[\w.?]+_at\}/g,
];

function formatters(source: string): string[] {
  return SHAPES.flatMap((shape) => [...source.matchAll(shape)].map((match) => match[0]));
}

/**
 * Uses that are not formatting, keyed `path: match`, each with its reason.
 * A key that stops matching live code fails the companion test below, so the
 * list cannot outlive the code it excuses.
 */
const EXCEPTIONS: Record<string, string> = {
  // Asks the engine whether a typed zone name exists; nothing is written.
  "notifications/quietHours.ts: Intl.DateTimeFormat":
    "validates a quiet-hours time zone and reads the browser's own zone; neither formats a date",
  // The clipboard text for a bug report, read by whoever triages it in
  // another zone: the ISO instant is the unambiguous shape there.
  "diagnostics/format.ts: ${d.started_at}":
    "the copied diagnostics are for a bug report, where an ISO instant with its offset is the point",
};

describe("dates, times, counts and percentages are written by format/format.ts", () => {
  it("formats nothing in place anywhere else", () => {
    const offenders = sources()
      .filter(([path]) => path !== HOME)
      .flatMap(([path, source]) => formatters(source).map((shape) => `${path}: ${shape}`))
      .filter((key) => !(key in EXCEPTIONS));
    expect(offenders).toEqual([]);
  });

  it("keeps every exception in use, so the list cannot rot", () => {
    const live = new Set(sources().flatMap(([path, source]) => formatters(source).map((shape) => `${path}: ${shape}`)));
    expect(Object.keys(EXCEPTIONS).filter((key) => !live.has(key))).toEqual([]);
  });

  it("recognises each in-place shape, so the scan cannot pass by matching nothing", () => {
    expect(formatters("d.toLocaleString(undefined, {})")).toEqual([".toLocaleString("]);
    expect(formatters("d.toLocaleDateString()")).toEqual([".toLocaleDateString("]);
    expect(formatters("d.toLocaleTimeString([], {})")).toEqual([".toLocaleTimeString("]);
    expect(formatters("n.toLocaleString()")).toEqual([".toLocaleString("]);
    expect(formatters('new Intl.NumberFormat("en")')).toEqual(["Intl.NumberFormat"]);
    expect(formatters("new Intl.DateTimeFormat(undefined, {})")).toEqual(["Intl.DateTimeFormat"]);
    expect(formatters("`${pad(d.getHours())}:${pad(d.getMinutes())}`")).toEqual([".getHours()"]);
    expect(formatters("`${share.toFixed(1)}%`")).toEqual([".toFixed(1)}%"]);
    expect(formatters("`${window.starts_at} → ${window.ends_at}`")).toEqual(["${window.starts_at}", "${window.ends_at}"]);
    expect(formatters("<dd>{data.last_success_at}</dd>")).toEqual(["{data.last_success_at}"]);
    expect(formatters("<When value={data.last_success_at} />")).toEqual([]);
    expect(formatters("<time dateTime={d.started_at}>")).toEqual([]);
    expect(formatters("`${d.started}`")).toEqual([]);
    expect(formatters('new Intl.Collator("en")')).toEqual([]);
    expect(formatters("`${top}%`")).toEqual([]);
    expect(formatters(readFileSync(join(webSrc, HOME), "utf8")).length).toBeGreaterThan(0);
  });
});

/*
 * The public status page prints its uptime on the server, in Go, so the one
 * precision rule crosses a language boundary. Reading the format string is
 * crude, and it is the cheapest check that fails when one side changes alone.
 */
describe("the status page writes uptime to the app's precision", () => {
  it("formats its figure with as many decimals as formatUptime", () => {
    const render = readFileSync(join(webSrc, "..", "..", "internal", "statuspage", "render.go"), "utf8");
    // The figure is formatted with a named precision, since the words around
    // it now come from a per-language table (internal/statuspage/texts.go).
    expect(render).toContain(`const uptimeDecimals = ${UPTIME_DECIMALS}\n`);
    expect(render).toContain("strconv.FormatFloat(*pct, 'f', uptimeDecimals, 64)");
  });
});
