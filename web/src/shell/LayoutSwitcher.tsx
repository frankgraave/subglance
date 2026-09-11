import { LAYOUTS, type LayoutId } from "./preferences";

/**
 * Picks one of the four views of the same data (DESIGN.md §7).
 *
 * A segmented control of real buttons, not a dropdown and not a route. A
 * dropdown costs a click to even see which layout you are in; a route would
 * turn a personal preference into something you can link someone else into.
 *
 * `aria-pressed` rather than a radio group: these are four toggle buttons over
 * one setting, they act on press, and the browser's radio semantics would add
 * arrow-key navigation that conflicts with the toolbar around it. Each button
 * carries its `title` as the hint from §7 so the difference between Rows and
 * Compact is discoverable without trying all four.
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
  const shown = effective ?? layout;
  return (
    <div className="shell-seg" role="group" aria-label="Dashboard layout">
      {LAYOUTS.map((option) => (
        <button
          key={option.id}
          type="button"
          className="shell-seg-button"
          aria-pressed={shown === option.id}
          title={option.hint}
          onClick={() => onChange(option.id)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
