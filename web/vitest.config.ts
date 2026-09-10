import { defineConfig } from "vitest/config";

/*
 * Kept separate from vite.config.ts: the suite is node-side (it reads
 * docs/DESIGN.md off disk) and has no business widening the browser build's
 * type surface.
 */
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
  },
});
