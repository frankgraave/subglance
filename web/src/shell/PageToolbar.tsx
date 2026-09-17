import type { ReactNode } from "react";
import { setToolbarSlot } from "./topbarSlot";

/**
 * The page toolbar: the bar under the masthead, whose contents change with the
 * route (SUB-138).
 *
 * The split is the whole idea. The masthead above is identical on every screen
 * — sidebar, search, layout, workbench, theme — so a control there never moves
 * when you navigate. This bar is the opposite: everything in it belongs to the
 * screen you are on, and it is expected to change completely between routes.
 * Sorting, grouping, filtering and the counts that describe them live here.
 *
 * Two bars rather than one crowded row, because the two have different
 * contracts with the reader. Chrome that is always in the same place can be
 * reached without looking; chrome that changes has to be read. Mixing them
 * means everything has to be read every time.
 *
 * A screen with no tools renders nothing into the slot, and the bar collapses
 * to nothing rather than leaving an empty strip: an always-present bar that is
 * usually empty is a promise the product does not keep.
 *
 * The controls in it are portalled from the screen through `ToolbarTools`, so
 * the filter state stays inside the screen that filters. See `TopbarTools` for
 * why a portal rather than props.
 */
export function PageToolbar({ children }: { children?: ReactNode }) {
  return (
    <div className="shell-toolbar" data-testid="page-toolbar">
      <div ref={setToolbarSlot} className="shell-toolbar-slot" />
      {children}
    </div>
  );
}
