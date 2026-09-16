import type { User } from "./api";

/**
 * Whether this account may change anything.
 *
 * The server's own rule, in one place on this side of the wire:
 * `store.Role.CanWrite` is admin or editor (internal/store/users.go), and
 * every write route is wrapped in `requireWrite`. A viewer who is offered a
 * Pause button gets a 403 for their trouble, which reads as a broken instance
 * rather than as a permission they do not have — so the controls are absent
 * instead, and the screen says why.
 *
 * Unknown roles are treated as read-only. A server newer than this build could
 * name a role this list has never heard of, and guessing "probably allowed"
 * would offer controls that fail; guessing "not allowed" only hides controls
 * that would have worked, which is the recoverable direction.
 */
export function canWrite(user: Pick<User, "role"> | undefined): boolean {
  return user?.role === "admin" || user?.role === "editor";
}
