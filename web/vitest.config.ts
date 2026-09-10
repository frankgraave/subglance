import { defineConfig } from "vitest/config";

/*
 * Kept separate from vite.config.ts: the suite is node-side (it reads
 * docs/DESIGN.md off disk) and has no business widening the browser build's
 * type surface.
 */
export default defineConfig({
  test: {
    environment: "node",
    // Component suites opt into jsdom with a `@vitest-environment` docblock;
    // node stays the default so the file-reading tests keep their real fs.
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
  },
});
