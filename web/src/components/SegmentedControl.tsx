/**
 * A segmented control: three or four mutually exclusive options, all visible.
 *
 * The component exists because the same control was hand-rolled three times —
 * once in the toolbar and twice in the workbench — and the three copies had
 * already drifted apart in radius, tone and padding. DESIGN.md §7.2 states the
 * pattern, §7.8 now states the component.
 *
 * ## One selected state
 *
 * There used to be a `variant` prop here: neutral for a control that changes
 * the *view*, accent for one that changes *what data* you see. It is gone.
 * Measured against the reference, the distinction does not exist — its range
 * selector is a view control by our own definition and it fills the active
 * segment with the accent. The split produced a selected segment drawn as a
 * grey box, which reads as disabled rather than as chosen, and it made two
 * controls side by side in the same toolbar disagree about what "selected"
 * looks like.
 *
 * ## Why `aria-pressed` and not a radio group
 *
 * Same reason `LayoutSwitcher` chose it before this component existed: these
 * are toggle buttons over one setting, they act on press, and radio semantics
 * would add arrow-key navigation that conflicts with the toolbar around them.
 * The `role="group"` plus its label is what ties them together for a screen
 * reader.
 */
import type { ReactElement } from "react";

export type SegmentedOption<Id extends string> = {
  id: Id;
  /**
   * The button's accessible name, always.
   *
   * When `icon` is set this is not rendered as text — it becomes the
   * `aria-label` instead. A segment drawn as a glyph with no name is a button
   * a screen reader announces as "button", which is the accessibility bug
   * every icon-only control ships with unless someone insists otherwise.
   */
  label: string;
  /**
   * Draw this glyph instead of the label.
   *
   * For a control whose options *are* shapes — how many columns, which
   * density — where the icon says it faster than the word. A `hint` is
   * effectively required alongside it: the shape is only obvious to someone
   * who already knows what the control does.
   *
   * `ReactElement`, not `ReactNode`, and the difference is load-bearing:
   * `ReactNode` admits `false` and `null`, which are *present* as far as the
   * `icon === undefined` test below is concerned but render nothing. A segment
   * would then take the icon class, drop its label into `aria-label`, and draw
   * an empty 26px square — the one failure mode a control of glyphs cannot
   * recover from, since there is no text to fall back to.
   */
  icon?: ReactElement;
  /** Optional `title`, for a difference that is not obvious from the label. */
  hint?: string;
};

export type SegmentedControlProps<Id extends string> = {
  /** Names the group for a screen reader; the buttons alone are just words. */
  label: string;
  options: readonly SegmentedOption<Id>[];
  /** The option drawn as selected. */
  value: Id;
  onChange: (next: Id) => void;
  className?: string;
};

export function SegmentedControl<Id extends string>({
  label,
  options,
  value,
  onChange,
  className,
}: SegmentedControlProps<Id>) {
  return (
    <div
      className={className ? `segmented ${className}` : "segmented"}
      role="group"
      aria-label={label}
    >
      {options.map((option) => (
        <button
          key={option.id}
          type="button"
          className={
            option.icon === undefined
              ? "segmented-option"
              : "segmented-option segmented-option--icon"
          }
          aria-pressed={value === option.id}
          // An icon segment keeps its name in `aria-label`, since the glyph
          // carries no text for the accessibility tree to read.
          {...(option.icon === undefined ? {} : { "aria-label": option.label })}
          title={option.hint}
          onClick={() => onChange(option.id)}
        >
          {option.icon ?? option.label}
        </button>
      ))}
    </div>
  );
}
