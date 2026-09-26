import { apiFetch } from "../api/http";

/** GET /api/v1/backup. Every nullable field is null until the event has happened in this process. */
export interface BackupState {
  configured: boolean;
  target: string | null;
  last_success_at: string | null;
  last_object: string | null;
  last_size_bytes: number | null;
  last_error: string | null;
  last_error_at: string | null;
  failures: number;
}

export const backupKey = ["backup"] as const;

export async function fetchBackup(signal?: AbortSignal): Promise<BackupState> {
  const response = await apiFetch("/api/v1/backup", { signal, cache: "no-store" });
  if (!response.ok) throw new Error("Backup state unavailable.");
  const data: unknown = await response.json();
  // A 503 or an off-shape body must never read as "not configured": that is
  // the one answer that tells an operator there is nothing to worry about.
  if (!validState(data)) throw new Error("Backup state unavailable.");
  const { configured, target, last_success_at, last_object, last_size_bytes, last_error, last_error_at, failures } = data;
  return { configured, target, last_success_at, last_object, last_size_bytes, last_error, last_error_at, failures };
}

const isDate = (value: unknown) => value === null || (typeof value === "string" && /^\d{4}-\d{2}-\d{2}T/.test(value) && Number.isFinite(Date.parse(value)));
const isText = (value: unknown) => value === null || typeof value === "string";
const isCount = (value: unknown) => typeof value === "number" && Number.isInteger(value) && value >= 0;

function validState(value: unknown): value is BackupState {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const d = value as Record<string, unknown>;
  if (typeof d.configured !== "boolean" || !isCount(d.failures)) return false;
  if (![d.last_success_at, d.last_error_at].every(isDate) || ![d.target, d.last_object, d.last_error].every(isText)) return false;
  if (d.last_size_bytes !== null && !isCount(d.last_size_bytes)) return false;
  // A success and an error each arrive as a set; half of one is a malformed body.
  if ((d.last_success_at === null) !== (d.last_object === null) || (d.last_success_at === null) !== (d.last_size_bytes === null)) return false;
  if ((d.last_error === null) !== (d.last_error_at === null)) return false;
  if (!d.configured) return d.target === null && d.last_success_at === null && d.last_error === null && d.failures === 0;
  return typeof d.target === "string";
}
