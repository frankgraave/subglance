import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * A class belongs to the feature whose stylesheet defines it (SUB-173).
 *
 * The settings forms used to be built from the sign-in screen's `auth-*`
 * classes, the add-monitor form's `add-*` classes and the monitors
 * inventory's `inv-act` row actions. A change to the sign-in form's spacing,
 * or to the add-monitor form's buttons, then moved every settings form, and
 * nothing in the markup said so. SUB-171 removed the same coupling from card
 * frames; this guard holds it for every class.
 *
 * The rule: a .tsx may wear a class defined in its own feature directory, or
 * in a shared one (`components/`, `styles/`, or a stylesheet at the root of
 * `src/`). A class whose only stylesheets sit in another feature directory is
 * borrowed, and fails here unless the exception map below names it with a
 * reason. A class no stylesheet defines is a hook for a test, not a style
 * borrowed from anywhere, and is ignored.
 */

const webSrc = join(fileURLToPath(new URL(".", import.meta.url)), "..");
const SHARED = new Set(["components", "styles", "(root)"]);

/**
 * Borrowed on purpose. Keyed by class; the reason is what a reviewer can
 * disagree with. A companion test fails when an entry stops matching a live
 * borrowing, so the list cannot rot into reasons for code that is gone.
 */
const INVENTORY_ROW =
  "A channel row is the monitors inventory row: an identity, settings columns under a legend, inline actions (notifications.css says why). The row is one object on two screens, so it keeps one stylesheet.";
const DETAIL_COLUMN =
  "The monitor detail's page column and empty states, which the incidents, notifications and missing-monitor screens take so every screen reads at one measure.";
const INCIDENT_ROWS =
  "The monitor detail lists that monitor's incidents with the incidents screen's own rows and notices, so the two lists cannot drift apart.";
const PAGE_TOOLBAR =
  "The page toolbar is the shell's, and each view fills its portal slot with the shell's own toolbar fields (AGENTS.md, Where a control belongs).";
const SHELL_ICON =
  "The status wall's exit is the shell's icon button: the wall replaces the shell, and its one control should look like the one it stands in for.";
const BRAND_LAMP =
  "The brand mark beside the product name is the status lamp (DESIGN.md §3), drawn without the Led component because no monitor stands behind it.";
const TOOLTIP =
  "The heartbeat bar's tooltip is the product's one chart tooltip; the latency chart and the style guide's token sheet show the same object.";

const exceptions = new Map<string, string>([
  ["inv-col", INVENTORY_ROW],
  ["inv-label", INVENTORY_ROW],
  ["inv-name", INVENTORY_ROW],
  ["inv-paused-chip", INVENTORY_ROW],
  ["inv-result", INVENTORY_ROW],
  ["inv-result--bad", INVENTORY_ROW],
  ["inv-sub", INVENTORY_ROW],
  ["inv-type", INVENTORY_ROW],
  ["mon-detail", DETAIL_COLUMN],
  ["mon-detail-back", DETAIL_COLUMN],
  ["mon-detail-empty", DETAIL_COLUMN],
  ["mon-detail-empty--quiet", DETAIL_COLUMN],
  ["mon-detail-nav", DETAIL_COLUMN],
  ["mon-detail-note", DETAIL_COLUMN],
  ["mon-result-count", DETAIL_COLUMN],
  ["inc-churn", INCIDENT_ROWS],
  ["inc-list", INCIDENT_ROWS],
  ["inc-notice", INCIDENT_ROWS],
  ["shell-search", PAGE_TOOLBAR],
  ["shell-search-input", PAGE_TOOLBAR],
  ["tb-count", PAGE_TOOLBAR],
  ["tb-field", PAGE_TOOLBAR],
  ["tb-field--framed", PAGE_TOOLBAR],
  ["tb-group", PAGE_TOOLBAR],
  ["tb-label", PAGE_TOOLBAR],
  ["tb-select", PAGE_TOOLBAR],
  ["shell-icon", SHELL_ICON],
  ["shell-icon-btn", SHELL_ICON],
  ["led", BRAND_LAMP],
  ["hb-tooltip", TOOLTIP],
  ["conn-badge", "The host-offline line wears the connection badge's look on purpose: both say whether the data on screen can be taken at face value (HostOffline.tsx)."],
  ["conn-badge-age", "As conn-badge: the host-offline line is the connection badge's second state."],
  ["conn-badge-dot", "As conn-badge: the host-offline line is the connection badge's second state."],
]);

/**
 * Files exempt as a whole. The style guide's specimens render every
 * feature's components by their real class names; styleguide.test.ts checks
 * that they do, so borrowing is the file's job.
 */
