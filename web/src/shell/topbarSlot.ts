/**
 * Where the topbar's page slot lives, as a store rather than a DOM lookup
 * (SUB-131).
 *
 * `Topbar` renders an empty element for the current route's controls to be
 * portalled into, and the screens that fill it are rendered below the topbar
 * in the tree — so a consumer cannot simply read the node during render. The
 * obvious repair is an effect calling `getElementById`, and that is a render
 * triggered by a render with no correct dependency list: the node has to be
 * re-read whenever the shell swaps the topbar out (the status wall removes it
 * entirely), and "whenever" is not something a dependency array can spell.
 *
 * So the slot publishes itself through a ref callback, which React calls
 * exactly when the node attaches and detaches, and consumers subscribe. No
 * effect, no cascading render, and a topbar that unmounts tells its tenants
 * instead of leaving them portalling into a detached element.
 *
 * Its own module because it is not a component: keeping it beside `Topbar`
 * cost that file fast refresh, which is a real tax on the one file a designer
 * edits most.
 */

let slot: HTMLElement | null = null;
const listeners = new Set<() => void>();

/** Ref callback for the slot element. Called by `Topbar`, and by nobody else. */
export function setTopbarSlot(node: HTMLElement | null): void {
  if (slot === node) return;
  slot = node;
  for (const listener of listeners) listener();
}

export function subscribeToTopbarSlot(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getTopbarSlot(): HTMLElement | null {
  return slot;
}

/*
 * The server snapshot is `null`, which is the honest answer: there is no DOM
 * to portal into during a server render, so a screen contributes no tools and
 * the bar renders empty rather than throwing.
 */
export function getTopbarSlotServerSnapshot(): HTMLElement | null {
  return null;
}
