import { defineConfig } from "vitest/config";

/*
 * The browser layout checks, kept out of the default suite on purpose.
 *
 * They need two things `npm test` deliberately does not: a production bundle
 * on disk and a real Chromium. Putting them in the main suite would make a
 * unit-test run depend on a 100MB download and a build step, and would turn a
 * missing browser into a red test for someone editing a Go file.
 *
 * Run with `npm run test:browser`, which builds first.
 */
process.env.NODE_ENV = "test";

export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.browser.test.ts"],
    /*
     * One browser, one server, shared across the file. Parallel workers would
     * each launch their own Chromium, which is slower than the tests are.
     */
    fileParallelism: false,
    testTimeout: 30_000,
    hookTimeout: 120_000,
  },
});
