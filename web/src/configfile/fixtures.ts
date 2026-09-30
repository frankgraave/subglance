import type { ImportReport } from "./api";

/** A dry run that creates, updates, leaves alone and needs a secret. */
export const dryRunReport: ImportReport = {
  dry_run: true,
  channels: [
    { key: "ops-slack", name: "Ops Slack", action: "create", needs_secrets: ["url"] },
    { key: "pager", name: "Pager", action: "unchanged" },
  ],
  monitors: [
    { key: "shop", name: "Webshop", action: "update", changes: ["name", "interval_s"] },
    { key: "api", name: "API", action: "unchanged" },
    { key: "nightly-backup", name: "Nightly backup", action: "create" },
  ],
  routing_rules: [{ name: "team=web", action: "create" }],
  maintenance: [],
  summary: { create: 3, update: 1, unchanged: 2, needs_secrets: 1 },
};

/** The same file applied: the created push monitor carries its URL, once. */
export const appliedReport: ImportReport = {
  ...dryRunReport,
  dry_run: false,
  monitors: dryRunReport.monitors.map((item) => item.key === "nightly-backup"
    ? { ...item, push_url: "https://status.example.test/api/v1/push/sgk_0123456789abcdef" } : item),
};

/** A re-import of a file that is already on the instance. */
export const unchangedReport: ImportReport = {
  dry_run: true,
  channels: [{ key: "pager", name: "Pager", action: "unchanged" }],
  monitors: [{ key: "api", name: "API", action: "unchanged" }],
  routing_rules: [],
  maintenance: [],
  summary: { create: 0, update: 0, unchanged: 2, needs_secrets: 0 },
};
