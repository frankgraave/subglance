// @vitest-environment jsdom
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { FieldError } from "./FieldError";

afterEach(cleanup);

const webSrc = join(dirname(fileURLToPath(import.meta.url)), "..");

describe("FieldError", () => {
  it("announces the refusal and keeps the glyph out of its name", () => {
    render(<FieldError id="name-error">The name is already taken.</FieldError>);
    const alert = screen.getByRole("alert");
    expect(alert.id).toBe("name-error");
    expect(alert.className).toBe("field-error");
    expect(alert.textContent).toBe("The name is already taken.");
    // The glyph marks the line for the eye; a reader is told by the words.
    const glyph = alert.querySelector(":scope > svg");
    expect(glyph?.getAttribute("aria-hidden")).toBe("true");
    expect(alert.querySelector(":scope > span")?.textContent).toBe("The name is already taken.");
  });

  it("stays quiet when the reader did nothing to cause it", () => {
    render(<FieldError announce={false}>Could not load channels.</FieldError>);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(document.querySelector(".field-error > svg")).not.toBeNull();
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

/** The refusal's class written into markup: a className string or template that names it. */
function handBuilt(source: string): string[] {
  return [...source.matchAll(/className\s*=\s*\{?\s*["'`][^"'`]*\bfield-error\b/g)].map((match) => match[0]);
}

/*
 * Twenty-nine refusals were written out by hand as `<p className="field-error">`.
 * Some carried the alert glyph and some did not, and three features overrode
 * the ink locally to get a sentence that could be read. One component now
 * draws every refusal, and this fails on one built any other way.
 */
describe("a refusal is a FieldError, never a hand-built one", () => {
  it("writes the field-error class only in FieldError.tsx", () => {
    const offenders = sources()
      .filter(([path]) => path !== join("components", "FieldError.tsx"))
      .flatMap(([path, source]) => handBuilt(source).map((shape) => `${path}: ${shape}`));
    expect(offenders).toEqual([]);
  });

  it("has an owner that really is the only writer", () => {
    const own = readFileSync(join(webSrc, "components", "FieldError.tsx"), "utf8");
    expect(handBuilt(own)).toEqual(['className="field-error']);
  });

  it("bites on each hand-built form in a fixture", () => {
    expect(handBuilt(`
      <p className="field-error" role="alert">x</p>
      <p className={"field-error"}>x</p>
      <p className={\`field-error \${extra}\`}>x</p>
      <p className="note field-error">x</p>
      <FieldError>x</FieldError>
      <p className="field-errors">x</p>
      <input aria-describedby={\`\${ids}-field-error\`} />
    `)).toHaveLength(4);
  });
});

/*
 * No feature stylesheet sets the refusal's ink: three did, to get a sentence
 * that could be read, and the shared rule now draws that for everyone.
 */
describe("no feature stylesheet restyles a refusal", () => {
  function stylesheets(dir = webSrc): [string, string][] {
    return readdirSync(dir).flatMap((entry) => {
      const path = join(dir, entry);
      if (statSync(path).isDirectory()) return stylesheets(path);
      if (!entry.endsWith(".css")) return [];
      return [[relative(webSrc, path), readFileSync(path, "utf8")] as [string, string]];
    });
  }

  it("names .field-error only in components/controls.css", () => {
    const offenders = stylesheets()
      .filter(([path]) => path !== join("components", "controls.css"))
      .filter(([, source]) => /\.field-error\b/.test(source.replace(/\/\*[\s\S]*?\*\//g, "")))
      .map(([path]) => path);
    expect(offenders).toEqual([]);
  });
});