const exemptFiles = new Set(["styleguide-specimens.tsx"]);

function files(dir: string, extension: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) out.push(...files(full, extension));
    else if (entry.endsWith(extension) && !entry.includes(".test.")) out.push(full);
  }
  return out;
}

/** The feature a file belongs to: its top directory under src/. */
function feature(file: string): string {
  const parts = relative(webSrc, file).split(sep);
  return parts.length === 1 ? "(root)" : parts[0];
}

/** Each class a stylesheet defines, with the features that define it. */
function classHomes(): Map<string, Set<string>> {
  const homes = new Map<string, Set<string>>();
  for (const file of files(webSrc, ".css")) {
    const body = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    for (const [, chunk] of body.matchAll(/([^{}]+)\{/g)) {
      // Everything after the last statement (`@import ...;`, a declaration
      // before a nested rule) is the selector; an at-rule's prelude names no
      // class.
      const selector = chunk.slice(chunk.lastIndexOf(";") + 1);
      if (/^\s*@/.test(selector)) continue;
      for (const [, name] of selector.matchAll(/\.([a-zA-Z_][\w-]*)/g)) {
        const owners = homes.get(name) ?? new Set<string>();
        owners.add(feature(file));
        homes.set(name, owners);
      }
    }
  }
  return homes;
}

/**
 * The class names a source file writes into a `className` (or a
 * `...ClassName` prop, or a `className:` in a props object).
 *
 * A quoted value is read whole. An expression is scanned to its closing
 * brace and every string literal inside it is read, so both halves of
 * `open ? "a b" : "c"` count; `${...}` holes in a template are skipped.
 */
function classesIn(source: string): string[] {
  const out: string[] = [];
  const take = (text: string) => out.push(...text.replace(/\$\{[^}]*\}/g, " ").split(/\s+/).filter(Boolean));
  for (const match of source.matchAll(/\b(?:className|[a-z]+ClassName)\s*(=|:)\s*/g)) {
    let i = match.index + match[0].length;
    const open = source[i];
    if (open === '"' || open === "'" || open === "`") {
      const end = source.indexOf(open, i + 1);
      take(source.slice(i + 1, end));
      continue;
    }
    if (open !== "{") continue;
    let depth = 0;
    for (; i < source.length; i++) {
      const c = source[i];
      if (c === "{") depth++;
      else if (c === "}") { if (--depth === 0) break; }
      else if (c === '"' || c === "'" || c === "`") {
        const end = source.indexOf(c, i + 1);
        take(source.slice(i + 1, end));
        i = end;
      }
    }
  }
  return out;
}

/** Every class a feature wears from another feature, as "class <- file". */
function borrowings(): Map<string, string[]> {
  const homes = classHomes();
  const found = new Map<string, string[]>();
  for (const file of files(webSrc, ".tsx")) {
    if (exemptFiles.has(relative(webSrc, file))) continue;
    const own = feature(file);
    for (const name of new Set(classesIn(readFileSync(file, "utf8")))) {
      const owners = homes.get(name);
      if (!owners || owners.has(own) || [...owners].some((o) => SHARED.has(o))) continue;
      found.set(name, [...(found.get(name) ?? []), `${relative(webSrc, file)} (styled in ${[...owners].join(", ")})`]);
    }
  }
  return found;
}

describe("class ownership (SUB-173)", () => {
  it("reads classes out of quoted values, expressions and props objects", () => {
    expect(classesIn('<p className="a b">')).toEqual(["a", "b"]);
    expect(classesIn('<b className={open ? "c d" : "e"} />')).toEqual(["c", "d", "e"]);
    expect(classesIn("<i className={`f ${x ? \"g\" : \"h\"}`} />")).toEqual(["f"]);
    expect(classesIn('const p = { className: "i" }; <Menu triggerClassName="j" />')).toEqual(["i", "j"]);
  });

  it("finds the shared controls in a shared stylesheet", () => {
    // A scanner that stops finding classes would pass the next test
    // vacuously; these are the ones SUB-173 moved.
    const homes = classHomes();
    for (const name of ["button", "icon-button", "input", "field", "field-label", "stack", "button-solid", "warn-note", "sr-only"]) {
      expect([...(homes.get(name) ?? [])], name).toContain("components");
    }
  });

  it("does not let a screen wear another feature's class", () => {
    const unexplained = [...borrowings()].filter(([name]) => !exceptions.has(name))
      .map(([name, where]) => `.${name} <- ${where.join("; ")}`);
    expect(unexplained).toEqual([]);
  });

  it("has no exception for a borrowing that no longer happens", () => {
    const live = borrowings();
    expect([...exceptions.keys()].filter((name) => !live.has(name))).toEqual([]);
  });
});
