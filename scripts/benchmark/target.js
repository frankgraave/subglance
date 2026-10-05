// The page every benchmark monitor checks. It answers each request with the
// same small HTML page and counts requests by the first path segment, so
// /subglance/3 and /kuma-2/3 are counted apart and GET /counts reports how
// many checks each server really made.
const http = require("http");

const counts = {};
const page = "<!doctype html><title>benchmark</title>" + "<p>ok</p>".repeat(200);

http
  .createServer((req, res) => {
    if (req.url.startsWith("/counts")) {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify(counts));
      // /counts?reset=1 starts the count again, at the end of the warm-up.
      if (req.url.endsWith("?reset=1")) for (const k of Object.keys(counts)) delete counts[k];
      return;
    }
    const who = req.url.split("/")[1] || "other";
    counts[who] = (counts[who] || 0) + 1;
    res.setHeader("Content-Type", "text/html; charset=utf-8");
    res.end(page);
  })
  .listen(8000);
