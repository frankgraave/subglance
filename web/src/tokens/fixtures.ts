import type { ApiToken } from "./api";

// One token in each state the list draws: in use, never used, revoked.
export const sampleTokens: ApiToken[] = [
  { id: 3, name: "grafana", prefix: "sgp_2c77ab", role: "viewer", created_at: "2026-09-20T08:00:00Z", expires_at: "2027-09-20T08:00:00Z" },
  { id: 2, name: "ci-deploy", prefix: "sgp_9a41f0", role: "editor", created_at: "2026-09-01T08:00:00Z", last_used_at: "2026-09-25T21:10:00Z" },
  { id: 1, name: "laptop", prefix: "sgp_b013c2", role: "admin", created_at: "2026-08-11T08:00:00Z", revoked_at: "2026-09-02T08:00:00Z" },
];
