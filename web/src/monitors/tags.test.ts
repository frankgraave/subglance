import { describe, expect, it } from "vitest";
import { tagsToText, textToTags } from "./tags";

describe("textToTags", () => {
  it("splits on the first colon only, so a value may contain one", () => {
    // The API carries tags as an object precisely so a value can be a URL. A
    // naive split would store `url` -> `https` and throw the rest away.
    expect(textToTags("url:https://example.com")).toEqual({
      url: "https://example.com",
    });
  });

  it("reads a comma-separated list", () => {
    expect(textToTags("env:prod, team:payments")).toEqual({
      env: "prod",
      team: "payments",
    });
  });

  it("treats an empty string as no tags rather than as a parse failure", () => {
    expect(textToTags("")).toEqual({});
    expect(textToTags("  ,  ")).toEqual({});
  });

  it("refuses text with no colon, rather than inventing a key or a value", () => {
    // Guessing here would send the server something it rejects with a message
    // about a field the user cannot see.
    expect(textToTags("prod")).toBeNull();
    expect(textToTags(":prod")).toBeNull();
    expect(textToTags("env:")).toBeNull();
  });
});

describe("tagsToText", () => {
  it("round-trips", () => {
    const tags = { env: "prod", url: "https://example.com" };
    expect(textToTags(tagsToText(tags))).toEqual(tags);
  });
});
