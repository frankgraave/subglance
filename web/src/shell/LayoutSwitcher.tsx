import { SegmentedControl } from "../components/SegmentedControl";
import { LAYOUTS, type LayoutId } from "./preferences";

/**
 * Picks one of the four views of the same data (DESIGN.md §7).
 *
 * A segmented control of real buttons, not a dropdown and not a route. A
 * dropdown costs a click to even see which layout you are in; a route would
 * turn a personal preference into something you can link someone else into.
 *
 * The control itself is `SegmentedControl` — this layout switcher was the
 * first of three hand-rolled copies. It is the `view` variant by definition:
 * it changes which view of the monitors you get, never which monitors. Each
 * button carries its `title` as the hint from §7 so the difference between
 * Rows and Compact is discoverable without trying all four.
 */
export type LayoutSwitcherProps = {
  layout: LayoutId;
  onChange: (next: LayoutId) => void;
  /**
   * The layout actually on screen, when the viewport overrode the preference.
   *
   * Shown as pressed so the control never claims Rows while cards are
   * rendered: a toolbar that disagrees with the screen is worse than one that
   * admits the narrow viewport won.
   */
  effective?: LayoutId;
};

export function LayoutSwitcher({ layout, onChange, effective }: LayoutSwitcherProps) {
  return (
    <SegmentedControl
      label="Dashboard layout"
      options={LAYOUTS}
      value={effective ?? layout}
      onChange={onChange}
    />
  );
}
