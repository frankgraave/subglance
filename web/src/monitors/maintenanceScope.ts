/*
 * Beside Maintenance.tsx rather than in it: that file is a component module,
 * loaded lazily, and helpers exported from it would both break fast refresh
 * and pull the whole schedule into whichever bundle imported a type.
 */

/** What the manager needs to know about a monitor: who it is, and its tags. */
export type MaintenanceMonitor = { id: string; name: string; tags?: Readonly<Record<string, string>> };

/**
 * Whether a window covers a monitor: named for it, or for a tag pair it
 * carries. The same two scopes the server matches on, so the detail page
 * lists exactly the windows that suppress this monitor's alerts.
 */
export function windowCovers(window: { monitor_id?: number; tag_key?: string; tag_value?: string }, monitor: MaintenanceMonitor): boolean {
  if (window.monitor_id) return String(window.monitor_id) === monitor.id;
  return window.tag_key !== undefined && monitor.tags?.[window.tag_key] === window.tag_value;
}
