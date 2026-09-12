// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { Led } from "./Led";
import type { MonitorStatus } from "./types";

afterEach(cleanup);

/**
 * SUB-47: "paused" and "no data yet" used to be the same grey lamp, which is
 * rule 4 of DESIGN.md broken in the one component the whole product is built
 * around. These tests guard the separation from both ends — the markup that
 * distinguishes them, and the CSS that gives the distinction a shape.
 *
 * The CSS half is asserted by reading led.css rather than computed styles:
 * jsdom does not apply stylesheets, so a computed-style assertion here would
 * pass against an empty file and prove nothing.
 */

const led = (status: MonitorStatus) => {
  const { container } = render(<Led status={status} />);
  const el = container.querySelector(".led");
  if (!el) throw new Error("no lamp rendered");
  return el;
};

describe("Led state mapping", () => {
  it("gives paused its own state, not the one used for no-data-yet", () => {
    expect(led("paused").getAttribute("data-state")).toBe("off");
  });

  it("keeps pending on the amber lamp", () => {
    expect(led("pending").getAttribute("data-state")).toBe("warn");
  });

  it("maps up and down to their own lit states", () => {
    expect(led("up").getAttribute("data-state")).toBe("up");
    expect(led("down").getAttribute("data-state")).toBe("down");
  });

  it("gives every status a distinct state, so no two meanings share a lamp", () => {
    const statuses: MonitorStatus[] = ["up", "down", "pending", "paused"];
    const states = statuses.map((s) => led(s).getAttribute("data-state"));
    expect(new Set(states).size).toBe(statuses.length);
  });

  it("leaves `idle` unclaimed, reserved for having no reading at all", () => {
    // `idle` is the lamp a monitor wears when we hold no measurement for it —
    // the empty dashboard, a placeholder. No *status* may map onto it, or the
    // two meanings collapse back into one signal.
    const statuses: MonitorStatus[] = ["up", "down", "pending", "paused"];
    for (const status of statuses) {
      expect(led(status).getAttribute("data-state"), status).not.toBe("idle");
    }
  });

  it("still says the status in words, since colour never stands alone", () => {
    render(<Led status="paused" />);
    expect(screen.getByText("Paused")).toBeTruthy();
  });
});

// Resolved from the vitest root (web/) rather than `import.meta.url`: under
// the jsdom environment that URL is not a file: URL and cannot be converted.
const ledCss = readFileSync(join(process.cwd(), "src/monitors/led.css"), "utf8");

/** The body of one `.led[data-state="…"]` rule. */
function rule(state: string, pseudo = ""): string {
  const selector = `.led[data-state="${state}"]${pseudo}`;
  const at = ledCss.indexOf(selector);
  expect(at, `missing rule for ${selector}`).toBeGreaterThan(-1);
  return ledCss.slice(at, ledCss.indexOf("}", at));
}

describe("led.css draws off as an empty socket", () => {
  it("leaves the off lamp unfilled while idle stays filled", () => {
    expect(rule("off")).toContain("background: transparent");
    expect(rule("idle")).toContain("background: var(--idle)");
  });

  it("rings the off lamp in --ink-2, which clears the 3:1 non-text floor", () => {
    // --idle is 2.5:1 on the dark canvas and 1.7:1 on a light card; a ring in
    // it would be a signal only some people can see. See DESIGN.md §3.1.
    expect(rule("off")).toContain("var(--ink-2)");
  });

  it("rings with an inset shadow, never a border, so the lamp stays 20x7", () => {
    expect(rule("off")).toContain("inset 0 0 0");
    expect(rule("off")).not.toContain("border:");
  });

  it("removes the lens highlight entirely, since nothing is lit behind it", () => {
    expect(rule("off", "::after")).toContain("opacity: 0");
  });
});
