import { lazy } from "react";
import type { ComponentType, LazyExoticComponent } from "react";

/**
 * A `lazy()` component that can be asked for again.
 *
 * `lazy()` keeps a rejected import for good: once its chunk has failed to
 * load, rendering it again rethrows the same error without asking the
 * network. A "Try again" button on such a component would do nothing. So the
 * instance is replaceable: `renew()` builds a fresh `lazy()` around the same
 * loader, and the next `current()` returns it.
 *
 * It is held per form, not per mount, so a drawer that opens a second time
 * gets the instance that has already loaded and renders the form at once
 * instead of flashing its fallback.
 */
export function retryableLazy<P extends object>(load: () => Promise<{ default: ComponentType<P> }>) {
  let instance: LazyExoticComponent<ComponentType<P>> = lazy(load);
  return {
    current: () => instance,
    renew: () => {
      instance = lazy(load);
      return instance;
    },
  };
}

export type RetryableLazy<P extends object> = ReturnType<typeof retryableLazy<P>>;
