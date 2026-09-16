import { describe, expect, it } from "vitest";
import { INCIDENTS_PATH, monitorPath, parseRoute, routePath } from "./route";

describe("parseRoute", () => {
  it("reads a monitor id out of the path", () => {
    expect(parseRoute("/monitors/42")).toEqual({ name: "monitor", id: "42" });
  });

  it("ignores a trailing slash, which a pasted URL often carries", () => {
    expect(parseRoute("/monitors/42/")).toEqual({ name: "monitor", id: "42" });
  });

  it("decodes the id, so it survives a round trip through monitorPath", () => {
    const id = "a b/c";
    expect(parseRoute(monitorPath(id))).toEqual({ name: "monitor", id });
  });

  it("falls back to the dashboard on a malformed escape rather than throwing", () => {
    // decodeURIComponent throws on this, and a hand-typed URL can contain it.
    // An exception here would take the whole app down on a typo.
    expect(parseRoute("/monitors/%zz")).toEqual({ name: "dashboard" });
  });

  it("reads the incidents screen, which an alert links people to", () => {
    // SUB-34. It is a place rather than a mode: somebody pastes this URL into
    // a chat window at 03:00, and a boolean cannot be pasted.
    expect(parseRoute(INCIDENTS_PATH)).toEqual({ name: "incidents" });
    expect(parseRoute("/incidents/")).toEqual({ name: "incidents" });
  });

  it("does not mistake a deeper incidents path for the screen", () => {
    // No per-incident route exists, and silently showing the list for
    // /incidents/42 would claim a screen that is not there.
    expect(parseRoute("/incidents/42")).toEqual({ name: "dashboard" });
  });

  it("treats an unknown path as the dashboard, not as a crash", () => {
    expect(parseRoute("/nope")).toEqual({ name: "dashboard" });
    expect(parseRoute("/")).toEqual({ name: "dashboard" });
    expect(parseRoute("/monitors")).toEqual({ name: "dashboard" });
    expect(parseRoute("/monitors/1/extra")).toEqual({ name: "dashboard" });
  });

  it("refuses an empty id, which would otherwise select monitor ''", () => {
    expect(parseRoute("/monitors//")).toEqual({ name: "dashboard" });
  });
});

describe("routePath", () => {
  it("inverts parseRoute for both routes", () => {
    for (const route of [
      { name: "dashboard" },
      { name: "incidents" },
      { name: "monitor", id: "7" },
    ] as const) {
      expect(parseRoute(routePath(route))).toEqual(route);
    }
  });
});
