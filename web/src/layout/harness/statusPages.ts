/**
 * The public status page as the server renders it, served for the browser
 * suite.
 *
 * `go run ./internal/statuspage/preview` renders every preview scenario with
 * the stylesheet from this build. Each document is served at
 * /status/<scenario> with the Content-Security-Policy the renderer states, so
 * its relative font URLs resolve the way they do behind the product and a
 * script or style the policy refuses fails here as it would in production.
 *
 * Shared by statuspage.browser.test.ts, which measures the page's layout, and
 * accessibility.browser.test.ts, which holds it to the same axe gate as every
 * other screen. Both need the real page, not a copy of it.
 */
import { execFile } from "node:child_process";
import { createServer, type Server } from "node:http";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const ROOT = fileURLToPath(new URL("../../../../", import.meta.url));
const FONTS = fileURLToPath(new URL("../../../public/fonts/", import.meta.url));

export interface StatusPages {
  /** Origin of the server; a scenario is at `${url}/status/<name>`. */
  url: string;
  /** The policy every page is served with, as the renderer states it. */
  csp: string;
  /** The scenarios the preview command rendered, by name. */
  scenarios: string[];
  close(): Promise<void>;
}

/**
 * Renders the preview scenarios and serves them. Needs `npm run build` first.
 *
 * Until the handle is returned, a failed step — the render, reading what it
 * wrote, or binding the port — closes whatever is already listening and
 * removes the rendered directory before the error reaches the caller, which
 * has no handle to clean up with.
 */
export async function serveStatusPages(): Promise<StatusPages> {
  const dir = await mkdtemp(join(tmpdir(), "status-page-"));
  let server: Server | undefined;
  try {
    await promisify(execFile)("go", ["run", "./internal/statuspage/preview", "-out", dir], { cwd: ROOT });
    const csp = await readFile(join(dir, "csp.txt"), "utf8");
    const scenarios = (await readdir(dir))
      .filter((file) => file.endsWith(".html"))
      .map((file) => file.slice(0, -".html".length))
      .sort();

    const listening = createServer(async (req, res) => {
      const path = new URL(req.url ?? "/", "http://localhost").pathname;
      try {
        if (path.startsWith("/status/fonts/")) {
          const body = await readFile(join(FONTS, basename(path)));
          res.writeHead(200, { "content-type": "font/woff2" }).end(body);
          return;
        }
        const name = /^\/status\/([a-z]+)$/.exec(path)?.[1];
        if (name && scenarios.includes(name)) {
          const body = await readFile(join(dir, `${name}.html`));
          res.writeHead(200, { "content-type": "text/html; charset=utf-8", "content-security-policy": csp }).end(body);
          return;
        }
      } catch { /* fall through to 404 */ }
      res.writeHead(404).end();
    });
    server = listening;
    await new Promise<void>((resolve, reject) => {
      listening.once("error", reject);
      listening.listen(0, "127.0.0.1", () => {
        listening.off("error", reject);
        resolve();
      });
    });
    const addr = listening.address();
    if (!addr || typeof addr === "string") throw new Error("no port");

    return {
      url: `http://127.0.0.1:${addr.port}`,
      csp,
      scenarios,
      async close() {
        await stop(listening);
        await rm(dir, { recursive: true, force: true });
      },
    };
  } catch (error) {
    if (server?.listening) await stop(server);
    await rm(dir, { recursive: true, force: true });
    throw error;
  }
}

function stop(server: Server): Promise<void> {
  return new Promise<void>((resolve) => server.close(() => resolve()));
}
