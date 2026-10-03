import { lazy, Suspense } from "react";
import { Drawer } from "../components/Drawer";
import { Panel } from "../components/Card";
import type { MaintenanceMonitor } from "./maintenanceScope";

const MaintenanceManager = lazy(() => import("./Maintenance"));

/**
 * Scheduled maintenance, in a drawer over the screen that asked for it.
 *
 * It used to be a collapsed card under the whole inventory, which put the
 * thing planned minutes before a change below every monitor on the page — on
 * a phone, a long scroll from anything. A drawer opens from a button in the
 * inventory's header and from a monitor's own page, so it is one press from
 * either place someone is when they decide to do maintenance, and the list
 * behind it stays where it was.
 *
 * `className="maintenance"` is the name the schedule's styles and its browser
 * test have always used for the surface around it.
 */
export function MaintenanceDrawer({
  open,
  onClose,
  monitors,
  canWrite,
  focus,
}: {
  open: boolean;
  onClose: () => void;
  monitors: readonly MaintenanceMonitor[];
  canWrite: boolean;
  /** One monitor's view of the schedule, from its detail page. */
  focus?: MaintenanceMonitor;
}) {
  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={focus === undefined ? "Scheduled maintenance" : `Maintenance for ${focus.name}`}
      className="maintenance"
    >
      <Panel>
        <Suspense fallback={<p className="field-help">Loading maintenance…</p>}>
          <MaintenanceManager monitors={monitors} canWrite={canWrite} focus={focus} />
        </Suspense>
      </Panel>
    </Drawer>
  );
}
