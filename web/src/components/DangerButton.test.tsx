// @vitest-environment jsdom
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DangerButton } from "./DangerButton";

afterEach(cleanup);

const webSrc = join(dirname(fileURLToPath(import.meta.url)), "..");

describe("DangerButton", () => {
  it("is named by its words and carries the bin as a hidden glyph", () => {
    const onClick = vi.fn();
    render(<DangerButton onClick={onClick}>Delete auth</DangerButton>);
    const button = screen.getByRole("button", { name: "Delete auth" }) as HTMLButtonElement;
    expect(button.className).toBe("button button--danger");
    // A plain button by default, so one inside a form does not submit it.
    expect(button.type).toBe("button");
    expect(button.textContent).toBe("Delete auth");
    const glyph = button.querySelector(":scope > svg");
    expect(glyph?.getAttribute("aria-hidden")).toBe("true");
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("submits its form when asked to, and stays inert when disabled", () => {
    render(<DangerButton type="submit" disabled>Delete all data</DangerButton>);
    const button = screen.getByRole("button", { name: "Delete all data" }) as HTMLButtonElement;
    expect(button.type).toBe("submit");
    expect(button.disabled).toBe(true);
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
 * The danger class written into markup on anything but a row's bin: a
 * className string or template that names `button--danger` without
 * `icon-button`. A bin is a glyph alone, so it has no word to colour.
 */
function handBuilt(source: string): string[] {
  return [...source.matchAll(/className\s*=\s*\{?\s*["'`]([^"'`]*\bbutton--danger\b[^"'`]*)/g)]
    .filter((match) => !/\bicon-button\b/.test(match[1]))
    .map((match) => match[0]);
}

/*
 * The worded danger button was written out twice by hand, and both drew their
 * label in the failure red, which a label cannot be read in on the dark
 * button surface. One component now draws every worded one, and this fails on
 * one built any other way.
 */
describe("a worded danger button is a DangerButton, never a hand-built one", () => {
  it("writes the worded danger class only in DangerButton.tsx", () => {
    const offenders = sources()
      .filter(([path]) => path !== join("components", "DangerButton.tsx"))
      .flatMap(([path, source]) => handBuilt(source).map((shape) => `${path}: ${shape}`));
    expect(offenders).toEqual([]);
  });

  it("has an owner that really is the only writer", () => {
    const own = readFileSync(join(webSrc, "components", "DangerButton.tsx"), "utf8");
    expect(handBuilt(own)).toEqual(['className="button button--danger']);
  });

  it("bites on each hand-built form in a fixture and spares a row's bin", () => {
    expect(handBuilt(`
      <button className="button button--danger">x</button>
      <button className={"button button--danger"}>x</button>
      <button className={\`button button--danger \${extra}\`}>x</button>
      <button className="button--danger button">x</button>
      <button className="icon-button button--danger">x</button>
      <button className="button button--dangerous">x</button>
      <DangerButton>x</DangerButton>
    `)).toHaveLength(4);
  });
});
