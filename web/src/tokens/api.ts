import { apiJSON, apiPost, apiRequest } from "../api/http";

export type TokenRole = "admin" | "editor" | "viewer";

/** An API token as the server lists it. The secret itself is never here. */
export interface ApiToken {
  id: number;
  name: string;
  prefix: string;
  role: TokenRole;
  created_at: string;
  expires_at?: string;
  last_used_at?: string;
  revoked_at?: string;
}

export const tokensKey = ["tokens"] as const;

const ROLES: readonly string[] = ["admin", "editor", "viewer"];
const optionalText = (value: unknown) => value === undefined || typeof value === "string";

function validToken(value: unknown): value is ApiToken {
  if (!value || typeof value !== "object") return false;
  const t = value as Record<string, unknown>;
  return typeof t.id === "number" && typeof t.name === "string" && typeof t.prefix === "string" &&
    ROLES.includes(t.role as string) && typeof t.created_at === "string" &&
    optionalText(t.expires_at) && optionalText(t.last_used_at) && optionalText(t.revoked_at);
}

export async function fetchTokens(signal?: AbortSignal): Promise<ApiToken[]> {
  const data = await apiJSON<unknown>("/api/v1/tokens", { signal, cache: "no-store" });
  // A token misread as live would be offered for use after it was revoked,
  // so anything off-shape is an error rather than a guess.
  const tokens = (data as { tokens?: unknown } | null)?.tokens;
  if (!Array.isArray(tokens) || !tokens.every(validToken)) throw new Error("API tokens unavailable.");
  return tokens;
}

/** The one response that carries the secret. */
export async function createToken(body: { name: string; role: TokenRole; expires_in?: string }): Promise<{ token: string; details: ApiToken }> {
  const res = await apiPost("/api/v1/tokens", body);
  const data = (await res.json()) as { token?: unknown; details?: unknown };
  if (typeof data.token !== "string" || !validToken(data.details)) throw new Error("The token was created, but the answer was unreadable. Revoke it and create another.");
  return { token: data.token, details: data.details };
}

export async function revokeToken(id: number): Promise<void> {
  await apiRequest(`/api/v1/tokens/${id}`, { method: "DELETE" });
}

/** The roles a token may be given: never more than the account's own. */
export function rolesWithin(role: string): TokenRole[] {
  if (role === "admin") return ["viewer", "editor", "admin"];
  if (role === "editor") return ["viewer", "editor"];
  return ["viewer"];
}
