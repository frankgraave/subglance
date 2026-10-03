import { useId } from "react";
import type { ReactNode } from "react";

/**
 * Who a monitor alerts, and how it repeats itself, under one heading.
 *
 * Both monitor forms end in the collapsed "Advanced options" panel's
 * neighbourhood, and the channel list and the repeat controls used to follow
 * it with nothing in between, so they read as the panel's contents spilling
 * out of it. A heading and a rule above them say they are a group of their
 * own: what happens after a check fails, rather than how the check runs.
 *
 * An `h3` because the drawer's title is the `h2` above it; on the row role,
 * the next rung under the card title the drawer wears (DESIGN.md §2.5).
 */
export function AlertingSection({ children }: { children: ReactNode }) {
  const id = useId();
  return <section className="mon-form-section" aria-labelledby={id}>
    <h3 id={id} className="mon-form-section-title">Alerts</h3>
    {children}
  </section>;
}
