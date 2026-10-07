import { apiJSON, apiPost, apiRequest } from "../api/http";

/**
 * A browser session as the server lists it. The token and its hash are never
 * here: `id` is a separate random name, used only to end the session.
 */
export interface BrowserSession {
  id: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  /** Read from the User-Agent header; empty when the server did not recognise it. */
  browser: string;
  platform: string;
  user_agent: string;
  ip: string;
  /** The session this page is running in. */
  current: boolean;
}

export const sessionsKey = ["sessions"] as const;
export const userSessionsKey = (userId: number) => ["users", userId, "sessions"] as const;

function validSession(value: unknown): value is BrowserSession {
  if (!value || typeof value !== "object") return false;
  const s = value as Record<string, unknown>;
  return typeof s.id === "string" && typeof s.created_at === "string" && typeof s.last_seen_at === "string" &&
    typeof s.expires_at === "string" && typeof s.browser === "string" && typeof s.platform === "string" &&
    typeof s.user_agent === "string" && typeof s.ip === "string" && typeof s.current === "boolean";
}

async function fetchList(path: string, signal?: AbortSignal): Promise<BrowserSession[]> {
  const data = await apiJSON<unknown>(path, { signal, cache: "no-store" });
  // A list misread here would put "Sign out" on the wrong row, or none on a
  // session someone came to end, so anything off-shape is an error.
  const sessions = (data as { sessions?: unknown } | null)?.sessions;
  if (!Array.isArray(sessions) || !sessions.every(validSession)) throw new Error("Sessions unavailable.");
  return sessions;
}

function endedCount(data: unknown): number {
  const ended = (data as { ended?: unknown } | null)?.ended;
  return typeof ended === "number" ? ended : 0;
}

/** The signed-in account's own sessions. */
export const fetchSessions = (signal?: AbortSignal) => fetchList("/api/v1/sessions", signal);

export async function endSession(id: string): Promise<void> {
  await apiRequest(`/api/v1/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** Every session of this account but the one this page runs in. */
export async function endOtherSessions(): Promise<number> {
  const res = await apiPost("/api/v1/sessions/end-others", {});
  return endedCount(await res.json());
}

/** Another account's sessions; administrators only. */
export const fetchUserSessions = (userId: number, signal?: AbortSignal) => fetchList(`/api/v1/users/${userId}/sessions`, signal);

export async function endUserSessions(userId: number): Promise<number> {
  const res = await apiRequest(`/api/v1/users/${userId}/sessions`, { method: "DELETE" });
  return endedCount(await res.json());
}
