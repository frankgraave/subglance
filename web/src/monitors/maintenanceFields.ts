/*
 * Which inputs the schedule form draws, so a refusal that names a field can
 * stand under that field. Beside Maintenance.tsx rather than in it for the
 * reason maintenanceScope.ts gives: that file is a lazily loaded component
 * module, and helpers exported from it break fast refresh.
 */

/** The request fields the form has an input for, in the shape it is in now. */
export function maintenanceInputs(scope: string, weekly: boolean): readonly string[] {
  return [
    "name",
    ...(scope === "monitor" ? ["monitor_id"] : ["tag_key", "tag_value"]),
    ...(weekly ? ["timezone", "weekdays", "local_time", "duration_minutes"] : ["starts_at", "ends_at"]),
  ];
}

/**
 * The input a server refusal belongs under, or null for the form-wide alert.
 *
 * A refusal with no field ("choose exactly one monitor or tag pair") spans
 * settings, so it has no single input to stand beside. One that names a field
 * the form does not draw in its current shape has none either: put under an
 * input that is not on screen, it would be a message nobody sees.
 */
export function refusalInput(field: string | null, scope: string, weekly: boolean): string | null {
  return field !== null && maintenanceInputs(scope, weekly).includes(field) ? field : null;
}
