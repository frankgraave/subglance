#!/usr/bin/env python3
"""Measure SubGlance and Uptime Kuma side by side: the same monitors, the same
interval, the same target, on the same host, at the same time.

Each server runs in its own container and checks its own copy of N HTTP
monitors against one shared target container. The script reads memory and CPU
from each container's cgroup every few seconds and, after a warm-up that
leaves out start-up and the first sign-in, reports the steady state.

Linux with cgroup v2 and Docker only: the numbers come straight from
/sys/fs/cgroup and /proc. Standard library only. See README.md beside this
file for what is measured and how to read it.
"""
import argparse
import json
import os
import signal
import statistics
import subprocess
import time
import urllib.request
import http.cookiejar

HERE = os.path.dirname(os.path.abspath(__file__))
SG_PASSWORD = "benchmark-password-1"


def sh(*args, check=True):
    return subprocess.run(args, check=check, capture_output=True, text=True).stdout.strip()


class Server:
    """One container under measurement, started attached so that stopping
    this script stops it, and with --rm so its anonymous volume goes too."""

    def __init__(self, name, image, host_port, container_port, args, env=None, data_dir=None):
        self.name, self.image, self.data_dir = name, image, data_dir
        self.host_port, self.container_port = host_port, container_port
        self.args, self.env = args, env or {}
        self.proc = None
        self.cgroup = None
        self.container = None
        self.pid = None

    def start(self, network, run_id, alias=None):
        self.container = "%s-%s" % (run_id, self.name)
        cmd = ["docker", "run", "--rm", "--no-healthcheck", "--name", self.container, "--network", network,
               "-p", "127.0.0.1:%d:%d" % (self.host_port, self.container_port)]
        if alias:
            cmd += ["--network-alias", alias]
        for k, v in self.env.items():
            cmd += ["-e", "%s=%s" % (k, v)]
        cmd += self.args
        self.log = open(os.path.join(OUT, self.name + ".log"), "w")
        self.proc = subprocess.Popen(cmd, stdout=self.log, stderr=subprocess.STDOUT)

    def find_cgroup(self):
        for _ in range(300):
            pid = sh("docker", "inspect", "--format", "{{.State.Pid}}", self.container, check=False)
            if pid and pid != "0":
                self.pid = pid
                with open("/proc/%s/cgroup" % pid) as f:
                    rel = f.read().strip().split("::", 1)[1]
                self.cgroup = "/sys/fs/cgroup" + rel
                return
            time.sleep(0.2)
        raise SystemExit("%s: container did not start, see %s.log" % (self.name, self.name))

    def wait_http(self, path):
        url = "http://127.0.0.1:%d%s" % (self.host_port, path)
        deadline = time.time() + 300
        while time.time() < deadline:
            try:
                urllib.request.urlopen(url, timeout=2)
                return
            except Exception:
                time.sleep(1)
        raise SystemExit("%s did not answer on %s" % (self.name, url))

    def disk_bytes(self):
        """Size of the data directory, read through the container's root."""
        if not self.data_dir:
            return 0
        root = "/proc/%s/root%s" % (self.pid, self.data_dir)
        total = 0
        for dirpath, _, files in os.walk(root):
            for name in files:
                try:
                    total += os.lstat(os.path.join(dirpath, name)).st_size
                except FileNotFoundError:
                    pass
        return total

    def sample(self):
        """Resident memory of every process in the container, the figure
        `docker stats` shows, swap, and CPU time used so far."""
        def read(name):
            with open(os.path.join(self.cgroup, name)) as f:
                return f.read()
        rss_kb = 0
        for pid in read("cgroup.procs").split():
            try:
                with open("/proc/%s/status" % pid) as f:
                    for line in f:
                        if line.startswith("VmRSS:"):
                            rss_kb += int(line.split()[1])
            except FileNotFoundError:
                pass
        stat = dict(line.split() for line in read("memory.stat").splitlines())
        current = int(read("memory.current"))
        try:
            swap = int(read("memory.swap.current"))
        except FileNotFoundError:
            swap = 0
        cpu = dict(line.split() for line in read("cpu.stat").splitlines())
        return {
            "rss": rss_kb * 1024,
            "docker_stats": current - int(stat.get("inactive_file", 0)),
            "swap": swap,
            "cpu_usec": int(cpu["usage_usec"]),
        }


