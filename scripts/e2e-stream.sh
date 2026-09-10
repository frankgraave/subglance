#!/usr/bin/env bash
# End-to-end proof that the live stream carries real check results.
#
# Boots a real subglance, creates an account and a monitor pointing at a local
# target, opens the SSE stream, and asserts that heartbeats actually arrive.
# This is the test the unit tests cannot do: it exercises the whole path from
# scheduler through the bus to an HTTP client.
set -euo pipefail

cd "$(dirname "$0")/.."
export PATH="$PATH:/usr/local/go/bin"

tmp="$(mktemp -d)"
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$tmp"' EXIT

echo "building..."
go build -o "$tmp/subglance" ./cmd/subglance

# A target that always answers, so the monitor has something real to check.
python3 -m http.server 18099 --directory "$tmp" >/dev/null 2>&1 &
sleep 1

SUBGLANCE_ADDR=127.0.0.1:18080 \
SUBGLANCE_DB="$tmp/sg.db" \
SUBGLANCE_ALLOW_PRIVATE_TARGETS=true \
SUBGLANCE_DATA_DIR="$tmp" \
  "$tmp/subglance" >"$tmp/server.log" 2>&1 &

for _ in $(seq 1 40); do
  curl -sf http://127.0.0.1:18080/health >/dev/null 2>&1 && break
  sleep 0.25
done
curl -sf http://127.0.0.1:18080/health >/dev/null || { echo "server never came up"; cat "$tmp/server.log"; exit 1; }
echo "server up"

curl -sf -X POST http://127.0.0.1:18080/api/v1/setup \
  -H 'Content-Type: application/json' \
  -d '{"email":"a@example.com","password":"correct-horse-battery-staple"}' >/dev/null
echo "admin created"

token="$(curl -sf -X POST http://127.0.0.1:18080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"a@example.com","password":"correct-horse-battery-staple"}' \
  -c "$tmp/cookies" >/dev/null && \
  curl -sf -X POST http://127.0.0.1:18080/api/v1/tokens \
  -b "$tmp/cookies" -H 'Content-Type: application/json' \
  -d '{"name":"e2e"}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')"
echo "token acquired"

# Not -sf: a silent failure here used to leave the script looking like it
# hung on the stream, when in fact no monitor was ever created. The API
# rejects unknown fields, so a renamed field must fail loudly, not quietly.
create_status="$(curl -s -o "$tmp/create.json" -w '%{http_code}' \
  -X POST http://127.0.0.1:18080/api/v1/monitors \
  -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  -d '{"name":"local target","type":"http","target":"http://127.0.0.1:18099/","interval_s":20,"timeout_s":5}')"
if [ "$create_status" != "201" ]; then
  echo "FAIL: creating the monitor returned HTTP $create_status"
  cat "$tmp/create.json"
  exit 1
fi
echo "monitor created"

echo "listening on the stream for 50s..."
# 20s is the schema minimum for an interval; anything lower is rejected at
# creation time. The window still has to clear the scheduler's 30s reload
# before the monitor is even picked up, so 50s is the floor for this to be
# a test of the stream rather than of the reload timer.
timeout 50 curl -sN http://127.0.0.1:18080/api/v1/stream \
  -H "Authorization: Bearer $token" >"$tmp/stream.txt" 2>/dev/null || true

echo
echo "--- stream ---"
cat "$tmp/stream.txt"
echo "--- end ---"
echo

fail=0
grep -q 'event: hello'     "$tmp/stream.txt" || { echo "FAIL: no hello frame"; fail=1; }
grep -q 'event: heartbeat' "$tmp/stream.txt" || { echo "FAIL: no heartbeat arrived"; fail=1; }
grep -q '"ok":true'        "$tmp/stream.txt" || { echo "FAIL: heartbeat carried no result"; fail=1; }
grep -q '^id: '            "$tmp/stream.txt" || { echo "FAIL: no id line, resume impossible"; fail=1; }

if [ "$fail" -eq 0 ]; then
  echo "PASS: real check results reached the stream"
else
  echo; echo "--- server log ---"; tail -30 "$tmp/server.log"
fi
exit "$fail"
