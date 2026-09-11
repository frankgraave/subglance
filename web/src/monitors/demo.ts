/**
 * Deterministic fixtures for the workbench.
 *
 * Deterministic on purpose: a dashboard that reshuffles on every hot reload
 * cannot be judged, and "does this look right at 200 rows" is a question you
 * answer by comparing two screenshots. The PRNG is written out here rather
 * than pulled from npm — it is nine lines, and a dependency for nine lines of
 * arithmetic is a dependency to keep patched forever.
 *
 * This file is fixture data, not product code. Real data arrives via the API
 * in SUB-26 and flows through `fromApi`.
 */

import type { ApiMonitor } from "./types";
import { fromApi } from "./types";
import type { Monitor } from "./types";

/**
 * mulberry32: a 32-bit PRNG with a full period and good enough distribution
 * for placing dots on a screen. Not for anything that needs real randomness.
 */
function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const SERVICES = [
  "api", "auth", "billing", "cdn", "checkout", "dashboard", "docs", "events",
  "gateway", "grafana", "images", "invoices", "jobs", "ldap", "mail", "metrics",
  "notifications", "payments", "postgres", "queue", "redis", "reports", "search",
  "sessions", "signup", "smtp", "staging", "status", "storage", "support",
  "uploads", "vault", "webhooks", "website", "worker",
];

const ENVIRONMENTS = ["prod", "staging", "eu", "us", "edge"];

/** A fixed clock, so snapshots of the workbench are comparable across runs. */
const NOW = Date.parse("2026-09-11T12:00:00Z");

function makeHeartbeats(
  rand: () => number,
  count: number,
  health: number,
  baseLatency: number,
): ApiMonitor["heartbeats"] {
  const beats: NonNullable<ApiMonitor["heartbeats"]> = [];
  for (let i = count - 1; i >= 0; i--) {
    const ok = rand() < health;
    // Latency wanders around its baseline with the occasional spike, so the
    // bar has a shape to read rather than a flat wall.
    const jitter = 0.6 + rand() * 0.9;
    const spike = rand() < 0.06 ? 3 + rand() * 4 : 1;
    beats.push({
      ts: new Date(NOW - i * 60_000).toISOString(),
      ok,
      latency_ms: ok ? Math.round(baseLatency * jitter * spike) : null,
      status_code: ok ? 200 : 502,
      error: ok ? undefined : "502 Bad Gateway",
    });
  }
  return beats;
}

/**
 * Builds `count` monitors from `seed`. The same arguments always produce the
 * same list, including the same statuses and the same heartbeat history.
 */
export function demoMonitors(count: number, seed = 20260911): Monitor[] {
  const rand = mulberry32(seed);
  const api: ApiMonitor[] = [];

  for (let i = 0; i < count; i++) {
    const service = SERVICES[i % SERVICES.length];
    const env = ENVIRONMENTS[Math.floor(i / SERVICES.length) % ENVIRONMENTS.length];
    const name = count > SERVICES.length ? `${service}-${env}` : service;

    const roll = rand();
    // Weighted so the list looks like a real estate: mostly healthy, a couple
    // of genuine failures, a few not yet checked and the odd paused one. A
    // fixture where a third of everything is broken flatters the design.
    const enabled = roll >= 0.94 ? false : true;
    const status: ApiMonitor["status"] =
      roll < 0.055 ? "down" : roll < 0.085 ? "pending" : "up";

    const baseLatency = 20 + Math.floor(rand() * 380);
    const health = status === "down" ? 0.72 : 0.985;
    const neverChecked = status === "pending" && rand() < 0.6;

    api.push({
      id: `mon-${String(i + 1).padStart(3, "0")}`,
      name,
      type: "http",
      target: `https://${name}.example.com/healthz`,
      interval_s: 60,
      timeout_s: 10,
      enabled,
      status,
      // A pending monitor may genuinely never have run: that is the case that
      // has to render as an em dash, not as 0.
      last_check: neverChecked ? null : new Date(NOW - Math.floor(rand() * 60_000)).toISOString(),
      latency_ms: status === "down" || neverChecked ? null : baseLatency,
      status_code: status === "down" ? 502 : 200,
      error: status === "down" ? "502 Bad Gateway" : undefined,
      uptime_24h: neverChecked ? null : Math.round((health * 100 - rand() * 1.4) * 100) / 100,
      created_at: new Date(NOW - 86_400_000 * 30).toISOString(),
      heartbeats: neverChecked ? [] : makeHeartbeats(rand, 40, health, baseLatency),
    });
  }

  return api.map(fromApi);
}