def seed_subglance(server, count, interval, timeout, prefix):
    base = "http://127.0.0.1:%d" % server.host_port
    jar = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def post(path, body):
        req = urllib.request.Request(base + path, data=json.dumps(body).encode(), method="POST",
                                     headers={"Content-Type": "application/json"})
        return jar.open(req, timeout=60)

    creds = {"email": "bench@example.com", "password": SG_PASSWORD}
    post("/api/v1/setup", creds)
    post("/api/v1/auth/login", creds)
    for i in range(1, count + 1):
        post("/api/v1/monitors", {"name": "Benchmark %d" % i, "type": "http", "target": prefix + str(i),
                                  "interval_s": interval, "timeout_s": timeout})


def seed_kuma(server, count, interval, timeout, prefix):
    subprocess.run(["docker", "exec", "-e", "MONITORS=%d" % count, "-e", "INTERVAL=%d" % interval,
                    "-e", "TIMEOUT=%d" % timeout, "-e", "TARGET_PREFIX=" + prefix,
                    server.container, "node", "/bench/seed-kuma.js"], check=True, timeout=600)


def pct(values, p):
    values = sorted(values)
    return values[min(len(values) - 1, int(round(p / 100 * (len(values) - 1))))]


def summarise(servers, rows, start, counts):
    """Steady-state figures from the samples taken after the warm-up."""
    mib = 1024 * 1024
    out = {"window_hours": round((rows[-1]["t"] - start) / 3600, 2) if rows else 0, "servers": {}}
    for s in servers:
        mine = [r[s.name] for r in rows]
        if len(mine) < 2:
            continue
        rss = [m["rss"] for m in mine]
        ds = [m["docker_stats"] for m in mine]
        cpu_s = (mine[-1]["cpu_usec"] - mine[0]["cpu_usec"]) / 1e6
        wall = rows[-1]["t"] - rows[0]["t"]
        per_min = []
        step = max(1, int(60 / ARGS.sample))
        for i in range(step, len(mine), step):
            dt = rows[i]["t"] - rows[i - step]["t"]
            per_min.append((mine[i]["cpu_usec"] - mine[i - step]["cpu_usec"]) / 1e6 / dt * 100)
        out["servers"][s.name] = {
            "image": s.image,
            "rss_mib": {"median": round(statistics.median(rss) / mib, 1), "p95": round(pct(rss, 95) / mib, 1),
                        "max": round(max(rss) / mib, 1)},
            "docker_stats_mib": {"median": round(statistics.median(ds) / mib, 1), "max": round(max(ds) / mib, 1)},
            "swap_mib_max": round(max(m["swap"] for m in mine) / mib, 1),
            "cpu_seconds": round(cpu_s, 1),
            "cpu_percent_of_one_core": {"mean": round(cpu_s / wall * 100, 2) if wall else 0,
                                        "p95_per_minute": round(pct(per_min, 95), 2) if per_min else 0},
            "checks_answered": counts.get(s.name, 0),
            "data_dir_mib": round(s.disk_bytes() / mib, 1),
            # The compressed size, what a pull downloads.
            "image_download_mib": round(int(sh("docker", "image", "inspect", "--format", "{{.Size}}", s.image) or 0) / mib, 1),
        }
    return out


def write_summary(servers, rows, start, target_port, final=False):
    try:
        counts = json.load(urllib.request.urlopen("http://127.0.0.1:%d/counts" % target_port, timeout=5))
    except Exception:
        counts = {}
    summary = summarise(servers, rows, start, counts)
    summary["final"] = final
    summary["setup"] = {"monitors": ARGS.monitors, "interval_s": ARGS.interval, "timeout_s": ARGS.timeout,
                        "warmup_s": ARGS.warmup, "sample_s": ARGS.sample,
                        "host": {"kernel": os.uname().release, "cpus": os.cpu_count()}}
    with open(os.path.join(OUT, "summary.json.tmp"), "w") as f:
        json.dump(summary, f, indent=2)
    os.replace(os.path.join(OUT, "summary.json.tmp"), os.path.join(OUT, "summary.json"))


