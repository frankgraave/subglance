import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { COMPACT_MAX_WIDTH } from "./useMediaQuery";

/**
 * Node environment on purpose: under jsdom `import.meta.url` is an http URL
 * and `fileURLToPath` refuses it, so a test that reads a file from disk
 * cannot live in the same file as one that renders a component.
 */
describe("the breakpoint", () => {
  it("agrees with the media queries in monitors.css", () => {
    // The component switch happens in React and the chrome around it switches
    // in CSS. Two numbers, one decision — so assert they are the same number
    // rather than trusting a comment.
    const here = fileURLToPath(new URL(".", import.meta.url));
    const css = readFileSync(join(here, "..", "monitors", "monitors.css"), "utf8");
    const widths = [...css.matchAll(/@media\s*\(max-width:\s*(\d+)px\)/g)].map((m) =>
      Number(m[1]),
    );
    expect(widths.length).toBeGreaterThan(0);
    for (const width of widths) expect(width).toBe(COMPACT_MAX_WIDTH);
  });
});
