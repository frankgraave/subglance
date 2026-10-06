import { formatCount } from "../format/format";
import { STATUS_LABEL } from "../monitors/format";
import { SUMMARY_ORDER, type Summary } from "../monitors/model";
import type { MonitorStatus } from "../monitors/types";

/**
 * Every non-zero count, worst first: "3 down · 2 warning · 1 paused · 20 up".
 *
 * The same counts, in the same order, as the dashboard's status tabs
 * (`SUMMARY_ORDER`). A summary that names only what is down tells a room that
 * everything else is fine, when two monitors may be warning and one may have
 * been switched off and forgotten.
 */
export function wallCounts(summary: Summary): { status: MonitorStatus; text: string }[] {
  return SUMMARY_ORDER.filter((status) => summary[status] > 0).map((status) => ({
    status,
    text: `${formatCount(summary[status])} ${STATUS_LABEL[status].toLowerCase()}`,
  }));
}
