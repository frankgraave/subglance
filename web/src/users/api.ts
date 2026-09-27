import { apiJSON, apiPost, apiRequest } from "../api/http";

export type UserRole = "admin" | "editor" | "viewer";

/** An account as the server lists it. Password hashes never leave the server. */
export interface Account {
  id: number;
  email: string;
  role: UserRole;
  created_at: string;
}

export const usersKey = ["users"] as const;
export const ROLES: readonly UserRole[] = ["viewer", "editor", "admin"];

function validAccount(value: unknown): value is Account {
  if (!value || typeof value !== "object") return false;
  const a = value as Record<string, unknown>;
  return typeof a.id === "number" && typeof a.email === "string" &&
    ROLES.includes(a.role as UserRole) && typeof a.created_at === "string";
}

function account(data: unknown): Account {
  if (!validAccount(data)) throw new Error("The server's answer about this account was unreadable. Reload the page.");
  return data;
}

export async function fetchUsers(signal?: AbortSignal): Promise<Account[]> {
  const data = await apiJSON<unknown>("/api/v1/users", { signal, cache: "no-store" });
  // A role misread here would be offered back in the role picker and could be
  // saved that way, so anything off-shape is an error rather than a guess.
  const users = (data as { users?: unknown } | null)?.users;
  if (!Array.isArray(users) || !users.every(validAccount)) throw new Error("Users unavailable.");
  return users;
}

export async function createUser(body: { email: string; password: string; role: UserRole }): Promise<Account> {
  const res = await apiPost("/api/v1/users", body);
  return account(await res.json());
}

export async function setUserRole(id: number, role: UserRole): Promise<Account> {
  const res = await apiRequest(`/api/v1/users/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ role }),
  });
  return account(await res.json());
}

export async function deleteUser(id: number): Promise<void> {
  await apiRequest(`/api/v1/users/${id}`, { method: "DELETE" });
}
