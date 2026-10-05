# Benchmark against Uptime Kuma

`run.py` runs SubGlance and Uptime Kuma side by side on one host and measures
what each costs to keep running: resident memory, CPU time and the size of its
data directory. The question it answers is a narrow one, "what does it take to
watch the same monitors at the same interval", not which tool is better.

## What it does

1. Starts four containers on a private Docker network: a small HTTP target,
   SubGlance, Uptime Kuma 1.23 and Uptime Kuma 2 (the `-slim` image, the
   lighter of Kuma 2's two, with SQLite). Each runs the image as published,
   with its data on a fresh volume and nothing but the settings below changed.
2. Adds the same monitors to each server through its own API: by default 50
   HTTP checks every 30 seconds with a 10-second timeout, each one a GET of a
   2 KB page. SubGlance needs `SUBGLANCE_ALLOW_PRIVATE_TARGETS=true` to check
   an address on the Docker network; Kuma allows that by default.
3. Waits ten minutes so that start-up, the first sign-in and the first checks
   are not counted.
4. Every 10 seconds reads, per container, from its cgroup and `/proc`:
   - **RSS**: the summed `VmRSS` of every process in the container, the
     same figure as in [Memory](../../docs/operations.md#memory).
   - **docker stats**: the cgroup's memory use minus inactive page cache,
     which is what `docker stats` shows.
   - **CPU**: the cgroup's `usage_usec`.
5. At the end writes `summary.json`: median, 95th percentile and maximum of
   memory, CPU time as a share of one core, the data directory's size, the
   image's download size, and how many checks each server actually made, as
   counted by the target. A server that falls behind its interval shows it
   there. `samples.csv` holds every sample.

Each container gets 1 GiB of memory and no swap, far above what any of them
uses, so that a busy host cannot flatter one by paging it out. The images'
own health checks are off: each probe is a process started inside the
container, and they probe at different rates.

## Running it

Linux with Docker and cgroup v2, Python 3 (standard library only), about
1 GB of free memory and 1 GB of disk.

```bash
docker pull ghcr.io/frankgraave/subglance:edge
docker pull louislam/uptime-kuma:1.23.16
docker pull louislam/uptime-kuma:2.5.5-slim
sudo python3 scripts/benchmark/run.py --hours 24 --out ./benchmark-result
```

Root is needed to read other containers' `/proc` entries. `--monitors`,
`--interval`, `--timeout`, `--kuma-image` (repeatable) and
`--subglance-image` change the setup; `--hours 0.1 --warmup 60` is a quick
check that everything starts. Ctrl-C or SIGTERM stops early and still writes a
summary of what was measured; a summary of the run so far is also rewritten
every ten minutes. `images.json` records the exact image digests.

## Reading the result

All servers run at the same time, so anything else on the host disturbs them
together: compare them with each other, not with a number from another
machine. The CPU figures are small enough that a single check's TLS handshake
or a garbage collection shows up, which is why the median and the 95th
percentile are given and not one reading. The target answers over plain HTTP
on the same host, so this measures the cost of the monitor, not of the
network.
