import { readFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * The tab icon is the product mark, named by the shell.
 *
 * Without a link the browser asks the root of the host for /favicon.ico on
 * its own. At the root of a host that request answered the shell as
 * text/html, so the tab showed a blank-page glyph; under a path prefix it
 * left the prefix altogether and landed on whatever else that host serves at
 * /. Naming a file in the shell, root-written like the font preloads, is what
 * lets the build make it relative and the server keep it under the prefix.
 */

const webRoot = join(fileURLToPath(new URL(".", import.meta.url)), "..", "..");
const indexHtml = readFileSync(join(webRoot, "index.html"), "utf8");
const iconPath = join(webRoot, "public", "favicon.svg");
const tokensCss = readFileSync(join(webRoot, "src", "styles", "tokens.css"), "utf8");

function iconLinks(): string[] {
  return [...indexHtml.matchAll(/<link\b[^>]*>/g)]
    .map((m) => m[0])
    .filter((tag) => /\srel="(?:[^"]*\s)?icon(?:\s[^"]*)?"/.test(tag));
}

describe("the tab icon", () => {
  it("is named once by the shell, as an SVG written from the root", () => {
    const links = iconLinks();
    expect(links, "index.html names no rel=icon link").toHaveLength(1);
    expect(links[0]).toMatch(/\shref="\/favicon\.svg"/);
    expect(links[0]).toMatch(/\stype="image\/svg\+xml"/);
  });

  it("is shipped as a file the build copies beside the shell", () => {
    expect(existsSync(iconPath), "web/public/favicon.svg is missing").toBe(true);
  });

  it("is a static picture: no script, no handler, nothing loaded from elsewhere", () => {
    const svg = readFileSync(iconPath, "utf8");
    expect(svg.startsWith("<svg ")).toBe(true);
    expect(svg).not.toMatch(/<script\b/i);
    expect(svg).not.toMatch(/\son[a-z]+\s*=/i);
    expect(svg).not.toMatch(/<foreignObject\b/i);
    // The namespace declaration is the one URL an SVG must carry.
    const urls = [...svg.matchAll(/(?:href|url\()\s*=?\s*["']?([^"')\s]+)/gi)].map((m) => m[1]);
    expect(urls).toEqual([]);
  });

  it("draws its lamp in the dark theme's up colour, the tile the sidebar draws it on", () => {
    // The tile stays dark in both themes: it sits on the browser's own tab
    // strip, which follows the system, not the product's theme switch. The
    // lamp is the one colour the mark is about, so it must not drift from the
    // token the sidebar's lamp is lit with.
    const dark = tokensCss.slice(tokensCss.indexOf('[data-theme="dark"]'));
    const up = /--up:\s*(#[0-9a-f]{6});/i.exec(dark)?.[1];
    expect(up, "no --up in the dark theme").toBeDefined();
    const svg = readFileSync(iconPath, "utf8");
    expect(svg.toLowerCase()).toContain(`fill="${up!.toLowerCase()}"`);
  });
});
