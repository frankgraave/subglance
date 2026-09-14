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
    //
    // `*.browser.test.ts` is excluded: those need a built bundle and a real
    // Chromium, so they run separately via `npm run test:browser`. Leaving
    // them in here would make a unit-test run depend on a browser download.
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
    exclude: ["src/**/*.browser.test.ts", "node_modules/**"],

    /*
     * Coverage exists for the same reason the Go side has a floor: to stop the
     * suite thinning out unnoticed. The numbers below are set a little under
     * what the suite measures today, so ordinary work does not trip them but
     * deleting a test file does.
     *
     * Thresholds are only enforced when a run asks for coverage, so a plain
     * `npm test` stays fast and CI runs `npm run test:coverage`.
     */
    coverage: {
      provider: "v8",
      include: ["src/**"],
      exclude: [
        // Bootstrap: three lines that mount the app into the real DOM. A test
        // for it would assert that react-dom works.
        "src/main.tsx",
        // Fixtures for the browser layout checks, which run under
        // vitest.browser.config.ts. This run never imports them, so counting
        // them here would report 0% for code that is exercised elsewhere.
        "src/layout/harness/**",
      ],
      reporter: ["text-summary", "lcov"],
      thresholds: {
        statements: 90,
        branches: 85,
        functions: 90,
        lines: 92,
      },
    },
  },
});
