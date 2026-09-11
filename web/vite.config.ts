import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // The bundle is embedded into the Go binary by internal/webui, so it is
    // built straight into that package: one artefact in one place, with no
    // copy step that someone can forget to run.
    outDir: "../internal/webui/dist",

    // Deliberately false. That directory holds a committed .gitkeep, which is
    // what keeps `//go:embed all:dist` compiling on a checkout where Node
    // never ran; emptying the directory would delete it and break `go build`
    // for anyone who is not working on the frontend. `make web-build` removes
    // the stale asset directory itself.
    emptyOutDir: false,
  },
});
