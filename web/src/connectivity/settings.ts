import { apiRequest } from "../api/http";

/** Where a setting's value came from: built in, saved here, or fixed by a flag or variable. */
export type SettingSource = "default" | "database" | "pinned";

export interface ConnectivitySetting<T> {
  value: T;
  source: SettingSource;
  /** The flag or variable that fixes the value, when `source` is "pinned". */
  pinned_by: string | null;
}

/**
 * GET and PUT /api/v1/settings/connectivity (SUB-151): whether the check
 * runs, which `host:port` addresses it dials, and where each came from.
 * Administrators only, because a target can name a host on the operator's
 * own network.
 */
export interface ConnectivitySettings {
  enabled: ConnectivitySetting<boolean>;
  targets: ConnectivitySetting<string[]>;
  default_targets: string[];
  max_targets: number;
  /**
   * The ETag that came with these settings, sent back as If-Match so a save
   * cannot overwrite a change it never saw. Client-side only; null when the
   * response carried none.
   */
  etag: string | null;
}

/** A save: only the fields that changed and are not pinned. */
export type ConnectivityChange = { enabled?: boolean; targets?: string[] };

export const connectivitySettingsKey = ["connectivity-settings"] as const;

const URL = "/api/v1/settings/connectivity";

export async function fetchConnectivitySettings(signal?: AbortSignal): Promise<ConnectivitySettings> {
  return readSettings(await apiRequest(URL, { signal, cache: "no-store" }));
}

/**
 * Conditional on `etag`: if anyone saved since it was read, the server
 * answers 412 (an `ApiError`) and writes nothing.
 */
export async function saveConnectivitySettings(body: ConnectivityChange, etag: string): Promise<ConnectivitySettings> {
  return readSettings(await apiRequest(URL, {
    method: "PUT",
    headers: { "Content-Type": "application/json", "If-Match": etag },
    body: JSON.stringify(body),
  }));
}

async function readSettings(res: Response): Promise<ConnectivitySettings> {
  const data: unknown = await res.json();
  // An off-shape body would otherwise fill the form with a guess, and a
  // guess saved back is a change nobody made.
  if (!validSettings(data)) throw new Error("Connectivity settings unavailable.");
  return { ...data, etag: res.headers.get("ETag") };
}

const record = (value: unknown) => (value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null);
const strings = (value: unknown): value is string[] => Array.isArray(value) && value.every((item) => typeof item === "string");

function validSetting(value: unknown, check: (inner: unknown) => boolean): boolean {
  const s = record(value);
  return !!s && check(s.value) && ["default", "database", "pinned"].includes(s.source as string) &&
    (s.source === "pinned" ? typeof s.pinned_by === "string" : s.pinned_by === null);
}

function validSettings(value: unknown): value is Omit<ConnectivitySettings, "etag"> {
  const d = record(value);
  return !!d && validSetting(d.enabled, (v) => typeof v === "boolean") && validSetting(d.targets, strings) &&
    strings(d.default_targets) && Number.isInteger(d.max_targets) && (d.max_targets as number) > 0;
}

/**
 * The target list as typed: one address per line, blank lines ignored.
 *
 * A comma separates too, so the value of SUBGLANCE_CONNECTIVITY_TARGETS can
 * be pasted as it is. That cannot split a valid address: the server refuses
 * a comma in a host, and a port is digits.
 */
export function parseTargets(text: string): string[] {
  return text.split(/[\n,]/).map((item) => item.trim()).filter((item) => item !== "");
}
