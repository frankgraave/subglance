import { causeWords } from "../incidents/story";
import type { Monitor } from "./types";

/**
 * Why a monitor is not fine, said short, or null when there is nothing to say.
 *
 * One rule for every dashboard layout that prints the why in a chip: the rows
 * layout put it under the name in SUB-194, and the compact layout used to
 * decide for itself, on `down` only, and print the raw error. Two copies of
 * the rule is how they drifted apart, so both read this one.
 *
 * - Which statuses say why: `down` (it is failing), `expiring` (a certificate
 *   or a registration is running out) and `unknown` (a domain registry could
 *   not say). The rest have no failure to name: `warning` is a failure still
 *   under its confirmation threshold, whose lamp word already says so.
 * - In which words: the failure kind as the incident row says it
 *   (`causeWords`), so the dashboard and the incident agree. A failure the
 *   server did not class falls back to its own message.
 * - Nothing without an error: a monitor that is down with no message has no
 *   why to show, and an empty chip would claim one.
 */
export function monitorCause(monitor: Pick<Monitor, "status" | "error" | "failureKind">): string | null {
  const { status, error } = monitor;
  if (status !== "down" && status !== "expiring" && status !== "unknown") return null;
  if (!error) return null;
  return causeWords(monitor.failureKind) ?? error;
}