def main():
    global ARGS, OUT
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    p.add_argument("--hours", type=float, default=24)
    p.add_argument("--monitors", type=int, default=50)
    p.add_argument("--interval", type=int, default=30, help="seconds between checks (Kuma's minimum is 20)")
    p.add_argument("--timeout", type=int, default=10)
    p.add_argument("--warmup", type=int, default=600, help="seconds left out after seeding")
    p.add_argument("--sample", type=int, default=10, help="seconds between samples")
    p.add_argument("--subglance-image", default="ghcr.io/frankgraave/subglance:edge")
    p.add_argument("--kuma-image", action="append",
                   help="repeatable; default: louislam/uptime-kuma:1.23.16 and :2.5.5-slim")
    p.add_argument("--out", default=os.path.join(os.getcwd(), "benchmark-" + time.strftime("%Y%m%d-%H%M%S")))
    ARGS = p.parse_args()
    OUT = ARGS.out
    os.makedirs(OUT, exist_ok=True)
    kuma_images = ARGS.kuma_image or ["louislam/uptime-kuma:1.23.16", "louislam/uptime-kuma:2.5.5-slim"]

    run_id = "sgbench%d" % os.getpid()
    network = run_id
    sh("docker", "network", "create", network)
    target_port = 18300
    # Any image with node will do for the target; Kuma's is already pulled.
    target = Server("target", kuma_images[-1], target_port, 8000,
                    ["--init", "-v", HERE + ":/bench:ro", "--entrypoint", "node", kuma_images[-1], "/bench/target.js"])
    # Every server runs its image as shipped, on its default port, with its
    # data on an anonymous volume.
    servers = [Server("subglance", ARGS.subglance_image, 18301, 8080, ["-v", "/data", ARGS.subglance_image],
                      {"SUBGLANCE_ALLOW_PRIVATE_TARGETS": "true"}, data_dir="/data")]
    for i, image in enumerate(kuma_images):
        tag = image.rsplit(":", 1)[-1].replace("-slim", "")
        servers.append(Server("kuma-" + tag, image, 18302 + i, 3001,
                              ["-v", "/app/data", "-v", HERE + ":/bench:ro", image],
                              {"UPTIME_KUMA_DB_TYPE": "sqlite"}, data_dir="/app/data"))

    everything = [target] + servers

    def stop():
        for s in everything:
            if s.proc and s.proc.poll() is None:
                s.proc.send_signal(signal.SIGTERM)
        for s in everything:
            if s.proc:
                try:
                    s.proc.wait(timeout=60)
                except subprocess.TimeoutExpired:
                    sh("docker", "kill", s.container, check=False)
        sh("docker", "network", "rm", network, check=False)

    def interrupted(*_):
        raise KeyboardInterrupt

    # A SIGTERM ends the run like Ctrl-C: summary of what was measured, then
    # every container stopped.
    signal.signal(signal.SIGTERM, interrupted)
    rows = []
    start = None
    try:
        target.start(network, run_id, alias="target")
        for s in servers:
            s.start(network, run_id)
        for s in servers:
            s.find_cgroup()
        servers[0].wait_http("/health")
        for s in servers[1:]:
            s.wait_http("/")
        for s in servers:
            prefix = "http://target:8000/%s/" % s.name
            if s.name == "subglance":
                seed_subglance(s, ARGS.monitors, ARGS.interval, ARGS.timeout, prefix)
            else:
                seed_kuma(s, ARGS.monitors, ARGS.interval, ARGS.timeout, prefix)
        versions = {s.name: sh("docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", s.image,
                               check=False) for s in servers}
        with open(os.path.join(OUT, "images.json"), "w") as f:
            json.dump(versions, f, indent=2)
        print("seeded; warming up for %ds" % ARGS.warmup, flush=True)
        time.sleep(ARGS.warmup)
        # Reset the target's counts so they cover the measured window only.
        urllib.request.urlopen("http://127.0.0.1:%d/counts?reset=1" % target_port, timeout=5)
        start = time.time()
        end = start + ARGS.hours * 3600
        csv = open(os.path.join(OUT, "samples.csv"), "w")
        csv.write("t,server,rss,docker_stats,swap,cpu_usec\n")
        last_summary = start
        while time.time() < end:
            row = {"t": time.time()}
            for s in servers:
                m = s.sample()
                row[s.name] = m
                csv.write("%.1f,%s,%d,%d,%d,%d\n" % (row["t"], s.name, m["rss"], m["docker_stats"], m["swap"], m["cpu_usec"]))
            csv.flush()
            rows.append(row)
            if time.time() - last_summary >= 600:
                write_summary(servers, rows, start, target_port)
                last_summary = time.time()
            for s in everything:
                if s.proc.poll() is not None:
                    raise SystemExit("%s exited early, see %s.log" % (s.name, s.name))
            time.sleep(max(0, row["t"] + ARGS.sample - time.time()))
        write_summary(servers, rows, start, target_port, final=True)
        print(open(os.path.join(OUT, "summary.json")).read())
    except KeyboardInterrupt:
        if rows:
            write_summary(servers, rows, start, target_port)
    finally:
        stop()


if __name__ == "__main__":
    main()
