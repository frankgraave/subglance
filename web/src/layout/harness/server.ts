/**
 * A real HTTP server for the browser layout checks.
 *
 * Serves the production bundle from `internal/webui/dist` and stubs the two
 * API calls the dashboard makes on load. The stub is deliberate: these tests
 * are about layout, so binding a database and a session cookie would add two
 * ways for them to fail that have nothing to do with what they measure. The
 * fixture instead pins the *content* — the longest name, the longest URL, the
 * most tags — because layout bugs are found by the widest realistic row, not
 * by an average one.
 */
import { createServer, type Server as HttpServer } from "node:http";
import { createReadStream } from "node:fs";
import { stat } from "node:fs/promises";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";
import type { ApiHeartbeat, ApiMonitor } from "../../monitors/types";

/*
 * `import.meta.url` is .../web/src/layout/harness/server.ts, so the repository
 * root is four levels up. Resolved from the module rather than from
 * process.cwd() so the tests work regardless of where they are invoked from.
 */
const DIST = fileURLToPath(new URL("../../../../internal/webui/dist/", import.meta.url));

const TYPES: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".json": "application/json; charset=utf-8",
};

/*
 * Heartbeats in the wire shape `fromApi` expects: `ts`/`ok`, not a status
 * string. Getting this wrong does not fail loudly — the dashboard renders an
 * empty or misleading row — so the field names are taken from
 * `ApiHeartbeat` in src/monitors/types.ts rather than invented here.
 */
function beats(count: number, downFrom = -1): ApiHeartbeat[] {
  const now = Date.now();
  return Array.from({ length: count }, (_, i) => {
    const down = downFrom >= 0 && i >= downFrom;
    return {
      ts: new Date(now - (count - i) * 60_000).toISOString(),
      ok: !down,
      latency_ms: down ? null : 40 + ((i * 37) % 900),
      ...(down ? { error: "dial tcp: connect: connection refused" } : {}),
    };
  });
}

/*
 * Names and targets sit at the long end deliberately. A phone layout that
 * holds for "api" and breaks on a real customer's hostname is not tested by
 * "api". One monitor is down with an open incident, one is paused, one is
 * warning on latency: between them they exercise the widest form of a row.
 */
const MONITORS: ApiMonitor[] = [
  {
    id: 1,
    name: "checkout-api-eu-west-1.internal.acme-corporation.example",
    type: "http",
    target: "https://checkout-api-eu-west-1.internal.acme-corporation.example/healthz?verbose=true",
    interval_s: 60,
    timeout_s: 10,
    enabled: true,
    status: "down",
    last_check: new Date().toISOString(),
    latency_ms: null,
    error: "dial tcp 10.0.4.12:443: connect: connection refused",
    incident_id: "42",
    incident_since: new Date(Date.now() - 8 * 60_000).toISOString(),
    uptime_24h: 97.41,
    created_at: new Date(Date.now() - 86_400_000).toISOString(),
    heartbeats: beats(60, 52),
    tags: { env: "production", customer: "acme-corporation", team: "payments-platform" },
  },
  {
    id: 2,
    name: "auth",
    type: "http",
    target: "https://auth.example.com/healthz",
    interval_s: 30,
    timeout_s: 5,
    enabled: true,
    status: "up",
    last_check: new Date().toISOString(),
    latency_ms: 82,
    uptime_24h: 100,
    created_at: new Date(Date.now() - 86_400_000).toISOString(),
    heartbeats: beats(60),
    tags: { env: "production" },
  },
  {
    id: 3,
    name: "nightly-database-backup-verification",
    type: "tcp",
    target: "db.internal.example:5432",
    interval_s: 300,
    timeout_s: 10,
    enabled: false,
    status: "up",
    last_check: new Date(Date.now() - 3_600_000).toISOString(),
    latency_ms: 11,
    uptime_24h: 99.9,
    created_at: new Date(Date.now() - 86_400_000).toISOString(),
    heartbeats: beats(60),
    tags: { env: "staging", team: "data" },
  },
  {
    id: 4,
    name: "cdn",
    type: "http",
    target: "https://cdn.example.com",
    interval_s: 60,
    timeout_s: 10,
    enabled: true,
    status: "up",
    last_check: new Date().toISOString(),
    latency_ms: 1840,
    uptime_24h: 99.12,
    created_at: new Date(Date.now() - 86_400_000).toISOString(),
    heartbeats: beats(60),
    tags: { env: "production" },
  },
];

