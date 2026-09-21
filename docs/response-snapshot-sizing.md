# Response snapshot limits

Response capture keeps diagnostic evidence at the start of a failed HTTP check
sequence. The limits remain **three successfully stored snapshots per outage**
and **2048 bytes of body per snapshot**. This is a prefix of the response and of
the outage: neither limit promises that every cause or later change is captured.
Allowed response headers are additional storage, outside the body limit.

## Byte-cap decision

Keep the existing retention policy; **do not add a per-monitor or global
snapshot-byte admission cap**. This deliberately favors keeping the beginning
of each new, settled outage over a fixed snapshot storage allocation. A global
cap lets one noisy monitor prevent other monitors from recording their first
failure. A per-monitor cap avoids that interference but can withhold the next
unrelated outage's only response until retention runs. Evicting old snapshots
instead would shorten the diagnostic history and, without separate durable
accounting, could refund the outage allowance on restart.

This is a tradeoff, not a claim that three snapshots bounds total disk use.
Repeated settled outages receive fresh allowances. Unconfirmed failure/recovery
pairs do not trigger confirmed-state flapping suppression. Large allowlisted
headers can dominate body storage. The synthetic stress cases below demonstrate
these costs; the default is not a guarantee that arbitrary monitoring workloads
fit in 200 MB. Operators who need a fixed storage allocation must budget for the
actual failure rate and headers, reduce raw retention, or disable response
capture on the relevant monitors. No automatic byte ceiling is enforced.

Snapshots follow their raw heartbeat's foreign key: raw retention deletes them,
and hourly rollups do not keep bodies or headers. Retention runs daily and rounds
the cutoff down to the hour, so allow approximately an extra day and hour beyond
the configured raw window, plus WAL, indexes, other tables, and free-space
headroom. Failed retention or a long-lived reader delaying WAL checkpoints can
increase that requirement. Increasing raw retention increases retained snapshot
costs as well. This choice should be revisited if the product requires a fixed
snapshot allocation rather than retention-based diagnostic history; the sizing
harness supplies the repeated-outage and large-header workloads for that change.

## Synthetic disk measurements

Measured with the repository's Go SQLite driver and migrations on 2026-09-20.
These are **controlled synthetic fixtures, not production benchmarks**. Every
monitor produces 10,080 checks: seven days at one check per 60 seconds, retries
set to two, five-millisecond latency, and fixed 200/503 status/error values.
The daily pattern fails for five checks at the start of each day. Captured
bodies have exactly the candidate length, with one 64-byte `X-Request-Id` value
(serialized JSON costs another 19 bytes). Nothing contacts an external service.

| Synthetic workload | Monitors | Allowance | Body bytes | Stored snapshots | Payload bytes | SQLite file bytes |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Always healthy | 200 | 3 | 2048 | 0 | 0 | 107,651,072 |
| Daily outage | 200 | 1 | 2048 | 1,400 | 2,983,400 | 114,442,240 |
| Daily outage | 200 | 3 | 2048 | 4,200 | 8,950,200 | 127,418,368 |
| Daily outage | 200 | 5 | 2048 | 7,000 | 14,917,000 | 141,053,952 |
| Daily outage | 200 | 3 | 1024 | 4,200 | 4,649,400 | 127,418,368 |
| Daily outage | 200 | 3 | 4096 | 4,200 | 17,551,800 | 127,418,368 |
| Continuous outage | 200 | 3 | 2048 | 600 | 1,278,600 | 180,256,768 |
| Two failures / two successes, repeating | 200 | 3 | 2048 | 504,400 | 1,074,876,400 | 2,714,198,016 |
| Three failures every 15 minutes | 10 | 3 | 2048 | 20,160 | 42,960,960 | 107,208,704 |
| Unconfirmed failure / success, repeating | 10 | 3 | 2048 | 50,400 | 107,402,400 | 260,550,656 |
| Daily outage, 16 KiB header value | 200 | 3 | 2048 | 4,200 | 77,494,200 | 196,317,184 |

Changing only the daily fixture's allowed header from 64 to 16,384 bytes raised
the file from 127.4 to 196.3 MB. The 2048-byte constant limits the body, not the
whole stored response. Even larger allowed headers can cost more.

The four-minute pattern repeatedly crosses the engine's five-flips-in-ten-minutes
boundary. It suppresses some responses but becomes eligible again when old flips
expire: even repeated confirmed oscillation is **not** a total-byte ceiling.
The 15-minute pattern has enough quiet time to avoid suppression. The alternating
unconfirmed pattern never reaches the two-failure confirmation threshold.
The last two files really contain ten monitors; they are not measurements of
200 monitors. Multiplying them by 20 is a capacity estimate, not a measured file.

