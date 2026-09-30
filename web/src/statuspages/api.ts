import { apiJSON, apiPost, apiRequest } from "../api/http";

/**
 * Status pages as the admin API returns them (`/api/v1/status-pages`).
 *
 * This is the operator's view: it carries monitor ids and is never what a
 * visitor sees. The public answer is a different type on a different route.
 */
export type Selection = "monitors" | "tag";

export interface StatusPageEntry {
  monitor_id: number;
  public_key: string;
  display_name: string;
}

export interface StatusPage {
  id: number;
  slug: string;
  title: string;
  description: string;
  timezone: string;
  selection: Selection;
  tag_key: string;
  tag_value: string;
  indexable: boolean;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  entries: StatusPageEntry[];
  /** For a tag page: tagged monitors with no public name, so not shown. */
  unnamed_monitor_ids: number[];
}

/** The settings a create or an update sends. Every field, every time. */
export type StatusPageSettings = Pick<StatusPage,
  "slug" | "title" | "description" | "timezone" | "selection" | "tag_key" | "tag_value" | "indexable" | "enabled">;

export type EntryInput = { monitor_id: number; display_name: string };

export const statusPagesKey = ["status-pages"] as const;

const text = (value: unknown) => typeof value === "string";
const id = (value: unknown) => typeof value === "number" && Number.isInteger(value) && value > 0;

function validEntry(value: unknown): value is StatusPageEntry {
  if (!value || typeof value !== "object") return false;
  const e = value as Record<string, unknown>;
  return id(e.monitor_id) && text(e.public_key) && text(e.display_name);
}

function validPage(value: unknown): value is StatusPage {
  if (!value || typeof value !== "object") return false;
  const p = value as Record<string, unknown>;
  return id(p.id) && [p.slug, p.title, p.description, p.timezone, p.tag_key, p.tag_value, p.created_at, p.updated_at].every(text) &&
    (p.selection === "monitors" || p.selection === "tag") &&
    typeof p.indexable === "boolean" && typeof p.enabled === "boolean" &&
    Array.isArray(p.entries) && p.entries.every(validEntry) &&
    Array.isArray(p.unnamed_monitor_ids) && p.unnamed_monitor_ids.every(id);
}

function page(data: unknown): StatusPage {
  // A page misread as switched off would be offered back that way and saved,
  // taking a published page down; anything off-shape is an error instead.
  if (!validPage(data)) throw new Error("The server's answer about this status page was unreadable. Reload the page.");
  return data;
}

export async function fetchStatusPages(signal?: AbortSignal): Promise<StatusPage[]> {
  const data = await apiJSON<unknown>("/api/v1/status-pages", { signal, cache: "no-store" });
  const pages = (data as { pages?: unknown } | null)?.pages;
  if (!Array.isArray(pages) || !pages.every(validPage)) throw new Error("Status pages unavailable.");
  return pages;
}

const path = (slug: string) => `/api/v1/status-pages/${encodeURIComponent(slug)}`;

function put(url: string, body: unknown): Promise<Response> {
  return apiRequest(url, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}

export async function createStatusPage(settings: StatusPageSettings): Promise<StatusPage> {
  return page(await (await apiPost("/api/v1/status-pages", settings)).json());
}

/** Replaces every setting of the page now at `slug`; `settings.slug` may rename it. */
export async function updateStatusPage(slug: string, settings: StatusPageSettings): Promise<StatusPage> {
  return page(await (await put(path(slug), settings)).json());
}

/** Makes `entries` the page's complete list of monitors, in that order. */
export async function setStatusPageEntries(slug: string, entries: EntryInput[]): Promise<StatusPage> {
  return page(await (await put(`${path(slug)}/entries`, { entries })).json());
}

export async function deleteStatusPage(slug: string): Promise<void> {
  await apiRequest(path(slug), { method: "DELETE" });
}

/**
 * A slug suggested from a title: what a person would type for the address.
 *
 * Accents are folded rather than dropped ("Café" gives "cafe"), anything else
 * becomes a single dash, and the result is cut to the 63 characters a DNS
 * label allows, which is also the server's limit. The server still decides:
 * a reserved word or a taken slug comes back as an error on the field.
 */
export function slugFrom(title: string): string {
  return title.normalize("NFKD").replace(/[\u0300-\u036f]/g, "").toLowerCase()
    .replace(/[^a-z0-9]+/g, "-").replace(/^-+/, "").slice(0, 63).replace(/-+$/, "");
}
