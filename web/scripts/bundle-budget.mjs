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
 *
 * Fonts are measured separately and NOT gzipped: woff2 is already Brotli
 * inside, so gzipping it again measures a number no browser ever downloads.
 * They are also the one category where the budget is the point rather than a
 * tripwire — an unsubsetted face is 350 kB, and the whole reason the subset
 * step exists is that nothing in the build would otherwise say so.
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
  fonts: 80,
};

/*
 * A font only counts once the link that fetches it is complete. A `.woff2`
 * href alone measures a file the browser may never request in the first round
 * trip: without `rel="preload"` it is not preloaded at all, without
 * `as="font"` it is fetched at the wrong priority and again by the stylesheet,
 * and without `crossorigin` the preload is discarded and fetched twice. So the
 * budget is spent on links that are actually doing the job, and a link that
 * loses one of those attributes reads here as a font that stopped being
 * preloaded.
 */
function fontPreloads(html) {
  const preloaded = [];
  for (const tag of html.matchAll(/<link\b[^>]*>/g)) {
    const attrs = tag[0];
    const href = /\shref="\/?((?:assets|fonts)\/[^"]+\.woff2)"/.exec(attrs);
    if (!href) continue;
    if (!/\srel="preload"/.test(attrs)) continue;
    if (!/\sas="font"/.test(attrs)) continue;
    if (!/\scrossorigin(?:="(?:anonymous|use-credentials)?")?[\s/>]/.test(attrs)) continue;
    preloaded.push(href[1]);
  }
  return preloaded;
}

function entries(html) {
  const found = { js: [], css: [], fonts: fontPreloads(html) };
  for (const match of html.matchAll(/(?:src|href)="\/?((?:assets|fonts)\/[^"]+)"/g)) {
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

/*
 * The fonts reach index.html through preload links. If someone drops those the
 * faces still load, from the stylesheet — so an empty list here is a real
 * regression in loading behaviour, not an absence to skip over. The check sits
 * after the filtering above, so an incomplete link counts as no preload.
 */
if (found.fonts.length === 0) {
  console.error(
    "bundle-budget: index.html has no complete font preload " +
      '(rel="preload" as="font" crossorigin) — see src/styles/fonts.css',
  );
  process.exit(1);
}

let failed = false;
for (const [kind, budget] of Object.entries(budgets)) {
  let raw = 0;
  let gzipped = 0;
  for (const path of found[kind]) {
    const bytes = readFileSync(join(dist, path));
    raw += bytes.byteLength;
    gzipped += kind === "fonts" ? bytes.byteLength : gzipSync(bytes).byteLength;
  }
  if (found[kind].length === 0) continue;

  const kb = gzipped / 1024;
  const rawKb = raw / 1024;
  const line =
    kind === "fonts"
      ? `${kind}: ${kb.toFixed(1)} kB over the wire, budget ${budget} kB`
      : `${kind}: ${kb.toFixed(1)} kB gzip (${rawKb.toFixed(1)} kB raw), budget ${budget} kB`;
  if (kb > budget) {
    console.error(`::error::${line} — over budget`);
    failed = true;
  } else {
    console.log(`${line} — ok`);
  }
}

process.exit(failed ? 1 : 0);
