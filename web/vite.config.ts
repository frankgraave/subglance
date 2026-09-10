import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // The bundle is embedded into the Go binary, so keep the output flat and
    // predictable rather than content-hashed into nested directories.
    outDir: "dist",
    emptyOutDir: true,
  },
});
