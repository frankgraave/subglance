import type { MonitorStatus } from "./types";

/**
 * The lamp's visual states, and the mapping from a monitor's status to one.
 *
 * Its own module rather than an export from `Led.tsx`: a component file that
 * also exports constants breaks fast refresh, and the filter chips in the
 * toolbar need this mapping without needing the component. The alternative —
 * a second copy of the table — is how "paused" ends up hollow in one place and
 * grey in another.
 *
 * `idle` is a filled but unlit grey lamp: we have no reading. `off` is the
 * same silhouette with nothing in it: nobody is taking a reading, on purpose.
 * They are two states rather than one colour because they are two different
 * facts, and the shape — not the hue — is what separates them (DESIGN.md §3).
 */
export type LedState = "up" | "down" | "warn" | "idle" | "off";

export const LED_STATE: Record<MonitorStatus, LedState> = {
  up: "up",
  down: "down",
  // Pending is amber, not grey: it is a monitor we are waiting on, which is
  // worth a glance.
  pending: "warn",
  // Paused is hollow, not grey-filled. A grey fill is what "no reading yet"
  // looks like, and a paused monitor is not waiting for a reading — it was
  // switched off by a person. Sharing one signal meant a monitor someone
  // paused by accident was indistinguishable from one that had just started
  // (DESIGN.md rule 4).
  paused: "off",
  // Grey and filled: the lamp for a monitor with no reading at all. That is
  // exactly what a push monitor with no report yet is, and it is not amber —
  // amber means a check is in flight, and no check is running here. Nor is it
  // hollow: nobody switched this off, it has simply never been pinged.
  waiting: "idle",
};

/** How each status is spoken. Colour never stands alone (DESIGN.md §2.3). */
export const LED_LABELS: Record<MonitorStatus, string> = {
  up: "Up",
  down: "Down",
  pending: "Pending",
  paused: "Paused",
  waiting: "Waiting",
};
