import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * Guards that the product actually ships the faces it is designed around.
 *
 * This regressed once already, silently and for months: tokens.css named two
 * families, nothing ever loaded them, and the product resolved a different
 * system font on every operating system. `document.fonts.size` was 0 while the
 * stylesheet and the design document both read as if the faces were there.
 * Nothing failed, because nothing was checking.
 *
 * So the checks below are deliberately about existence and wiring rather than
 * rendering: a unit test cannot see a glyph, but it can see that every link in
 * the chain — @font-face, file on disk, preload, feature settings — is present
 * and points at the same filename. The one thing that could still rot is the
 * face's visual quality, and no test was ever going to catch that.
 */

const here = fileURLToPath(new URL(".", import.meta.url));
const webRoot = join(here, "..", "..");

const fontsCss = readFileSync(join(here, "fonts.css"), "utf8");
const tokensCss = readFileSync(join(here, "tokens.css"), "utf8");
const indexCss = readFileSync(join(here, "..", "index.css"), "utf8");
const indexHtml = readFileSync(join(webRoot, "index.html"), "utf8");
const manifest = JSON.parse(
  readFileSync(join(webRoot, "public", "fonts", "MANIFEST.json"), "utf8"),
) as Array<{ face: string; version: string; source: string; sha256: string; file: string }>;

/** Every `src: url(...)` in a @font-face rule, in declaration order. */
function declaredFiles(): string[] {
  return [...fontsCss.matchAll(/src:\s*url\("([^"]+)"\)/g)].map((m) => m[1]);
}

describe("the faces are self-hosted", () => {
  it("declares a @font-face for the sans and the mono roles", () => {
    const families = [...fontsCss.matchAll(/font-family:\s*"([^"]+)"/g)].map((m) => m[1]);
    expect(families).toContain("InterVariable");
    expect(families).toContain("CommitMono");
  });

  it("names those same families first in the role tokens", () => {
    expect(tokensCss).toMatch(/--font-sans:\s*"InterVariable"/);
    expect(tokensCss).toMatch(/--font-mono:\s*"CommitMono"/);
  });

  it("keeps a fallback stack behind each, for the load and for failure", () => {
    // A bare family name is the trap: it renders as the browser default,
    // which on Linux is a serif face, if the file ever fails to arrive.
    expect(tokensCss).toMatch(/--font-sans:\s*"InterVariable",[^;]*sans-serif/);
    expect(tokensCss).toMatch(/--font-mono:\s*"CommitMono",[^;]*monospace/);
  });

  it("serves every declared file from the application itself", () => {
    const files = declaredFiles();
    expect(files.length).toBeGreaterThan(0);
    for (const url of files) {
      // An absolute, same-origin path. A scheme here means a CDN, and this
      // product is installed on networks with no outbound access at all.
      expect(url, `${url} is not an application-relative path`).toMatch(/^\/fonts\//);
      const onDisk = join(webRoot, "public", url);
      expect(existsSync(onDisk), `${url} is declared but not committed`).toBe(true);
    }
  });

  it("preloads every declared file, under the name it is declared with", () => {
    for (const url of declaredFiles()) {
      expect(indexHtml, `${url} is not preloaded`).toContain(`href="${url}"`);
    }
  });

  it("records the upstream release each file was built from", () => {
    const declared = declaredFiles().map((u) => u.replace("/fonts/", ""));
    expect(manifest.map((entry) => entry.file).sort()).toEqual([...declared].sort());
    for (const entry of manifest) {
      expect(entry.version, `${entry.face} has no version`).toMatch(/\d/);
      expect(entry.source).toMatch(/^https:\/\//);
      expect(entry.sha256, `${entry.face} has no checksum`).toMatch(/^[0-9a-f]{64}$/);
    }
  });

  it("keeps the shipped files small enough to preload", () => {
    // The unsubsetted sans alone is 352 kB. Anything near that means
    // scripts/subset-fonts.py was bypassed and the raw release was copied in.
    let total = 0;
    for (const url of declaredFiles()) {
      total += statSync(join(webRoot, "public", url)).size;
    }
    expect(total).toBeLessThan(80 * 1024);
  });
});

describe("the layout features are set once, on the role", () => {
  it("pins both roles in tokens.css", () => {
    expect(tokensCss).toMatch(/--feat-sans:/);
    expect(tokensCss).toMatch(/--feat-mono:/);
  });

  it("enables the mono's nine character variants", () => {
    const mono = /--feat-mono:([^;]+);/.exec(tokensCss)?.[1] ?? "";
    for (const tag of ["ss01", "ss02", "ss03", "ss04", "ss05", "cv01", "cv05", "cv06", "cv11"]) {
      expect(mono, `--feat-mono is missing ${tag}`).toContain(`"${tag}" 1`);
    }
  });

  it("keeps the sans to its structural features and proportional figures", () => {
    const sans = /--feat-sans:([^;]+);/.exec(tokensCss)?.[1] ?? "";
    for (const tag of ["calt", "ccmp", "locl", "kern"]) {
      expect(sans, `--feat-sans is missing ${tag}`).toContain(`"${tag}"`);
    }
    // Figures in the sans stay proportional on purpose: anything measurable is
    // set in the mono instead. Turning these on here would quietly widen every
    // digit in a heading.
    expect(sans).not.toContain("tnum");
    expect(sans).not.toContain("zero");
  });

  it("applies both roles from the base layer", () => {
    expect(indexCss).toContain("font-feature-settings: var(--feat-sans)");
    expect(indexCss).toContain("font-feature-settings: var(--feat-mono)");
  });

  it("lets no component spell out a feature setting of its own", () => {
    // Same rule as colours and sizes: a call site that writes its own
    // features is invisible drift. tokens.css and fonts.css hold them.
    const offenders: string[] = [];
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) walk(path);
        else if (entry.name.endsWith(".css") && !allowed.has(entry.name)) {
          // Read the value out and judge it, rather than a lookahead after
          // `\s*`: the engine backtracks the whitespace to zero and the
          // lookahead then passes on the space, so every file matches.
          for (const found of readFileSync(path, "utf8").matchAll(
            /font-feature-settings\s*:\s*([^;]+);/g,
          )) {
            if (!found[1].trim().startsWith("var(")) offenders.push(path);
          }
        }
      }
    };
    const allowed = new Set(["fonts.css", "tokens.css"]);
    walk(join(here, ".."));
    expect(offenders).toEqual([]);
  });
});
