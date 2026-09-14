/**
 * The push-monitor helpers that are not components.
 *
 * They live apart from `PushUrlReveal` and `AddMonitorForm` so those files
 * export a component and nothing else, which is what keeps fast refresh
 * working on them during development.
 */

import type { AddMonitorValues } from "./AddMonitorForm";

/** True when the chosen type is a push monitor, which is probed by nobody. */
export function isPush(values: AddMonitorValues): boolean {
  return values.type === "push";
}

/**
 * The command to paste into a cron line.
 *
 * `-f` so an HTTP error is a non-zero exit rather than a silent success, `-sS`
 * so a failure still says why without a progress meter landing in cron's mail.
 * The URL is quoted because the failure-reporting form carries `?status=$?`,
 * which an unquoted shell would expand and glob before curl ever saw it.
 */
export function curlLine(url: string): string {
  return `curl -fsS ${JSON.stringify(url)} > /dev/null`;
}

/** The same, reporting the exit code of the job that just ran. */
export function curlStatusLine(url: string): string {
  return `curl -fsS ${JSON.stringify(`${url}?status=$?`)} > /dev/null`;
}
