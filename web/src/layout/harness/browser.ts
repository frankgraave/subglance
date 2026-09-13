/**
 * Chromium for the layout checks.
 *
 * `puppeteer-core` rather than `puppeteer`: the full package downloads a
 * browser on every `npm install`, including for everyone who only touches Go
 * or only runs the unit suite. The browser is fetched explicitly instead, by
 * the one CI job that needs it and by `npm run test:browser:setup` locally.
 *
 * The executable is located in this order:
 *   1. PUPPETEER_EXECUTABLE_PATH — what CI sets after installing the shell.
 *   2. A Playwright cache, which several dev machines already have.
 *
 * When neither resolves, the error names the command that fixes it. A missing
 * browser is an environment problem and must not read as a layout failure.
 */
import { existsSync, readdirSync } from "node:fs";
import { join } from "node:path";
import puppeteer, { type Browser, type Page } from "puppeteer-core";

export type { Browser, Page };

function fromPlaywrightCache(): string | undefined {
  const root = join(process.env.HOME ?? "", ".cache/ms-playwright");
  if (!existsSync(root)) return undefined;
  for (const dir of readdirSync(root)) {
    if (!dir.startsWith("chromium")) continue;
    for (const candidate of [
      join(root, dir, "chrome-linux64/chrome"),
      join(root, dir, "chrome-headless-shell-linux64/chrome-headless-shell"),
      join(root, dir, "chrome-linux/chrome"),
    ]) {
      if (existsSync(candidate)) return candidate;
    }
  }
  return undefined;
}

function executablePath(): string {
  const explicit = process.env.PUPPETEER_EXECUTABLE_PATH;
  if (explicit && existsSync(explicit)) return explicit;

  const playwright = fromPlaywrightCache();
  if (playwright) return playwright;

  throw new Error(
    "No Chromium found for the browser layout checks.\n" +
      "Install one with:  npx @puppeteer/browsers install chrome-headless-shell@stable\n" +
      "then point PUPPETEER_EXECUTABLE_PATH at it, or set it to an existing browser.",
  );
}

export async function chromium(): Promise<Browser> {
  return puppeteer.launch({
    executablePath: executablePath(),
    // --no-sandbox is required inside the CI container. This browser only ever
    // loads a bundle this repository just built, from a loopback server.
    args: ["--no-sandbox", "--disable-dev-shm-usage", "--force-device-scale-factor=1"],
  });
}
