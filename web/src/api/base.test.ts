import { describe, expect, it } from "vitest";
import { baseFrom, withBase, withoutBase } from "./base";

describe("baseFrom", () => {
  it("is the directory above the bundle's assets/", () => {
    expect(baseFrom("https://example.com/assets/index-abc.js")).toBe("");
    expect(baseFrom("https://example.com/subglance/assets/index-abc.js")).toBe("/subglance");
    expect(baseFrom("http://10.0.0.5:8080/a/b/assets/index-abc.js")).toBe("/a/b");
  });

  it("keeps an encoded prefix encoded, as the browser shows it", () => {
    expect(baseFrom("https://example.com/up%20time/assets/x.js")).toBe("/up%20time");
  });

  it("is the root under the dev server and for a module that is not served over http", () => {
    expect(baseFrom("http://localhost:5173/src/api/base.ts")).toBe("");
    expect(baseFrom("file:///repo/web/src/api/base.ts")).toBe("");
  });
});

describe("withBase and withoutBase", () => {
  it("are each other's inverse for every app path", () => {
    for (const base of ["", "/subglance", "/a/b"]) {
      for (const path of ["/", "/monitors", "/monitors/12", "/notifications/new", "/api/v1/stream"]) {
        expect(withoutBase(base, withBase(base, path))).toBe(path);
      }
    }
  });

  it("puts the prefix in front of requests and links", () => {
    expect(withBase("/subglance", "/api/v1/monitors")).toBe("/subglance/api/v1/monitors");
    expect(withBase("", "/api/v1/monitors")).toBe("/api/v1/monitors");
  });

  it("reads the prefix itself, with or without its slash, as the dashboard", () => {
    expect(withoutBase("/subglance", "/subglance")).toBe("/");
    expect(withoutBase("/subglance", "/subglance/")).toBe("/");
  });

  it("leaves a path outside the prefix alone, so the router calls it not found", () => {
    expect(withoutBase("/subglance", "/monitors")).toBe("/monitors");
    // A longer name that only starts with the prefix is not under it.
    expect(withoutBase("/subglance", "/subglancer/monitors")).toBe("/subglancer/monitors");
  });
});
