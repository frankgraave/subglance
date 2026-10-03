/**
 * The three roles, as the server names them (internal/store/users.go).
 *
 * One list for accounts and tokens alike: the server gives both the same three
 * roles, and two copies of the list on this side of the wire had already
 * started to disagree about their order.
 */
export type Role = "admin" | "editor" | "viewer";

/** Least privilege first: the order every role choice draws them in. */
export const ROLES: readonly Role[] = ["viewer", "editor", "admin"];

/** Whether the server's answer names a role this build knows. */
export function isRole(value: unknown): value is Role {
  return typeof value === "string" && (ROLES as readonly string[]).includes(value);
}

/** The role as a word on screen: "Viewer". */
export const roleLabel = (role: Role) => role[0].toUpperCase() + role.slice(1);
