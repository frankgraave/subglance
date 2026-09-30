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

    // Two entrypoints. The dashboard is index.html. The public status page
    // is a stylesheet only: internal/statuspage renders the HTML on the
    // server and inlines this file into it, so it needs a fixed name the Go
    // side can find without reading a manifest, and it must stay out of
    // assets/, which webui serves as immutable because those names carry a
    // content hash.
    rolldownOptions: {
      input: {
        index: "index.html",
        "status-page": "src/statuspage/page.css",
      },
      output: {
        assetFileNames: (asset) =>
          asset.names.includes("status-page.css")
            ? "status-page.css"
            : "assets/[name]-[hash][extname]",
      },
    },
  },
});
