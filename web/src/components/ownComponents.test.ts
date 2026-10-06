import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * DESIGN.md §10 and ARCHITECTURE.md §1 both say the controls are SubGlance's
 * own, with no UI kit underneath. DESIGN.md once said a kit was "the starting
 * point" long after none was installed, and nothing noticed, because nothing
 * compared the sentence with the manifest. This does.
 *
 * The check reads files rather than rendering anything: the claim is about
 * what the build pulls in, and that is written in `web/package.json`.
 */

const here = fileURLToPath(new URL(".", import.meta.url));
const webRoot = join(here, "..", "..");
const repoRoot = join(webRoot, "..");

const manifest = JSON.parse(
  readFileSync(join(webRoot, "package.json"), "utf8"),
) as {
  dependencies?: Record<string, string>;
  devDependencies?: Record<string, string>;
};
const designMd = readFileSync(join(repoRoot, "docs", "DESIGN.md"), "utf8");
const architectureMd = readFileSync(
  join(repoRoot, "docs", "ARCHITECTURE.md"),
  "utf8",
);

/**
 * Packages that would make "no component library" untrue: a kit of drawn
 * controls, or the unstyled primitives a copied-in kit is built on. A scope
 * ends in a slash and matches every package under it.
 */
const KITS = [
  "@radix-ui/",
  "radix-ui",
  "shadcn",
  "@shadcn/",
  "@base-ui-components/",
  "@headlessui/",
  "@ark-ui/",
  "react-aria-components",
  "@mui/",
  "@chakra-ui/",
  "@mantine/",
  "antd",
  "react-bootstrap",
  "@nextui-org/",
  "@heroui/",
  "daisyui",
  "flowbite",
  "flowbite-react",
];

function isKit(name: string): boolean {
  return KITS.some((kit) =>
    kit.endsWith("/") ? name.startsWith(kit) : name === kit,
  );
}

/**
 * How a document would write a kit's name: every entry in KITS by its package
 * name (scope mark and slash dropped, a hyphen read as a hyphen, a space or
 * nothing), plus the names the projects go by that no package spells.
 */
const KIT_PROSE = [
  ...KITS.map((kit) =>
    kit.replace(/^@/, "").replace(/\/$/, "").split("-").join("[- ]?"),
  ),
  "radix",
  "headless ?ui",
  "material[- ]ui",
  "chakra",
  "mantine",
  "ant design",
  "nextui",
  "daisy ?ui",
  "react aria",
];
const kitNamed = new RegExp(`\\b(?:${KIT_PROSE.join("|")})\\b`, "i");

/** One section of a markdown document, from its heading to the next. */
function section(doc: string, heading: string): string {
  // A checkout with core.autocrlf writes CRLF; the delimiters below are LF.
  const text = doc.replace(/\r\n/g, "\n");
  const start = text.indexOf(`\n${heading}\n`);
  if (start === -1) throw new Error(`no section ${heading}; the check is broken`);
  const body = text.slice(start + heading.length + 2);
  const next = body.indexOf("\n## ");
  return next === -1 ? body : body.slice(0, next);
}

describe("the controls are SubGlance's own", () => {
  it("installs no UI kit", () => {
    const installed = [
      ...Object.keys(manifest.dependencies ?? {}),
      ...Object.keys(manifest.devDependencies ?? {}),
    ];
    expect(installed.filter(isKit)).toEqual([]);
  });

  it("has no copied-in kit's configuration file", () => {
    // `components.json` is what a copy-in kit's CLI writes beside the
    // manifest; its presence means components are being generated into the
    // tree from someone else's defaults.
    expect(existsSync(join(webRoot, "components.json"))).toBe(false);
  });

  it("says so in DESIGN.md §10, pointing at where the controls live", () => {
    const rules = section(designMd, "## 10. What this design does not do");
    expect(rules).toContain("No component-kit look.");
    expect(rules).toContain("`web/src/components`");
    expect(rules).not.toMatch(/starting point/i);
  });

  it("names no UI kit anywhere in DESIGN.md", () => {
    // The design document describes what is built. A kit's name in it is how
    // the stale claim got there, so none is named at all: telling a claim
    // that a kit is the base from a passing mention is not something a
    // pattern does reliably, and a missed claim is the failure this exists for.
    const named = designMd
      .split(/\r?\n/)
      .map((line, i) => ({ line: i + 1, text: line }))
      .filter(({ text }) => kitNamed.test(text));
    expect(named).toEqual([]);
  });

  it("recognises every listed kit by name", () => {
    for (const kit of KITS) {
      const name = kit.replace(/^@/, "").replace(/\/$/, "");
      expect(kitNamed.test(`Built on ${name}.`), kit).toBe(true);
    }
    const prose = [
      "shadcn/ui",
      "Radix UI",
      "Headless UI",
      "Material UI",
      "Ant Design",
      "NextUI",
      "daisyUI",
    ];
    for (const name of prose) {
      expect(kitNamed.test(`Starts from ${name}.`), name).toBe(true);
    }
    expect(kitNamed.test("A status badge drawn from the tokens.")).toBe(false);
  });

  it("finds a section in a CRLF checkout", () => {
    const doc = "intro\r\n## A\r\nbody\r\n## B\r\nrest";
    expect(section(doc, "## A")).toBe("body");
  });

  it("says so in ARCHITECTURE.md's stack table", () => {
    expect(architectureMd).toMatch(
      /\| Components \| Own components in `web\/src\/components`, no component library \|/,
    );
  });
});
