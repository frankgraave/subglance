import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { MIN_PASSWORD_LENGTH } from "./api";
import { describeStrength, passwordStrength } from "./password";

const repoRoot = join(fileURLToPath(new URL(".", import.meta.url)), "..", "..", "..");

describe("the password rule", () => {
  it("matches the minimum the Go server enforces", () => {
    /*
     * The one rule that exists in two languages, guarded rather than trusted.
     *
     * A drifting copy is the worst kind of validation bug: the form accepts a
     * password, the server rejects it, and the message blames a field the
     * user filled in exactly as instructed. Reading the constant out of the
     * Go source rather than restating it means raising the minimum on one
     * side fails here instead of in someone's browser.
     */
    const go = readFileSync(join(repoRoot, "internal", "auth", "auth.go"), "utf8");
    const declared = go.match(/MinPasswordLength\s*=\s*(\d+)/);
    expect(declared, "internal/auth/auth.go must declare MinPasswordLength").not.toBeNull();
    expect(MIN_PASSWORD_LENGTH).toBe(Number(declared![1]));
  });
});

describe("passwordStrength", () => {
  it("says nothing about an empty field", () => {
    expect(passwordStrength("")).toBe("empty");
  });

  it("calls anything under the server minimum short", () => {
    expect(passwordStrength("a".repeat(MIN_PASSWORD_LENGTH - 1))).toBe("short");
    expect(passwordStrength("a".repeat(MIN_PASSWORD_LENGTH))).toBe("ok");
  });

  it("counts code points, not UTF-16 units", () => {
    /*
     * The server counts runes. An emoji is two UTF-16 units and one rune, so
     * a naive `.length` here would call eleven emoji "long enough" and then
     * watch the server reject them as eleven characters.
     */
    const eleven = "\u{1F600}".repeat(11);
    expect(eleven.length).toBeGreaterThan(MIN_PASSWORD_LENGTH);
    expect(passwordStrength(eleven)).toBe("short");
  });

  it("praises a passphrase rather than a symbol soup", () => {
    // NIST SP 800-63B Rev 4 forbids composition rules: length is the thing
    // that matters, so a long ordinary phrase must outrank a short cryptic
    // one. This is the assertion that stops someone reintroducing a
    // "needs a digit" rule as an improvement.
    expect(passwordStrength("correct horse battery staple")).toBe("strong");
    expect(passwordStrength("P@ssw0rd!")).toBe("short");
  });
});

describe("describeStrength", () => {
  it("counts down the characters still needed, rather than repeating the rule", () => {
    expect(describeStrength("a".repeat(MIN_PASSWORD_LENGTH - 3))).toContain("3 more characters");
    expect(describeStrength("a".repeat(MIN_PASSWORD_LENGTH - 1))).toContain("1 more character");
    // Singular, because "1 more characters" is the kind of detail that makes
    // a careful product look careless on its first screen.
    expect(describeStrength("a".repeat(MIN_PASSWORD_LENGTH - 1))).not.toContain("characters");
  });

  it("names the minimum while the field is still empty", () => {
    expect(describeStrength("")).toContain(String(MIN_PASSWORD_LENGTH));
  });
});
