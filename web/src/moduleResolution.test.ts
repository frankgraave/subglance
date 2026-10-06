// @vitest-environment node
import { readdirSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * No two names under `src/` may differ only in case (SUB-220).
 *
 * The default filesystems on macOS (APFS) and Windows (NTFS) ignore case, and
 * CI runs on Linux, which does not. `monitors/ResponseHistory.tsx` (the
 * component) and `monitors/responseHistory.ts` (its types and grouping) lived
 * side by side for weeks: imports carry no extension, so on a Mac
 * `./responseHistory` and `./ResponseHistory` named the same file, TypeScript
 * read the wrong one, and `npm run build` failed with TS1149 and TS1261. CI
 * stayed green the whole time, because Linux told the two files apart.
 *
 * Two collisions are refused here, so that Linux fails them too:
 *
 * - two paths that fold to the same string. A case-insensitive checkout
 *   keeps only one of them, whatever they contain;
 * - two TypeScript modules whose path without the extension folds to the same
 *   string. `./x` resolves to `x.ts` or `x.tsx`, so `Foo.tsx` beside `foo.ts`
 *   is one import on a Mac even though the files themselves differ.
 *
 * A stylesheet is imported with its extension, so `responseHistory.css`
 * beside `ResponseHistory.tsx` is not a collision. Go needs no guard of its
 * own: `go build` refuses a case-insensitive file name collision in a package.
 */

const webSrc = fileURLToPath(new URL(".", import.meta.url));

/** Every file and directory under `dir`, relative to `webSrc`, with `/`. */
function entries(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    const local = relative(webSrc, path).split(sep).join("/");
    return entry.isDirectory() ? [local, ...entries(path)] : [local];
  });
}

const MODULE = /\.(?:ts|tsx)$/;

/** Groups of names that fold to the same key, each group and the list sorted. */
function collisions(names: string[], key: (name: string) => string | null): string[][] {
  const groups = new Map<string, string[]>();
  for (const name of names) {
    const folded = key(name);
    if (folded === null) continue;
    groups.set(folded, [...(groups.get(folded) ?? []), name]);
  }
  return [...groups.values()]
    .filter((group) => group.length > 1)
    .map((group) => group.sort())
    .sort(([a], [b]) => a.localeCompare(b));
}

/** Same path once case is ignored: a case-insensitive checkout keeps one. */
function pathCollisions(names: string[]): string[][] {
  return collisions(names, (name) => name.toLowerCase());
}

/** Same extensionless import once case is ignored. Declaration files are types only. */
function moduleCollisions(names: string[]): string[][] {
  return collisions(names, (name) =>
    MODULE.test(name) && !name.endsWith(".d.ts") ? name.replace(MODULE, "").toLowerCase() : null,
  );
}

describe("file names under src/", () => {
  const names = entries(webSrc);

  it("finds the source tree it guards", () => {
    // An empty walk would pass both checks below without reading anything.
    expect(names).toContain("App.tsx");
    expect(names).toContain("monitors/ResponseHistory.tsx");
  });

  it("has no two paths that differ only in case", () => {
    expect(pathCollisions(names)).toEqual([]);
  });

  it("has no two TypeScript modules one extensionless import cannot tell apart", () => {
    expect(moduleCollisions(names)).toEqual([]);
  });
});

describe("the collision rules", () => {
  it("refuses a component and a module whose names differ only in case", () => {
    expect(
      moduleCollisions(["monitors/ResponseHistory.tsx", "monitors/responseHistory.ts", "monitors/responseHistory.css"]),
    ).toEqual([["monitors/ResponseHistory.tsx", "monitors/responseHistory.ts"]]);
  });

  it("refuses a path that collides through a directory name", () => {
    expect(pathCollisions(["Wall/fit.ts", "wall/fit.ts"])).toEqual([["Wall/fit.ts", "wall/fit.ts"]]);
    expect(moduleCollisions(["Wall/fit.ts", "wall/fit.tsx"])).toEqual([["Wall/fit.ts", "wall/fit.tsx"]]);
  });

  it("lets a stylesheet, a declaration file or a different name stand beside a module", () => {
    const names = [
      "monitors/ResponseHistory.tsx",
      "monitors/responseHistory.css",
      "monitors/responseHistoryModel.ts",
      "env.d.ts",
      "Env.ts",
    ];
    expect(pathCollisions(names)).toEqual([]);
    expect(moduleCollisions(names)).toEqual([]);
  });
});
