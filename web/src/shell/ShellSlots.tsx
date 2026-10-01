import { setToolbarSlot } from "./toolbarSlot";

/**
 * The shell's portal target, for tests that render a screen on its own.
 *
 * A screen contributes its filters by portalling into the page toolbar
 * (SUB-138, SUB-182). A test that renders the screen without that target does
 * not fail — `ToolbarTools` renders `null` when there is no slot, which is
 * correct for the status wall — it silently drops every control the screen
 * contributes and then passes, having asserted nothing about them. That is the
 * worst of the three outcomes, so this exists to make wiring it one line.
 *
 * Deliberately not automatic: a test that genuinely wants the no-slot case
 * (the status wall renders no chrome) must be able to get it, and a global
 * setup file would take that away.
 */
export function ShellSlots() {
  return <div className="shell-toolbar-slot" ref={setToolbarSlot} />;
}
