import type { Account } from "./api";

// The operator who set the instance up, and one read-only second account.
export const twoUsers: Account[] = [
  { id: 1, email: "operator@example.com", role: "admin", created_at: "2026-01-02T09:00:00Z" },
  { id: 2, email: "oncall@example.com", role: "viewer", created_at: "2026-02-18T09:00:00Z" },
];
