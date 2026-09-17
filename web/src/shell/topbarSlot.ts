/**
 * Where the shell's portal slots live, as a store rather than a DOM lookup
 * (SUB-131, extended in SUB-138).
 *
 * The shell renders empty elements for the current route's controls to be
 * portalled into, and the screens that fill them are rendered below the shell
 * in the tree — so a consumer cannot simply read the node during render. The
 * obvious repair is an effect calling `getElementById`, and that is a render
 * triggered by a render with no correct dependency list: the node has to be
 * re-read whenever the shell swaps a bar out (the status wall removes the
 * chrome entirely), and "whenever" is not something a dependency array can
 * spell.
 *
 * So each slot publishes itself through a ref callback, which React calls
 * exactly when the node attaches and detaches, and consumers subscribe. No
 * effect, no cascading render, and a bar that unmounts tells its tenants
 * instead of leaving them portalling into a detached element.
 *
 * **Two slots, because the shell has two bars (SUB-138).** The masthead is the
 * same on every screen; the toolbar beneath it carries the tools of the screen
 * you are on. They are separate stores rather than one keyed map so that a
 * screen contributing toolbar controls cannot accidentally land them in the
 * masthead — the name of the hook is the whole guarantee.
 *
 * Its own module because it is not a component: keeping it beside `Topbar`
 * cost that file fast refresh, which is a real tax on the one file a designer
 * edits most.
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

const topbar = createSlot();
const toolbar = createSlot();

/** Ref callback for the masthead's slot. Called by `Topbar`, and nobody else. */
export const setTopbarSlot = topbar.set;
export const subscribeToTopbarSlot = topbar.subscribe;
export const getTopbarSlot = topbar.get;

/** Ref callback for the toolbar's slot. Called by `PageToolbar`, and nobody else. */
export const setToolbarSlot = toolbar.set;
export const subscribeToToolbarSlot = toolbar.subscribe;
export const getToolbarSlot = toolbar.get;

/*
 * The server snapshot is `null`, which is the honest answer: there is no DOM
 * to portal into during a server render, so a screen contributes no tools and
 * the bar renders empty rather than throwing.
 */
export function getTopbarSlotServerSnapshot(): HTMLElement | null {
  return null;
}
