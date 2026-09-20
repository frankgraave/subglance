/**
 * Mounted forms register only a dirty predicate, never their field values.
 * Drawer dismissal is scoped to its own DOM subtree; navigation checks all
 * mounted guards. Accepting discard clears the guard synchronously so a drawer
 * close that also navigates cannot ask twice. Cancel changes nothing.
 */
type LeaveGuard = {
  element: () => HTMLElement | null;
  dirty: () => boolean;
  discard: () => void;
};
const guards = new Set<LeaveGuard>();
const navigationCleanups = new Set<() => void>();

/** Local overlay owners survive same-screen navigation. Close their state,
 * including pending loads, without remounting the inventory and its filters. */
export function registerNavigationCleanup(close: () => void): () => void {
  navigationCleanups.add(close);
  return () => { navigationCleanups.delete(close); };
}

/** Approval and owner teardown are one operation. Scoped drawer dismissal
 * still uses confirmLeave: it must not close an unrelated overlay. */
export function confirmNavigation(): boolean {
  if (!confirmLeave()) return false;
  for (const close of navigationCleanups) close();
  return true;
}

export function registerLeaveGuard(guard: LeaveGuard): () => void {
  guards.add(guard);
  const unload = (event: BeforeUnloadEvent) => {
    if (!guard.dirty()) return;
    event.preventDefault();
    // Browsers own this dialog's wording and may suppress it without a prior
    // user gesture. No draft (including future headers/body) is persisted.
    event.returnValue = "";
  };
  window.addEventListener("beforeunload", unload);
  return () => { guards.delete(guard); window.removeEventListener("beforeunload", unload); };
}

export function confirmLeave(root?: HTMLElement | null): boolean {
  const dirty = [...guards].filter((guard) => guard.dirty() &&
    (root === undefined || (guard.element() !== null && root?.contains(guard.element()))));
  if (dirty.length === 0) return true;
  // Native confirm preserves the drawer's keyboard/focus handling and avoids
  // nesting a second custom modal. It deliberately has browser-owned styling.
  if (!window.confirm("Discard this unsaved monitor? Your changes will be lost.")) return false;
  for (const guard of dirty) guard.discard();
  return true;
}
