import { describe, expect, it } from "vitest";
import { canWrite } from "./permissions";

describe("canWrite", () => {
  it("matches the server's own rule", () => {
    // store.Role.CanWrite is admin or editor (internal/store/users.go), and
    // every write route is wrapped in requireWrite.
    expect(canWrite({ role: "admin" })).toBe(true);
    expect(canWrite({ role: "editor" })).toBe(true);
    expect(canWrite({ role: "viewer" })).toBe(false);
  });

  it("treats an unknown role as read-only", () => {
    // A server newer than this build could name a role this list has never
    // heard of. Guessing "allowed" offers controls that 403 and reads as a
    // broken instance; guessing "not allowed" only hides controls that would
    // have worked, which is the recoverable direction.
    expect(canWrite({ role: "auditor" })).toBe(false);
    expect(canWrite(undefined)).toBe(false);
  });
});
