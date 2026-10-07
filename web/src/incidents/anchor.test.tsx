// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { IncidentsView } from "./IncidentsView";
import { incidentAnchor } from "./anchor";
import type { Incident } from "../monitors/detail";

/*
 * An alert's link names one incident: `/incidents#incident-312` (SUB-187).
 * The server writes that shape in internal/notifier/links.go; this is the
 * other end of it. Whoever follows the link came for that incident's
 * timeline and error, so its row arrives open and is scrolled to.
 */

const T0 = 1_700_000_000_000;
const NOW = T0 + 720_000;

const incident = (over: Partial<Incident>): Incident => ({
  id: "1", monitorId: "7", startedAt: T0, confirmedAt: T0 + 60_000, resolvedAt: T0 + 300_000,
  ackedAt: null, confirmed: true, resolved: true, acked: false, durationS: 300,
  cause: "timeout", lastError: "no response within 10s", ...over,
});

const names = { "7": "api", "8": "auth", "9": "cdn" };

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
  delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
});

describe("a link to one incident", () => {
  it("writes the anchor the server links to", () => {
    expect(incidentAnchor("312")).toBe("incident-312");
  });

  it("opens and scrolls to the named row, and only that one", () => {
    // jsdom lays nothing out and has no scrollIntoView; record the calls.
    const scrolled: Element[] = [];
    Object.defineProperty(Element.prototype, "scrollIntoView", {
      configurable: true,
      value(this: Element) { scrolled.push(this); },
    });
    window.history.replaceState(null, "", "/incidents#incident-312");
    render(<IncidentsView incidents={[]} now={NOW} names={names}
      resolved={[incident({ id: "311" }), incident({ id: "312", startedAt: T0 - 86_400_000 })]} />);

    const linked = document.getElementById("incident-312")!;
    expect(linked.querySelector(".inc-line")?.getAttribute("aria-expanded")).toBe("true");
    expect(linked.querySelector(".inc-detail")).not.toBeNull();
    expect(scrolled).toEqual([linked]);

    const other = document.getElementById("incident-311")!;
    expect(other.querySelector(".inc-line")?.getAttribute("aria-expanded")).toBe("false");
  });

  it("opens a recovered group that holds the named incident", () => {
    // A group whose members have all recovered starts folded; the linked row
    // must not arrive hidden inside it.
    const together = [
      incident({ id: "a", monitorId: "7", startedAt: T0 }),
      incident({ id: "b", monitorId: "8", startedAt: T0 - 20_000 }),
      incident({ id: "c", monitorId: "9", startedAt: T0 - 40_000 }),
    ];
    render(<IncidentsView incidents={together} now={NOW} names={names} />);
    expect(document.querySelector(".inc-cluster")).not.toBeNull();
    expect(document.getElementById("incident-b")).toBeNull();
    cleanup();

    window.history.replaceState(null, "", "/incidents#incident-b");
    render(<IncidentsView incidents={together} now={NOW} names={names} />);
    const linked = document.getElementById("incident-b")!;
    expect(linked.querySelector(".inc-line")?.getAttribute("aria-expanded")).toBe("true");
  });

  it("leaves every row closed without a fragment", () => {
    render(<IncidentsView incidents={[]} now={NOW} names={names} resolved={[incident({ id: "312" })]} />);
    expect(document.getElementById("incident-312")?.querySelector(".inc-line")?.getAttribute("aria-expanded")).toBe("false");
  });
});
