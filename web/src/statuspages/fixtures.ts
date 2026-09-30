import type { ApiMonitor } from "../monitors/types";
import type { StatusPage } from "./api";

const monitor = (id: number, name: string, tags?: Record<string, string>): ApiMonitor => ({
  id, name, type: "http", target: `https://${name}.internal.example/healthz`, interval_s: 60, timeout_s: 10,
  enabled: true, status: "up", created_at: "2026-09-01T08:00:00Z", ...(tags ? { tags } : {}),
});

/** Three monitors whose own names must never be offered as public ones. */
export const pageMonitors: ApiMonitor[] = [
  monitor(1, "api-eu-west-1", { customer: "acme" }),
  monitor(2, "checkout-prod", { customer: "acme" }),
  monitor(3, "billing-worker"),
];

/** A published page with one service, and a tag page with a monitor still unnamed. */
export const samplePages: StatusPage[] = [
  {
    id: 1, slug: "status", title: "Acme services", description: "", timezone: "Europe/Amsterdam", selection: "monitors",
    tag_key: "", tag_value: "", indexable: false, enabled: true,
    created_at: "2026-09-20T08:00:00Z", updated_at: "2026-09-21T08:00:00Z",
    entries: [{ monitor_id: 1, public_key: "3f9a0c1d5e7b2a48", display_name: "API" }],
    unnamed_monitor_ids: [],
  },
  {
    id: 2, slug: "acme", title: "Acme for customers", description: "", timezone: "UTC", selection: "tag",
    tag_key: "customer", tag_value: "acme", indexable: false, enabled: false,
    created_at: "2026-09-22T08:00:00Z", updated_at: "2026-09-22T08:00:00Z",
    entries: [{ monitor_id: 1, public_key: "8b1e22c0f4d39a67", display_name: "API" }],
    unnamed_monitor_ids: [2],
  },
];
