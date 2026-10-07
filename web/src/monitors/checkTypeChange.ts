/**
 * The add form's values after choosing a check type.
 *
 * A domain monitor refuses an interval under six hours, so choosing it moves
 * a shorter one to a day, the server's own default, rather than letting the
 * save fail on a field the user never touched. Leaving it puts the day back
 * to the form's default, because a day is not what anyone expects of an
 * HTTP check they did not configure.
 *
 * Its own module so the form file exports components only (fast refresh).
 */
export function withType<T extends { type: string; intervalS: number }>(
  values: T,
  type: string,
  formDefaultS: number,
): T {
  if (type === "domain" && values.intervalS < DOMAIN_MIN_INTERVAL_S) {
    return { ...values, type, intervalS: DOMAIN_INTERVAL_S };
  }
  if (values.type === "domain" && type !== "domain" && values.intervalS === DOMAIN_INTERVAL_S) {
    return { ...values, type, intervalS: formDefaultS };
  }
  return { ...values, type };
}

/** The shortest interval the API takes for a domain monitor. */
export const DOMAIN_MIN_INTERVAL_S = 6 * 3600;

/** A domain monitor's interval when none is given: once a day. */
export const DOMAIN_INTERVAL_S = 86400;
