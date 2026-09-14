/*
 * Bundle budget.
 *
 * The container image has a 30 MB gate in docker.yml; the JavaScript that ships
 * inside the binary had none, so a dependency that doubled it would land
 * silently. This is the equivalent gate for the frontend: it fails the build
 * when a bundle crosses its budget.
 *
 * Sizes are gzipped, because that is what a browser downloads and therefore the
 * only number a user experiences. Raw bytes are printed too, for context.
 *
 * The entry files are read out of index.html rather than globbed off disk.
 * `emptyOutDir` is deliberately false in vite.config.ts, so the assets
 * directory accumulates every build ever made on a working copy; a glob would
 * measure whichever stale chunk sorted first.
 */

import { gzipSync } from "node:zlib";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const dist = resolve(dirname(fileURLToPath(import.meta.url)), "../../internal/webui/dist");

/*
 * Budgets in gzipped kilobytes, seeded a little above what the bundle measures
 * today so ordinary feature work does not trip them. Raising one is allowed —
 * it is meant to be a deliberate, reviewed act rather than an accident.
 */
const budgets = {
  js: 112,
  css: 12,
};

function entries(html) {
  const found = { js: [], css: [] };
  for (const match of html.matchAll(/(?:src|href)="\/?(assets\/[^"]+)"/g)) {
    const path = match[1];
    if (path.endsWith(".js")) found.js.push(path);
    else if (path.endsWith(".css")) found.css.push(path);
  }
  return found;
}

const html = readFileSync(join(dist, "index.html"), "utf8");
const found = entries(html);

if (found.js.length === 0) {
  console.error("bundle-budget: index.html references no JavaScript — did the build run?");
  process.exit(1);
}

let failed = false;
for (const [kind, budget] of Object.entries(budgets)) {
  let raw = 0;
  let gzipped = 0;
  for (const path of found[kind]) {
    const bytes = readFileSync(join(dist, path));
    raw += bytes.byteLength;
    gzipped += gzipSync(bytes).byteLength;
  }
  if (found[kind].length === 0) continue;

  const kb = gzipped / 1024;
  const rawKb = raw / 1024;
  const line = `${kind}: ${kb.toFixed(1)} kB gzip (${rawKb.toFixed(1)} kB raw), budget ${budget} kB`;
  if (kb > budget) {
    console.error(`::error::${line} — over budget`);
    failed = true;
  } else {
    console.log(`${line} — ok`);
  }
}

process.exit(failed ? 1 : 0);
