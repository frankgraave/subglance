import type { ApiHeartbeat } from "./types";

/** Raw detail-only history; never added to the live dashboard beat cache. */
export type ResponseHeartbeat = ApiHeartbeat & {
  response?: {
    body: string;
    headers?: Record<string, string>;
    truncated?: boolean;
  } | null;
  /** Absent on old rows or when no historical decision was recorded. */
  response_capture_reason?: "disabled" | "flapping" | "budget" | null;
};
