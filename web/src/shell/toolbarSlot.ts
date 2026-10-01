/**
 * Where the page toolbar's portal slot lives, as a store rather than a DOM
 * lookup (SUB-131, SUB-138, SUB-182).
 *
 * The shell renders an empty element for the current route's controls to be
 * portalled into, and the screens that fill it are rendered below the shell
 * in the tree — so a consumer cannot simply read the node during render. The
 * obvious repair is an effect calling `getElementById`, and that is a render
 * triggered by a render with no correct dependency list: the node has to be
 * re-read whenever the shell swaps a bar out (the status wall removes the
 * chrome entirely), and "whenever" is not something a dependency array can
 * spell.
 *
 * So the slot publishes itself through a ref callback, which React calls
 * exactly when the node attaches and detaches, and consumers subscribe. No
 * effect, no cascading render, and a bar that unmounts tells its tenants
 * instead of leaving them portalling into a detached element.
 *
 * **One slot, because only one bar takes a screen's controls (SUB-182).** The
 * masthead used to have a slot as well, for each screen's search field, and a
 * slot in the masthead is an open invitation to put one screen's control in
 * the bar every screen shares. The masthead now holds only what works on
 * every screen and renders it itself, so there is nothing for a screen to
 * portal into it — and no way to do it by accident.
 *
 * Its own module because it is not a component: keeping it beside a component
 * would cost that file fast refresh.
 */

type Slot = {
  set: (node: HTMLElement | null) => void;
  subscribe: (listener: () => void) => () => void;
  get: () => HTMLElement | null;
};

function createSlot(): Slot {
  let node: HTMLElement | null = null;
  const listeners = new Set<() => void>();
  return {
    set(next) {
      if (node === next) return;
      node = next;
      for (const listener of listeners) listener();
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    get() {
      return node;
    },
  };
}

const toolbar = createSlot();

/** Ref callback for the toolbar's slot. Called by `PageToolbar`, and nobody else. */
export const setToolbarSlot = toolbar.set;
export const subscribeToToolbarSlot = toolbar.subscribe;
export const getToolbarSlot = toolbar.get;

/*
 * The server snapshot is `null`, which is the honest answer: there is no DOM
 * to portal into during a server render, so a screen contributes no tools and
 * the bar renders empty rather than throwing.
 */
export function getToolbarSlotServerSnapshot(): HTMLElement | null {
  return null;
}
