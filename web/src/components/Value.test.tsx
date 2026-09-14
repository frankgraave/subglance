// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Value } from "./Value";

/**
 * SUB-106 §10: a reading is drawn as a reading.
 *
 * The assertions below are on the attributes the stylesheet keys off, not on
 * computed colour. That is deliberate: the tones live in tokens.css, which
 * jsdom does not load, so a test that read back a colour would be asserting
 * the empty string and proving nothing.
 *
 * The distinction worth the most here is the one between a zero and a missing
 * value. Both step back from a live reading, and they must step back to
 * different places — "the answer was zero" and "nothing was measured" are
 * opposite claims about the same monitor.
 */

afterEach(cleanup);

describe("Value", () => {
  it("marks a zero as a zero, because a zero is data", () => {
    render(<Value value={0}>0 ms</Value>);
    const el = screen.getByText("0 ms").closest(".value");
    expect(el?.getAttribute("data-zero")).toBe("true");
    // And not as an absent measurement, which is the confusion this exists to
    // prevent.
    expect(el?.getAttribute("data-empty")).toBeNull();
  });

  it("leaves a real measurement undimmed", () => {
    render(<Value value={87}>87 ms</Value>);
    const el = screen.getByText("87 ms").closest(".value");
    expect(el?.getAttribute("data-zero")).toBeNull();
    expect(el?.getAttribute("data-empty")).toBeNull();
  });

  it("tells a missing measurement apart from a zero", () => {
    const { container } = render(<Value value={null} />);
    const el = container.querySelector(".value");
    expect(el?.getAttribute("data-empty")).toBe("true");
    expect(el?.getAttribute("data-zero")).toBeNull();
  });

  it("does not dim a value that merely contains a zero digit", () => {
    // The reason the raw number is a separate prop: sniffing the text for "0"
    // would fade "10 ms" and "100%", which are the least ambiguous readings on
    // the screen.
    render(<Value value={10}>10 ms</Value>);
    expect(
      screen.getByText("10 ms").closest(".value")?.getAttribute("data-zero"),
    ).toBeNull();
  });

  it("carries a warning as a glyph as well as a line, never as colour alone", () => {
    // The whole point of §10's warning rule: the signal has to survive a
    // black-and-white screen. The dotted underline is CSS; the glyph is here,
    // and it is what a colour-blind reader and a screen reader both get.
    render(
      <Value value={4200} warning="Measured from one probe">
        4.2 s
      </Value>,
    );
    const el = screen.getByText("4.2 s").closest(".value");
    expect(el?.getAttribute("data-warn")).toBe("true");
    expect(
      screen.getByRole("img", { name: "Measured from one probe" }),
    ).toBeTruthy();
  });

  it("leaves an unwarned value with no glyph and no warning attribute", () => {
    render(<Value value={87}>87 ms</Value>);
    const el = screen.getByText("87 ms").closest(".value");
    expect(el?.getAttribute("data-warn")).toBeNull();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("warns on a zero without losing either statement", () => {
    // The two signals are independent: a measured zero that also carries a
    // caveat has to dim *and* mark itself.
    render(
      <Value value={0} warning="No successful check in 24h">
        0%
      </Value>,
    );
    const el = screen.getByText("0%").closest(".value");
    expect(el?.getAttribute("data-zero")).toBe("true");
    expect(el?.getAttribute("data-warn")).toBe("true");
  });
});
