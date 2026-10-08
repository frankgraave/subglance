import { apiRequest } from "../api/http";

/**
 * The configuration file endpoints (`docs/configuration-files.md`): export
 * writes one YAML document, import reads one back, with a dry run first.
 */

export type ImportAction = "create" | "update" | "unchanged";

/** What an import does, or would do, to one object. */
export interface ImportItem {
  /** A monitor's or channel's key, or a status page's slug. */
  key?: string;
  /** The object's name; `key=value` for a routing rule. */
  name?: string;
  action: ImportAction;
  /** The fields an update changes, by their name in the file. */
  changes?: string[];
  /** Withheld values the instance has nothing for: the object is saved switched off. */
  needs_secrets?: string[];
  /** A created push monitor's URL. This response is the only place it exists. */
  push_url?: string;
}

export interface ImportReport {
  dry_run: boolean;
  channels: ImportItem[];
  monitors: ImportItem[];
  routing_rules: ImportItem[];
  maintenance: ImportItem[];
  status_pages: ImportItem[];
  summary: { create: number; update: number; unchanged: number; needs_secrets: number };
}

/** The five groups, in the order the server plans them and the report lists them. */
export const GROUPS = [
  ["channels", "Channels"],
  ["monitors", "Monitors"],
  ["routing_rules", "Routing rules"],
  ["maintenance", "Maintenance windows"],
  ["status_pages", "Status pages"],
] as const;

/** The server refuses a larger body with a 413; refusing it here saves the upload. */
export const MAX_FILE_BYTES = 4 << 20;

/** The name the server's own download uses. */
const FILE_NAME = "subglance-config.yaml";

/**
 * Downloads the export as a file.
 *
 * Through fetch and a blob rather than a plain link, so a refused or failed
 * export surfaces as a sentence on the card. A link would navigate the tab to
 * a JSON error body, or to the login screen's 401, and leave the reader there.
 */
export async function downloadConfig(): Promise<void> {
  const response = await apiRequest("/api/v1/config/export", { cache: "no-store", headers: { Accept: "application/yaml" } });
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = FILE_NAME;
  document.body.append(link);
  link.click();
  link.remove();
  // Revoked on the next task, not synchronously: a browser that starts the
  // download after the click handler returns would find the URL gone.
  setTimeout(() => URL.revokeObjectURL(url));
}

/**
 * Sends a file to the importer. With `dryRun`, nothing is written.
 *
 * A 400 arrives as an `ApiError` whose `field` points into the file, such as
 * `monitors[2].interval_s`.
 */
export async function importConfig(text: string, dryRun: boolean): Promise<ImportReport> {
  const response = await apiRequest(`/api/v1/config/import${dryRun ? "?dry_run=true" : ""}`, {
    method: "POST",
    headers: { "Content-Type": "application/yaml" },
    body: text,
    cache: "no-store",
  });
  const data: unknown = await response.json().catch(() => null);
  // A report misread as "nothing to do" would hide an import that is about to
  // change the instance, or one that already has. Off-shape is an error.
  if (!validReport(data) || data.dry_run !== dryRun) {
    throw new Error(dryRun
      ? "The check ran, but its report was unreadable. Nothing was imported."
      : "The import ran, but its report was unreadable. Check the Monitors and Notifications screens for what changed.");
  }
  return data;
}

const ACTIONS: readonly string[] = ["create", "update", "unchanged"];
const isCount = (value: unknown) => typeof value === "number" && Number.isInteger(value) && value >= 0;
const optionalText = (value: unknown) => value === undefined || typeof value === "string";
const optionalWords = (value: unknown) => value === undefined || (Array.isArray(value) && value.every((word) => typeof word === "string"));

function validItem(value: unknown): value is ImportItem {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const item = value as Record<string, unknown>;
  return ACTIONS.includes(item.action as string) && optionalText(item.key) && optionalText(item.name) &&
    optionalText(item.push_url) && optionalWords(item.changes) && optionalWords(item.needs_secrets);
}

function validReport(value: unknown): value is ImportReport {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const report = value as Record<string, unknown>;
  if (typeof report.dry_run !== "boolean") return false;
  if (!GROUPS.every(([group]) => Array.isArray(report[group]) && (report[group] as unknown[]).every(validItem))) return false;
  const summary = report.summary as Record<string, unknown> | null;
  return !!summary && typeof summary === "object" &&
    ["create", "update", "unchanged", "needs_secrets"].every((count) => isCount(summary[count]));
}

/** The items that do something, in file order; unchanged ones are only counted. */
export const changing = (items: ImportItem[]) => items.filter((item) => item.action !== "unchanged");

/** Created push monitors whose URL has to be shown before it is gone. */
export const pushUrls = (report: ImportReport) =>
  report.monitors.flatMap((item) => item.push_url ? [{ url: item.push_url, name: item.name || item.key || "The new push monitor" }] : []);
