/**
 * A real HTTP server for the browser layout checks.
 *
 * Serves the production bundle from `internal/webui/dist` and stubs the API
 * calls the dashboard makes on load, including the session lookup that now
 * gates it. The stub is deliberate: these tests
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

/*
 * Channels in the wire shape `channelFromApi` reads, with the config values
 * already masked the way `internal/api/channels.go` masks them — `****` plus
 * the last four characters. A fixture that returned a plain webhook URL would
 * measure a row this product never renders.
 *
 * The set is chosen for width and for state, not for tidiness: five types, one
 * disabled, one with the longest realistic email recipient list, and one whose
 * type this build does not know — because "Unknown type" and "this build does
 * not know this channel type" are strings the layout has to hold, and nothing
 * else in the fixture produces them.
 */
const CHANNELS = [
  {
    id: 1,
    name: "platform-oncall-primary-escalation",
    type: "slack",
    config: { url: "****0f3a" },
    enabled: true,
    created_at: new Date(Date.now() - 40 * 86_400_000).toISOString(),
    updated_at: new Date(Date.now() - 2 * 86_400_000).toISOString(),
  },
  {
    id: 2,
    name: "Ops mailing list",
    type: "email",
    config: {
      to: "ops@acme-corporation.example, platform-oncall@acme-corporation.example",
      from: "subglance@acme-corporation.example",
      host: "smtp.acme-corporation.example",
      port: "587",
      username: "subglance",
      password: "********",
    },
    enabled: true,
    created_at: new Date(Date.now() - 30 * 86_400_000).toISOString(),
    updated_at: new Date(Date.now() - 30 * 86_400_000).toISOString(),
  },
  {
    id: 3,
    name: "Status page webhook",
    type: "webhook",
    config: { url: "****hook", headers: "****4f21" },
    enabled: true,
    created_at: new Date(Date.now() - 12 * 86_400_000).toISOString(),
    updated_at: new Date(Date.now() - 12 * 86_400_000).toISOString(),
  },
  {
    id: 4,
    name: "Weekend pager",
    type: "telegram",
    config: { bot_token: "****9xQ2", chat_id: "-1001234567890" },
    enabled: false,
    created_at: new Date(Date.now() - 5 * 86_400_000).toISOString(),
    updated_at: new Date(Date.now() - 86_400_000).toISOString(),
  },
  {
    id: 5,
    name: "Discord #incidents",
    type: "discord",
    config: { url: "****ab19" },
    enabled: true,
    created_at: new Date(Date.now() - 86_400_000).toISOString(),
    updated_at: new Date(Date.now() - 86_400_000).toISOString(),
  },
];

export interface Server {
  url: string;
  close(): Promise<void>;
}

export async function serveBuild(): Promise<Server> {
  const http: HttpServer = createServer(async (req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");

    /*
     * The session, answered as a signed-in administrator.
     *
     * `useSession` asks `/auth/me` before anything else and treats any status
     * other than 200 or 401 as "this instance is unreachable", which renders
     * an error card instead of the dashboard. Leaving it to the generic 404
     * below therefore measures the layout of the wrong screen — and does so by
     * timing out on a missing selector, which reads as a layout failure rather
     * than a missing stub. The identity is irrelevant to a layout
     * measurement; that a session resolves at all is not.
     */
    if (url.pathname === "/api/v1/auth/me") {
      res.writeHead(200, { "content-type": "application/json; charset=utf-8" });
      res.end(
        JSON.stringify({
          id: 1,
          email: "operator@example.com",
          role: "admin",
          created_at: new Date(Date.now() - 86_400_000).toISOString(),
        }),
      );
      return;
    }

    /*
     * The channel list. Without this route the notifications page fell through
     * to the 404 below, React Query retried, and the screen sat on "Loading
     * channels…" forever — which looked exactly like a product bug and wasted
     * a review pass (SUB-138). A layout harness that cannot render one of the
     * four screens is not a harness.
     */
    if (url.pathname === "/api/v1/channels") {
      /*
       * `SUBGLANCE_HARNESS_CHANNELS=none` serves the empty list. The empty
       * state is the first thing every new self-hoster sees and it had never
       * been looked at in a browser — the review that prompted SUB-138 in fact
       * mistook the *loading* line for it, because with no route here the page
       * never got past loading. A harness that can only draw the populated
       * case cannot catch that.
       */
      /*
       * `SUBGLANCE_HARNESS_CHANNELS=two` serves the first two.
       *
       * Five channels is not the shape most instances are in, and a layout
       * reviewed only at five is a layout nobody checked at the size it will
       * usually be seen. The rejection that prompted this redesign was written
       * against a real instance with two channels, where a caveat paragraph
       * that looks proportionate above five rows is taller than the list it
       * qualifies. Two is therefore a case the harness has to be able to draw,
       * rather than something a reviewer reproduces by editing the fixture and
       * remembering to put it back.
       */
      const mode = process.env.SUBGLANCE_HARNESS_CHANNELS;
      const served =
        mode === "none" ? [] : mode === "two" ? CHANNELS.slice(0, 2) : CHANNELS;
      res.writeHead(200, { "content-type": "application/json; charset=utf-8" });
      res.end(JSON.stringify({ channels: served }));
      return;
    }

    /*
     * Testing a channel, answered as a refusal rather than a success.
     *
     * The layout check presses this button to watch whether the row's controls
     * hold still while the label says "Sending test…", and a fixture that
     * returned `ok` would paint a green "Test delivered" chip — a claim this
     * product is careful never to make without a real delivery behind it, in a
     * harness that delivers nothing. The refusal is the honest stub, and it
     * also exercises the wider of the two result paragraphs.
     */
    const test = url.pathname.match(/^\/api\/v1\/channels\/[^/]+\/test$/);
    if (test) {
      /*
       * Answered after a beat, not instantly. A real test delivery opens a
       * socket to Slack or an SMTP server, so an instant reply is the one
       * timing this endpoint never has — and it made the row's in-flight
       * state ("Sending test…", the disabled button) unobservable, which is
       * precisely the state the layout check needs to measure. The delay is
       * the fixture being honest about the shape of the thing it stands in
       * for, not a sleep bolted on to make a test pass.
       */
      setTimeout(() => {
        res.writeHead(200, {
          "content-type": "application/json; charset=utf-8",
        });
        res.end(
          JSON.stringify({
            ok: false,
            error: "the layout harness does not deliver messages",
          }),
        );
      }, 400);
      return;
    }

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
