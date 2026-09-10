#!/usr/bin/env bash
# Diagnostic: does the scheduler actually fire a check for a new monitor?
set -uo pipefail
export PATH="$PATH:/usr/local/go/bin"
cd /root/projects/subglance

tmp="$(mktemp -d)"
go build -o "$tmp/sg" ./cmd/subglance || exit 1

python3 -m http.server 18099 --directory "$tmp" >/dev/null 2>&1 &
target_pid=$!

SUBGLANCE_ADDR=127.0.0.1:18081 \
SUBGLANCE_DATA_DIR="$tmp" \
SUBGLANCE_ALLOW_PRIVATE_TARGETS=true \
SUBGLANCE_LOG_LEVEL=debug \
  "$tmp/sg" >"$tmp/log.txt" 2>&1 &
sg_pid=$!

sleep 3
curl -sf -X POST http://127.0.0.1:18081/api/v1/setup -H 'Content-Type: application/json' \
  -d '{"email":"a@b.com","password":"correct-horse-battery-staple"}' >/dev/null
curl -sf -X POST http://127.0.0.1:18081/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"a@b.com","password":"correct-horse-battery-staple"}' -c "$tmp/c" >/dev/null
TOK="$(curl -sf -X POST http://127.0.0.1:18081/api/v1/tokens -b "$tmp/c" \
  -H 'Content-Type: application/json' -d '{"name":"t"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')"

curl -sf -X POST http://127.0.0.1:18081/api/v1/monitors -H "Authorization: Bearer $TOK" \
  -H 'Content-Type: application/json' \
  -d '{"name":"t","type":"http","target":"http://127.0.0.1:18099/","interval_seconds":5,"timeout_seconds":5}' >/dev/null
echo "monitor created; waiting 40s"
sleep 40

echo "=== LOG ==="
tail -30 "$tmp/log.txt"
echo "=== HEARTBEATS ==="
curl -sf "http://127.0.0.1:18081/api/v1/monitors/1/heartbeats" -H "Authorization: Bearer $TOK" | head -c 600
echo
kill $sg_pid $target_pid 2>/dev/null
rm -rf "$tmp"
