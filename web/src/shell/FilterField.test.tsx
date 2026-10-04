// @vitest-environment jsdom
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FilterField } from "./FilterField";

afterEach(cleanup);

const webSrc = join(dirname(fileURLToPath(import.meta.url)), "..");

describe("FilterField", () => {
  it("is a search box named by its label, not by its placeholder", () => {
    render(<FilterField label="Filter channels by name or type" placeholder="Filter channels…" value="" onChange={() => {}} />);
    const box = screen.getByRole("searchbox", { name: "Filter channels by name or type" });
    expect(box.getAttribute("placeholder")).toBe("Filter channels…");
    // The whole frame is the label, so a tap anywhere on it focuses the field.
    expect(box.closest("label")?.classList.contains("shell-search")).toBe(true);
  });

  it("reports the typed text, not the event", () => {
    const onChange = vi.fn();
    render(<FilterField label="Filter settings" placeholder="Filter settings…" value="" onChange={onChange} />);
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter settings" }), { target: { value: "tokens" } });
    expect(onChange).toHaveBeenCalledWith("tokens");
  });

  it("adds a caller's class to the frame and keeps its own", () => {
    render(<FilterField className="nt-filter" label="Filter" placeholder="Filter…" value="" onChange={() => {}} />);
    expect(screen.getByRole("searchbox").closest("label")?.className).toBe("shell-search nt-filter");
  });
});

/** Every non-test .tsx under web/src, as [path relative to web/src, source]. */
function sources(dir = webSrc): [string, string][] {
  return readdirSync(dir).flatMap((entry) => {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) return sources(path);
    if (!entry.endsWith(".tsx") || entry.includes(".test.")) return [];
    return [[relative(webSrc, path), readFileSync(path, "utf8")] as [string, string]];
  });
}

/** A filter field written out by hand: the input type, or the field's own class. */
function handBuilt(source: string): string[] {
  return [...source.matchAll(/type\s*=\s*\{?\s*["'`]search["'`]|\bshell-search-input\b/g)].map((match) => match[0]);
}

/*
 * Five screens wrote this field out by hand, and moving one out of the page
 * toolbar meant carrying the shell's classes into a feature. One component
 * now draws every one, and this fails on a sixth built any other way.
 */
describe("a filter field is a FilterField, never a hand-built one", () => {
  it("writes a search input only in FilterField.tsx", () => {
    const offenders = sources()
      .filter(([path]) => path !== join("shell", "FilterField.tsx"))
      .flatMap(([path, source]) => handBuilt(source).map((shape) => `${path}: ${shape}`));
    expect(offenders).toEqual([]);
  });

  it("recognises each hand-built shape, so the scan cannot pass by matching nothing", () => {
    expect(handBuilt('<input type="search" />')).toEqual(['type="search"']);
    expect(handBuilt("<input type={'search'} />")).toEqual(["type={'search'"]);
    expect(handBuilt('<input className="shell-search-input" />')).toEqual(["shell-search-input"]);
    expect(handBuilt('<input type="text" className="input" />')).toEqual([]);
    expect(handBuilt(readFileSync(join(webSrc, "shell", "FilterField.tsx"), "utf8")).length).toBeGreaterThan(0);
  });
});
