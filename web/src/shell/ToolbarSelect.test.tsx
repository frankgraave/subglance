// @vitest-environment jsdom
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { IconFilter } from "../components/icons";
import { ToolbarSelect } from "./ToolbarSelect";

afterEach(cleanup);

const webSrc = join(dirname(fileURLToPath(import.meta.url)), "..");

describe("ToolbarSelect", () => {
  it("draws a native select inside a framed label, named by its key", () => {
    render(
      <ToolbarSelect icon={<IconFilter />} label="Type" value="" onChange={() => {}}>
        <option value="">All types</option>
        <option value="http">HTTP</option>
      </ToolbarSelect>,
    );
    const select = screen.getByRole("combobox", { name: "Type" });
    expect(select.tagName).toBe("SELECT");
    const frame = select.closest("label");
    expect(frame?.classList.contains("tb-field")).toBe(true);
    expect(select.classList.contains("tb-select")).toBe(true);
  });

  it("leads with a glyph that a screen reader does not announce", () => {
    render(
      <ToolbarSelect icon={<IconFilter />} label="Type" value="" onChange={() => {}}>
        <option value="">All types</option>
      </ToolbarSelect>,
    );
    const glyph = document.querySelector(".tb-field > svg");
    expect(glyph).not.toBeNull();
    expect(glyph?.getAttribute("aria-hidden")).toBe("true");
    // The key is the whole name: the glyph adds nothing to it.
    expect(screen.getByRole("combobox").getAttribute("aria-label")).toBeNull();
  });

  it("reports the chosen value and keeps a caller's hook class", () => {
    const onChange = vi.fn();
    render(
      <ToolbarSelect icon={<IconFilter />} label="env" facetKey="env" selectClassName="mon-facet-select" value="" onChange={onChange}>
        <option value="">Any</option>
        <option value="prod">prod</option>
      </ToolbarSelect>,
    );
    const select = screen.getByRole("combobox", { name: "env" });
    fireEvent.change(select, { target: { value: "prod" } });
    expect(onChange).toHaveBeenCalledWith("prod");
    expect(select.className).toBe("tb-select mon-facet-select select");
    expect(select.closest("[data-facet-key]")?.getAttribute("data-facet-key")).toBe("env");
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

/**
 * The toolbar's own classes written out by hand. A bare `<select>` portalled
 * into the bar is the other shape; a source scan cannot tell which selects in
 * a file end up in the bar, so `App.masthead.test.tsx` catches that one by
 * walking every route's rendered toolbar.
 */
function handBuilt(source: string): string[] {
  return [...source.matchAll(/\btb-(?:field|label|select)\b/g)].map((match) => match[0]);
}

/*
 * The dashboard framed its tag filters with a glyph while the monitors and
 * incidents screens drew a bare key beside a bordered select, so the same bar
 * held two patterns depending on the screen. A toolbar select is a
 * ToolbarSelect, and this fails on one built any other way.
 */
describe("a toolbar select is a ToolbarSelect, never a hand-built one", () => {
  it("writes the toolbar field only in ToolbarSelect.tsx", () => {
    const offenders = sources()
      .filter(([path]) => path !== join("shell", "ToolbarSelect.tsx"))
      .flatMap(([path, source]) => handBuilt(source).map((shape) => `${path}: ${shape}`));
    expect(offenders).toEqual([]);
  });

  it("recognises each hand-built shape, so the scan cannot pass by matching nothing", () => {
    expect(handBuilt('<label className="tb-field"><span className="tb-label">Type</span>')).toEqual(["tb-field", "tb-label"]);
    expect(handBuilt('<select className="tb-select">')).toEqual(["tb-select"]);
    expect(handBuilt('<select className="input">')).toEqual([]);
    expect(handBuilt(sourceOf("shell/ToolbarSelect.tsx")).length).toBeGreaterThan(0);
  });
});

function sourceOf(path: string): string {
  return readFileSync(join(webSrc, path), "utf8");
}
