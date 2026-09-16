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

  it("reads one entry per line, which is what tagsToText writes", () => {
    expect(textToTags("env:prod\nteam:payments")).toEqual({
      env: "prod",
      team: "payments",
    });
  });

  it("does not treat a comma as a separator, so a value may contain one", () => {
    /*
     * Accepting both separators looks generous and quietly destroys the whole
     * guarantee: the value a user can see in the box would stop being the
     * value that gets saved. A format that round-trips only sometimes is worse
     * than one that asks for a line break, because the failure is invisible
     * until a tag has been cut in half.
     */
    expect(textToTags("note:a,b")).toEqual({ note: "a,b" });
  });

  it("treats an empty string as no tags rather than as a parse failure", () => {
    expect(textToTags("")).toEqual({});
    expect(textToTags("  \n  ")).toEqual({});
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
  it("round-trips a value containing a comma", () => {
    /*
     * The bug this pair was redesigned for. A comma separator renders
     * `{note: "a,b", env: "prod"}` as `note:a,b, env:prod`, which reads back
     * as three entries — one of them the bare word `b` — so editing any OTHER
     * tag on that monitor became impossible. A newline can be ruled out of a
     * value in a way a comma cannot: the store rejects control characters in
     * tag keys and values, so splitting on it is lossless.
     */
    const tags = { note: "a,b", env: "prod" };
    expect(textToTags(tagsToText(tags))).toEqual(tags);
  });

  it("round-trips a value containing a colon", () => {
    const tags = { env: "prod", url: "https://example.com" };
    expect(textToTags(tagsToText(tags))).toEqual(tags);
  });
});
