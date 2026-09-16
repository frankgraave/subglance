import { describe, expect, it } from "vitest";
import {
  INCIDENTS_PATH,
  MONITORS_PATH,
  MONITOR_CREATE_PATH,
  NOTIFICATIONS_PATH,
  NOTIFICATION_CREATE_PATH,
  monitorPath,
  parseRoute,
  routePath,
} from "./route";

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
    expect(parseRoute("/monitors/1/extra")).toEqual({ name: "dashboard" });
  });

  it("refuses an empty id, which would otherwise select monitor ''", () => {
    // `/monitors//` has no id segment at all once empties are dropped, so it
    // is the inventory — not monitor "". The distinction matters: selecting a
    // monitor with an empty id would fetch /api/v1/monitors/ and render a
    // detail page about nothing.
    expect(parseRoute("/monitors//")).toEqual({
      name: "monitors",
      create: false,
    });
    expect(parseRoute("/monitors/%20")).toEqual({ name: "monitor", id: " " });
  });

  it("reads the monitors inventory (SUB-122)", () => {
    // The last "Soon" in the rail. It is a place because it is where the
    // configuration lives, and \"open the monitor settings\" is a link people
    // send each other.
    expect(parseRoute(MONITORS_PATH)).toEqual({
      name: "monitors",
      create: false,
    });
    expect(parseRoute("/monitors/")).toEqual({ name: "monitors", create: false });
  });

  it("reads /monitors/new as the inventory with the create form open", () => {
    // The drawer is a real address, so the empty state's call to action can be
    // linked to and a reload does not lose the form.
    expect(parseRoute(MONITOR_CREATE_PATH)).toEqual({
      name: "monitors",
      create: true,
    });
  });

  it("reads the notifications screen (SUB-123)", () => {
    // A place, not a mode: "your alerts go nowhere, here is where you fix it"
    // is a link somebody sends to the person running the instance.
    expect(parseRoute(NOTIFICATIONS_PATH)).toEqual({
      name: "notifications",
      create: false,
    });
    expect(parseRoute("/notifications/")).toEqual({
      name: "notifications",
      create: false,
    });
  });

  it("reads /notifications/new as the list with the add form open", () => {
    expect(parseRoute(NOTIFICATION_CREATE_PATH)).toEqual({
      name: "notifications",
      create: true,
    });
  });

  it("does not mistake a deeper notifications path for the screen", () => {
    // There is no per-channel route, and rendering the list for
    // /notifications/42 would claim an address that promises one channel.
    expect(parseRoute("/notifications/42")).toEqual({ name: "dashboard" });
    expect(parseRoute("/notifications/new/extra")).toEqual({
      name: "dashboard",
    });
  });

  it("does not let an encoded spelling reach the create form", () => {
    // `%6eew` decodes to `new`. Accepting it would open the form at a URL
    // routePath can never produce, leaving the address bar and the screen
    // disagreeing about where the user is.
    expect(parseRoute("/monitors/%6eew")).toEqual({
      name: "monitor",
      id: "new",
    });
  });
});

describe("routePath", () => {
  it("inverts parseRoute for both routes", () => {
    for (const route of [
      { name: "dashboard" },
      { name: "incidents" },
      { name: "monitor", id: "7" },
      { name: "monitors", create: false },
      { name: "monitors", create: true },
      { name: "notifications", create: false },
      { name: "notifications", create: true },
    ] as const) {
      expect(parseRoute(routePath(route))).toEqual(route);
    }
  });
});
