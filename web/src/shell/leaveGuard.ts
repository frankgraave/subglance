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
