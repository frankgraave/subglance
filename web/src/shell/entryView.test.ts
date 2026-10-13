// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { isTagSelection, oneOf, readEntryView, writeEntryView } from "./entryView";
import { ROUTE_INDEX } from "./useRoute";

afterEach(() => window.history.replaceState(null, "", "/"));

describe("a list's view on the history entry", () => {
  it("merges into the entry without touching the router's keys or the URL", () => {
    window.history.replaceState({ [ROUTE_INDEX]: 4 }, "", "/monitors");
    writeEntryView("monitors", { query: "db" });
    writeEntryView("monitors", { sort: "status" });
    expect(window.history.state[ROUTE_INDEX]).toBe(4);
    expect(window.location.pathname).toBe("/monitors");
    expect(readEntryView("monitors")).toEqual({ query: "db", sort: "status" });
  });

  it("is never read by the other list", () => {
    window.history.replaceState(null, "", "/");
    writeEntryView("dashboard", { query: "db" });
    expect(readEntryView("monitors")).toEqual({});
    writeEntryView("monitors", { query: "api" });
    expect(readEntryView("dashboard")).toEqual({});
    expect(readEntryView("monitors")).toEqual({ query: "api" });
  });

  it("reads malformed state as no view", () => {
    for (const state of [null, "x", { subglanceListView: "x" }, { subglanceListView: null }]) {
      window.history.replaceState(state, "", "/");
      expect(readEntryView("dashboard")).toEqual({});
    }
  });
});

describe("the validators a saved value has to pass", () => {
  it("accept only what the controls could have set", () => {
    const sort = oneOf(["name", "status"]);
    expect(sort("status")).toBe(true);
    expect(sort("colour")).toBe(false);
    expect(isTagSelection({ env: "prod" })).toBe(true);
    expect(isTagSelection({ env: 1 })).toBe(false);
    expect(isTagSelection(["prod"])).toBe(false);
    expect(isTagSelection(null)).toBe(false);
  });
});
