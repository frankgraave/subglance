import { defineConfig } from "vitest/config";

/*
 * Kept separate from vite.config.ts: the suite is node-side (it reads
 * docs/DESIGN.md off disk) and has no business widening the browser build's
 * type surface.
 */

/*
 * Force the test build of React, whatever the ambient NODE_ENV says.
 *
 * Vitest normally sets NODE_ENV=test, but it leaves an already-exported value
 * alone — and `NODE_ENV=production` is exported by more environments than one
 * expects: CI images, container bases, and the agent runner that drives this
 * repository. React then resolves to its production build, which omits
 * `React.act`, and every component test dies with
 * "TypeError: React.act is not a function".
 *
 * The failure is worth spelling out because it lies about its cause: the suite
 * fails identically on a clean checkout of a green branch, so it reads as a
 * broken repository rather than a broken environment. Pinning the value here
 * means the suite's result depends on the code under test and nothing else.
 */
process.env.NODE_ENV = "test";

export default defineConfig({
  test: {
    environment: "node",
    // Component suites opt into jsdom with a `@vitest-environment` docblock;
    // node stays the default so the file-reading tests keep their real fs.
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
  },
});