File sizes are actual `os.Stat` lengths after `wal_checkpoint(TRUNCATE)`, with
WAL size zero. The shared-memory file was 32,768 bytes (65,536 for the two large
oscillation cases). Used SQLite pages equalled file size in these fresh fixtures.
These figures are not peak live disk usage: a writable WAL needs additional room.
The study uses the real engine, snapshot selection, store writes and migrations
for one monitor, then replicates its raw rows to the other monitors in bounded
transactions. It excludes incident/outbox population, scheduler timing and other
production traffic. It therefore does not measure throughput or a complete
installation's steady state.

The earlier 180–200 MB raw-table sizing estimate is consistent with the continuous
failure fixture's 180.3 MB, but not a universal baseline. Healthy rows here use
107.7 MB; long error messages, headers, and response frequency change that cost.
In particular, 1024-, 2048- and 4096-byte bodies landed on the same total page
allocation in the daily fixture: SQLite's separate `WITHOUT ROWID` response table
and overflow pages make file size discontinuous. Halving body bytes did **not**
halve disk usage, and doubling them did **not** double this file. Payload bytes
still matter to detail-response size and to other row shapes.

An actual `ApplyRetention` pass on the three-snapshot daily fixture removed all
raw snapshots and shrank the file from **127,418,368 to 1,044,480 bytes**. Hourly
aggregates remained. The study uses fixed historical dates so all seven days are
eligible; this is a reclamation test, not a seven-day rolling equilibrium test.

## Why three and 2048

The synthetic evolving-outage test sends five real local HTTP failures: three
different early symptoms, a repeat of the third, and a new late symptom. The
runner keeps the first three across a restart, records budget suppression for the
remaining two, and does not charge unsuccessful writes (covered separately).
One snapshot misses both early changes. Five captures the late change at the
cost of 2800 extra stored responses and 13,635,584 extra file bytes in the daily
fixture. Three remains the default compromise, not an empirically proven optimum
for production incidents. It does not deduplicate or periodically sample a long
outage: later changes can be lost.

Six synthetic body fixtures exercise the actual checker prefix:

| Fixture | Full body bytes | Cause fits in 1024 | Cause fits in 2048 | Cause fits in 4096 |
| --- | ---: | :---: | :---: | :---: |
| Compact JSON | 41 | yes | yes | yes |
| Plain HTML | 51 | yes | yes | yes |
| Padded JSON context | 1454 | no | yes | yes |
| Multilingual context | 1829 | no | yes | yes |
| HTML with styles before cause | 3053 | no | no | yes |
| Script before cause | 9055 | no | no | no |

The 2048-byte default preserves the padded and multilingual causes that 1024
misses, while limiting each returned diagnostic body to half the 4096 alternative.
4096 helps the styled-page fixture but still misses the script-heavy one. The
actual UI marks truncation and retains request correlation headers; inspect the
service's own logs for omitted content. These six deliberately constructed cases
are examples of boundary behavior, **not** a success-rate survey of health
endpoints. They justify a conservative prefix choice without claiming that 2 KiB
is always enough.

## Reproduce

The opt-in Go test needs several minutes and approximately 4 GiB of free scratch
disk. It deletes each case's temporary database before starting the next. Choose
a disk-backed `TMPDIR`; do not put the study on a small RAM filesystem. Ordinary
unit test runs skip it, including under `-short`, unless the variable below is set.
The study emits JSON including actual file sizes and payload bytes.

```sh
TMPDIR=/path/to/scratch \
SUBGLANCE_SNAPSHOT_STUDY=/path/to/snapshot-sizing.json \
GOMAXPROCS=2 GOFLAGS=-p=1 GOMEMLIMIT=350MiB GOGC=20 \
go test -timeout 30m -short ./internal/monitor \
  -run '^TestSyntheticSnapshotSizing$' -count=1 -v

go test -short ./internal/checker \
  -run '^TestSyntheticSnapshotDiagnosticPrefixes$' -count=1 -v
go test -short ./internal/monitor \
  -run 'TestThreeSnapshotsKeepEvolvingSymptomsAcrossRestart|TestRepeatedSettledOutagesAreNotATotalByteCap' \
  -count=1 -v
```

Capture decisions remain visible in the monitor's **Failure responses** history:
flapping, the outage allowance being spent, or capture switched off for that
check. These are recorded facts about each check; they are not inferred from
today's settings. Old rows without a reason remain unknown. The existing engine
flag controls flapping suppression and clears when flips age out; no independent
capture timer is added. Budget spending still follows a successful atomic write,
and restart restores the stored count for the open outage.