export interface Server {
  url: string;
  close(): Promise<void>;
}

export async function serveBuild(): Promise<Server> {
  const http: HttpServer = createServer(async (req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");

    if (url.pathname === "/api/v1/monitors") {
      res.writeHead(200, { "content-type": "application/json; charset=utf-8" });
      res.end(JSON.stringify({ monitors: MONITORS }));
      return;
    }

    /*
     * An SSE endpoint that accepts the connection and then says nothing. The
     * dashboard needs the socket to open — otherwise it renders its
     * "connection lost" state, which is a different screen from the one under
     * test — but no event needs to arrive for a layout measurement.
     */
    if (url.pathname === "/api/v1/stream") {
      res.writeHead(200, {
        "content-type": "text/event-stream",
        "cache-control": "no-cache",
        connection: "keep-alive",
      });
      res.write(`event: hello\ndata: ${JSON.stringify({ ping_interval_ms: 30_000 })}\n\n`);
      return;
    }

    /*
     * The detail view fetches uptime windows and incident history. Both are
     * stubbed because the screen under measurement is the layout, not the
     * numbers — but they must answer, or the panel stays in its loading state
     * and the heartbeat bar it is supposed to draw never appears.
     */
    const uptime = url.pathname.match(/^\/api\/v1\/monitors\/[^/]+\/uptime$/);
    if (uptime) {
      res.writeHead(200, { "content-type": "application/json; charset=utf-8" });
      res.end(
        JSON.stringify({
          windows: [
            { window: "24h", uptime: 97.41, checks: 1440, failures: 37 },
            { window: "7d", uptime: 99.02, checks: 10080, failures: 99 },
            { window: "30d", uptime: 99.55, checks: 43200, failures: 194 },
          ],
        }),
      );
      return;
    }

    const incidents = url.pathname.match(/^\/api\/v1\/monitors\/[^/]+\/incidents$/);
    if (incidents) {
      res.writeHead(200, { "content-type": "application/json; charset=utf-8" });
      res.end(
        JSON.stringify({
          incidents: [
            {
              id: "42",
              started_at: new Date(Date.now() - 8 * 60_000).toISOString(),
              ended_at: null,
              error: "dial tcp 10.0.4.12:443: connect: connection refused",
            },
            {
              id: "41",
              started_at: new Date(Date.now() - 26 * 3_600_000).toISOString(),
              ended_at: new Date(Date.now() - 25 * 3_600_000).toISOString(),
              error: "context deadline exceeded (Client.Timeout exceeded while awaiting headers)",
            },
          ],
        }),
      );
      return;
    }

    if (url.pathname.startsWith("/api/")) {
      res.writeHead(404, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "not stubbed" }));
      return;
    }

    // Everything else is the SPA: serve the asset if it exists, else index.html.
    const rel = normalize(url.pathname).replace(/^(\.\.[/\\])+/, "");
    let file = join(DIST, rel);
    try {
      const s = await stat(file);
      if (s.isDirectory()) throw new Error("dir");
    } catch {
      file = join(DIST, "index.html");
    }
    res.writeHead(200, { "content-type": TYPES[extname(file)] ?? "application/octet-stream" });
    const stream = createReadStream(file);
    /*
     * Without this the request hangs instead of failing: an unhandled stream
     * error takes the process down mid-response, and the browser sits waiting
     * for bytes that never arrive. A test that times out after 30 seconds says
     * far less than a 500 naming the missing file.
     */
    stream.on("error", (err) => {
      res.destroy();
      console.error(`[harness] could not serve ${file}: ${err.message}`);
    });
    stream.pipe(res);
  });

  await new Promise<void>((resolve) => http.listen(0, "127.0.0.1", resolve));
  const addr = http.address();
  if (!addr || typeof addr === "string") throw new Error("server did not bind a port");

  return {
    url: `http://127.0.0.1:${addr.port}`,
    close: () =>
      new Promise<void>((resolve, reject) =>
        http.close((err) => (err ? reject(err) : resolve())),
      ),
  };
}
